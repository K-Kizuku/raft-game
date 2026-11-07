// 接続 1 本を表す GameConnection（T-402 / プロトコル §10）。
// 接続・hello・再接続・seq の検査・ping・コマンドと ack を扱う。受けた値は保持せず、ハンドラーに渡すだけ。
import type {
  Ack,
  Clock,
  ErrorPayload,
  NodeDetail,
  RoundResult,
  ServerEvent,
  ServerMessage,
  Snapshot,
  SubEngineerPayload,
} from "../protocol/index.ts";
import {
  decodeServerMessage,
  encodeClientMessage,
  type ClientMessageType,
  type ClientPayload,
} from "./codec.ts";
import { acceptEvents, checkEnvelopeSeq } from "./sequence.ts";
import { createBrowserSocketFactory, type SocketFactory, type SocketLike } from "./socket.ts";
import { browserTimers, type TimerId, type Timers } from "./timers.ts";
import { buildWsUrl } from "./url.ts";

export const CLIENT_NAME = "web/0.1.0"; // hello の client

export type CommandType = Exclude<
  ClientMessageType,
  "hello" | "ping" | "cmd.sync" | "sub.engineer"
>;

export type ConnectionState =
  | "idle" // start() の前
  | "connecting" // 接続を開いている・開いてスナップショットを待っている
  | "ready" // スナップショットを受け、イベントを渡している
  | "syncing" // cmd.sync を送り、スナップショットを待っている
  | "reconnecting" // 切断された。次の接続までの待ち
  | "ended" // round.result を受けた（終わり）
  | "closed" // close() を呼んだ（終わり）
  | "failed"; // あきらめた（終わり）

export type FailureReason =
  "unreachable" | "gave_up" | "fatal_error" | "version_mismatch" | "replaced";

export type ConnectionStatus = Readonly<{
  state: ConnectionState;
  attempt: number; // 今の接続の試みが何回目か（1 から。idle では 0）
  reason: FailureReason | null; // failed のときだけ値がある
}>;

export type ConnectionHandlers = {
  onSnapshot(snapshot: Snapshot, receivedAt: number): void;
  onEvents(clock: Clock, events: readonly ServerEvent[], receivedAt: number): void;
  onNodeDetail?(detail: NodeDetail): void;
  onRoundResult?(result: RoundResult): void;
  onServerError?(error: ErrorPayload): void;
  onStatus?(status: ConnectionStatus): void;
  onPong?(rttMs: number): void;
};

export type ConnectionConfig = {
  pingIntervalMs: number; // 15000（プロトコル §4 の ping）
  idleTimeoutMs: number; // 45000（何も届かなければ切って再接続する）
  ackTimeoutMs: number; // 10000
  syncTimeoutMs: number; // 5000（cmd.sync の後、スナップショットを待つ時間）
  reconnectWindowMs: number; // 300000（切断から 5 分。DesignDoc §12.5 の保持時間）
  maxInitialAttempts: number; // 3（一度もスナップショットを受けていないときの試みの回数）
  backoffBaseMs: number; // 500
  backoffMaxMs: number; // 8000
};

export const DEFAULT_CONNECTION_CONFIG: Readonly<ConnectionConfig> = Object.freeze({
  pingIntervalMs: 15000,
  idleTimeoutMs: 45000,
  ackTimeoutMs: 10000,
  syncTimeoutMs: 5000,
  reconnectWindowMs: 300000,
  maxInitialAttempts: 3,
  backoffBaseMs: 500,
  backoffMaxMs: 8000,
});

export type CommandErrorReason = "not_connected" | "disconnected" | "timeout" | "closed";

export class CommandError extends Error {
  readonly reason: CommandErrorReason;

  constructor(reason: CommandErrorReason) {
    super(`command failed: ${reason}`);
    this.name = "CommandError";
    this.reason = reason;
  }
}

export type GameConnectionOptions = {
  session: { wsPath: string; token: string }; // セッションの作成（T-413）の 201 の wsPath・token
  handlers: ConnectionHandlers;
  socketFactory?: SocketFactory; // 既定 createBrowserSocketFactory()
  timers?: Timers; // 既定 browserTimers
  location?: { protocol: string; host: string }; // 既定 window.location
  config?: Partial<ConnectionConfig>;
};

export type GameConnection = {
  start(): void; // 1 回目だけ接続を始める。2 回目以降・終わった後は何もしない
  close(): void; // 終わりにする（closed）。再接続しない。何度呼んでもよい
  sendCommand<T extends CommandType>(type: T, payload: ClientPayload<T>): Promise<Ack>;
  setEngineerSubscription(payload: SubEngineerPayload): void;
  getStatus(): ConnectionStatus;
};

// Conn は接続 1 本の状態。古い接続からの呼び出しは、current と比べて捨てる。
type Conn = {
  socket: SocketLike | null;
  expectedSeq: number | null; // エンベロープの seq の次に来るはずの値
  gotSnapshot: boolean; // この接続でスナップショットを受けたか
  pingTimer: TimerId | null;
  idleTimer: TimerId | null;
  syncTimer: TimerId | null;
};

type PendingCommand = {
  resolve(ack: Ack): void;
  reject(error: CommandError): void;
  timer: TimerId;
};

function isTerminal(state: ConnectionState): boolean {
  return state === "ended" || state === "closed" || state === "failed";
}

// ハンドラーの例外でクライアントの状態を壊さない。例外はマイクロタスクで投げ直す。
function safeCall(fn: () => void): void {
  try {
    fn();
  } catch (error) {
    queueMicrotask(() => {
      throw error;
    });
  }
}

export function createGameConnection(options: GameConnectionOptions): GameConnection {
  const handlers = options.handlers;
  const timers = options.timers ?? browserTimers;
  const config: ConnectionConfig = { ...DEFAULT_CONNECTION_CONFIG, ...options.config };

  let status: ConnectionStatus = Object.freeze({ state: "idle", attempt: 0, reason: null });
  let started = false;
  let url = "";
  let socketFactory: SocketFactory | null = null;
  let current: Conn | null = null;
  let reconnectTimer: TimerId | null = null;
  let everSnapshot = false; // このクライアントがスナップショットを 1 度でも受けたか
  let lastEventSeq = 0;
  let reconnects = 0; // 今回の切断からの再接続の回数（スナップショットで 0 に戻す）
  let disconnectedAt: number | null = null; // 最後に ready だった後の最初の切断の時刻
  let cmdCounter = 0;
  const pending = new Map<string, PendingCommand>();
  let engineerSub: SubEngineerPayload | null = null;

  function setStatus(state: ConnectionState, attempt: number, reason: FailureReason | null): void {
    if (status.state === state && status.attempt === attempt && status.reason === reason) {
      return;
    }
    const next: ConnectionStatus = Object.freeze({ state, attempt, reason });
    status = next;
    if (handlers.onStatus !== undefined) {
      const onStatus = handlers.onStatus;
      safeCall(() => onStatus(next));
    }
  }

  function send(conn: Conn, data: string): boolean {
    if (conn.socket === null) {
      return false;
    }
    try {
      conn.socket.send(data);
      return true;
    } catch {
      return false;
    }
  }

  function rejectPending(reason: CommandErrorReason): void {
    const entries = [...pending.values()];
    pending.clear();
    for (const entry of entries) {
      timers.clearTimeout(entry.timer);
      entry.reject(new CommandError(reason));
    }
  }

  function clearConnTimers(conn: Conn): void {
    if (conn.pingTimer !== null) {
      timers.clearInterval(conn.pingTimer);
      conn.pingTimer = null;
    }
    if (conn.idleTimer !== null) {
      timers.clearTimeout(conn.idleTimer);
      conn.idleTimer = null;
    }
    if (conn.syncTimer !== null) {
      timers.clearTimeout(conn.syncTimer);
      conn.syncTimer = null;
    }
  }

  // detach は今の接続を切り離す（以後、その接続からの呼び出しは捨てる）。
  function detach(conn: Conn, closeCode?: number, closeReason?: string): void {
    clearConnTimers(conn);
    if (current === conn) {
      current = null;
    }
    if (closeCode !== undefined && conn.socket !== null) {
      const socket = conn.socket;
      try {
        socket.close(closeCode, closeReason);
      } catch {
        // すでに閉じている
      }
    }
  }

  function clearReconnectTimer(): void {
    if (reconnectTimer !== null) {
      timers.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  }

  function fail(reason: FailureReason): void {
    clearReconnectTimer();
    if (current !== null) {
      detach(current, 1000);
    }
    rejectPending("disconnected");
    setStatus("failed", status.attempt, reason);
  }

  function windowExpired(): boolean {
    return disconnectedAt !== null && timers.now() - disconnectedAt > config.reconnectWindowMs;
  }

  function scheduleReconnect(): void {
    if (everSnapshot) {
      if (disconnectedAt === null) {
        disconnectedAt = timers.now();
      }
      if (windowExpired()) {
        fail("gave_up");
        return;
      }
    } else if (status.attempt >= config.maxInitialAttempts) {
      fail("unreachable");
      return;
    }
    reconnects++;
    const delay = Math.min(config.backoffBaseMs * 2 ** (reconnects - 1), config.backoffMaxMs);
    setStatus("reconnecting", status.attempt + 1, null);
    reconnectTimer = timers.setTimeout(() => {
      reconnectTimer = null;
      if (status.state !== "reconnecting") {
        return;
      }
      if (everSnapshot && windowExpired()) {
        fail("gave_up");
        return;
      }
      connect();
    }, delay);
  }

  // handleDisconnect は切断（onClose・自分から切った）を扱う。
  function handleDisconnect(conn: Conn, code: number): void {
    if (current !== conn) {
      return;
    }
    detach(conn);
    rejectPending("disconnected");
    if (isTerminal(status.state)) {
      return;
    }
    if (code === 4001) {
      fail("replaced");
      return;
    }
    scheduleReconnect();
  }

  function closeAndReconnect(conn: Conn, reason: string): void {
    if (current !== conn) {
      return;
    }
    if (conn.socket !== null) {
      try {
        conn.socket.close(4000, reason);
      } catch {
        // すでに閉じている
      }
    }
    handleDisconnect(conn, 4000);
  }

  function resetIdle(conn: Conn): void {
    if (conn.idleTimer !== null) {
      timers.clearTimeout(conn.idleTimer);
    }
    conn.idleTimer = timers.setTimeout(() => {
      conn.idleTimer = null;
      closeAndReconnect(conn, "idle");
    }, config.idleTimeoutMs);
  }

  function requestSync(conn: Conn): void {
    if (status.state === "syncing") {
      return;
    }
    send(conn, encodeClientMessage("cmd.sync", {}));
    setStatus("syncing", status.attempt, null);
    conn.syncTimer = timers.setTimeout(() => {
      conn.syncTimer = null;
      closeAndReconnect(conn, "sync timeout");
    }, config.syncTimeoutMs);
  }

  function sendEngineerSubscription(conn: Conn): void {
    if (engineerSub !== null) {
      send(conn, encodeClientMessage("sub.engineer", engineerSub));
    }
  }

  function handleMessage(conn: Conn, message: ServerMessage): void {
    const receivedAt = timers.now();
    switch (message.type) {
      case "snapshot": {
        const snapshot = message.payload;
        conn.gotSnapshot = true;
        everSnapshot = true;
        lastEventSeq = snapshot.lastSeq;
        reconnects = 0;
        disconnectedAt = null;
        if (conn.syncTimer !== null) {
          timers.clearTimeout(conn.syncTimer);
          conn.syncTimer = null;
        }
        safeCall(() => handlers.onSnapshot(snapshot, receivedAt));
        if (current !== conn) {
          return;
        }
        setStatus("ready", status.attempt, null);
        if (engineerSub !== null && engineerSub.enabled) {
          sendEngineerSubscription(conn);
        }
        return;
      }
      case "events": {
        if (!conn.gotSnapshot || status.state === "syncing") {
          return;
        }
        const result = acceptEvents(lastEventSeq, message.payload.events);
        lastEventSeq = result.lastSeq;
        const clock = message.payload.clock;
        safeCall(() => handlers.onEvents(clock, result.accepted, receivedAt));
        if (result.gap && current === conn) {
          requestSync(conn);
        }
        return;
      }
      case "ack": {
        const ack = message.payload;
        const entry = pending.get(ack.cmdId);
        if (entry === undefined) {
          return;
        }
        pending.delete(ack.cmdId);
        timers.clearTimeout(entry.timer);
        entry.resolve(ack);
        return;
      }
      case "node.detail": {
        const detail = message.payload;
        if (handlers.onNodeDetail !== undefined) {
          const onNodeDetail = handlers.onNodeDetail;
          safeCall(() => onNodeDetail(detail));
        }
        return;
      }
      case "round.result": {
        const result = message.payload;
        if (handlers.onRoundResult !== undefined) {
          const onRoundResult = handlers.onRoundResult;
          safeCall(() => onRoundResult(result));
        }
        clearReconnectTimer();
        detach(conn, 1000, "round ended");
        rejectPending("disconnected");
        if (!isTerminal(status.state)) {
          setStatus("ended", status.attempt, null);
        }
        return;
      }
      case "error": {
        const error = message.payload;
        if (handlers.onServerError !== undefined) {
          const onServerError = handlers.onServerError;
          safeCall(() => onServerError(error));
        }
        if (error.fatal && !isTerminal(status.state)) {
          fail("fatal_error");
        }
        return;
      }
      case "pong": {
        const rtt = timers.now() - message.payload.clientTime;
        if (Number.isFinite(rtt) && rtt >= 0 && handlers.onPong !== undefined) {
          const onPong = handlers.onPong;
          safeCall(() => onPong(rtt));
        }
        return;
      }
    }
  }

  function onMessage(conn: Conn, data: unknown): void {
    if (current !== conn) {
      return;
    }
    resetIdle(conn);
    const decoded = decodeServerMessage(data);
    switch (decoded.kind) {
      case "malformed":
        return;
      case "version_mismatch":
        fail("version_mismatch");
        return;
      case "unknown": {
        const verdict = checkEnvelopeSeq(conn.expectedSeq, decoded.seq);
        conn.expectedSeq = verdict.expected;
        if (verdict.verdict === "gap") {
          requestSync(conn);
        }
        return;
      }
      case "message": {
        const verdict = checkEnvelopeSeq(conn.expectedSeq, decoded.message.seq);
        conn.expectedSeq = verdict.expected;
        if (verdict.verdict === "duplicate") {
          return;
        }
        if (verdict.verdict === "gap") {
          requestSync(conn);
        }
        if (current === conn) {
          handleMessage(conn, decoded.message);
        }
        return;
      }
    }
  }

  function onOpen(conn: Conn): void {
    if (current !== conn) {
      return;
    }
    send(conn, encodeClientMessage("hello", { client: CLIENT_NAME, resume: everSnapshot }));
    conn.pingTimer = timers.setInterval(() => {
      send(conn, encodeClientMessage("ping", { clientTime: timers.now() }));
    }, config.pingIntervalMs);
    resetIdle(conn);
  }

  function connect(): void {
    if (socketFactory === null) {
      return;
    }
    setStatus("connecting", status.attempt, null);
    const conn: Conn = {
      socket: null,
      expectedSeq: null,
      gotSnapshot: false,
      pingTimer: null,
      idleTimer: null,
      syncTimer: null,
    };
    current = conn;
    try {
      conn.socket = socketFactory(url, {
        onOpen: () => onOpen(conn),
        onMessage: (data) => onMessage(conn, data),
        onClose: (code) => handleDisconnect(conn, code),
        onError: () => {
          // onClose が続いて呼ばれる
        },
      });
    } catch {
      handleDisconnect(conn, 1006);
    }
  }

  return {
    start() {
      if (started) {
        return;
      }
      started = true;
      if (status.state !== "idle") {
        return;
      }
      try {
        url = buildWsUrl(options.session, options.location ?? window.location);
        socketFactory = options.socketFactory ?? createBrowserSocketFactory();
      } catch {
        setStatus("failed", 0, "unreachable");
        return;
      }
      setStatus("connecting", 1, null);
      connect();
    },

    close() {
      if (isTerminal(status.state)) {
        return;
      }
      started = true;
      clearReconnectTimer();
      if (current !== null) {
        detach(current, 1000, "closed");
      }
      rejectPending("closed");
      setStatus("closed", status.attempt, null);
    },

    sendCommand<T extends CommandType>(type: T, payload: ClientPayload<T>): Promise<Ack> {
      const conn = current;
      if ((status.state !== "ready" && status.state !== "syncing") || conn === null) {
        return Promise.reject(new CommandError("not_connected"));
      }
      cmdCounter++;
      const cmdId = `c-${cmdCounter}`;
      return new Promise<Ack>((resolve, reject) => {
        const timer = timers.setTimeout(() => {
          if (pending.delete(cmdId)) {
            reject(new CommandError("timeout"));
          }
        }, config.ackTimeoutMs);
        pending.set(cmdId, { resolve, reject, timer });
        if (!send(conn, encodeClientMessage(type, payload, cmdId))) {
          pending.delete(cmdId);
          timers.clearTimeout(timer);
          reject(new CommandError("disconnected"));
        }
      });
    },

    setEngineerSubscription(payload) {
      engineerSub = payload;
      const conn = current;
      if ((status.state === "ready" || status.state === "syncing") && conn !== null) {
        sendEngineerSubscription(conn);
      }
    },

    getStatus() {
      return status;
    },
  };
}
