// 記録を再生するサーバーのふり。開発のモック（app/dev）とリプレイ（ADR-0028）が使う（T-402 / プロトコル §10.6）。
// 「1 つのセッションを持つ Durable Object＋コンテナ」のふりをする。記録は書き換えない（コマンドで再生の中身は変わらない）。
// ブラウザの中だけの WS のふりで、外と通信しない。
import { Version } from "../../protocol/index.ts";
import type {
  Ack,
  AckCode,
  IslandState,
  RoundResult,
  Score,
  ServerEvent,
  StageView,
} from "../../protocol/index.ts";
import type { SocketFactory, SocketHandlers, SocketLike } from "../socket.ts";
import { browserTimers, type TimerId, type Timers } from "../timers.ts";
import { DEFAULT_ALLOWED_SPEEDS, snapshotFromRecord, type GameRecord } from "./record.ts";

export type ReceivedMessage = Readonly<{
  type: string;
  cmdId: string | null;
  payload: unknown;
  simTime: number;
}>;

export type AckPolicy = (
  msg: ReceivedMessage,
) => { ok: true } | { ok: false; code: AckCode; message: string };

export type RecordServerOptions = {
  timers?: Timers; // 既定 browserTimers（テストでは createManualTimers()）
  stage?: StageView; // snapshotFromRecord に渡す
  allowedSpeeds?: readonly number[]; // 既定 DEFAULT_ALLOWED_SPEEDS
  tickMs?: number; // 既定 100（プロトコル §5 の「100ms ごとにまとめて送る」）
  roundResult?: RoundResult; // 終わりに送るもの。無ければ placeholderRoundResult(record)
  ackPolicy?: AckPolicy; // 既定: すべて { ok: true }（リプレイでは readOnlyAckPolicy）
  islandsState?: readonly IslandState[]; // snapshotFromRecord に渡す（既定 []）
  score?: Score; // snapshotFromRecord に渡す（既定は 0 点）
  sessionId?: string; // snapshotFromRecord に渡す（既定 mock_<stageId>_<seed>）
};

export type RecordServer = {
  socketFactory: SocketFactory; // createGameConnection の socketFactory に渡す
  received(): readonly ReceivedMessage[]; // 受けたクライアントの電文（hello・ping も含む。受けた順）
  simTime(): number; // 再生しているゲーム内時刻
  disconnect(code?: number): void; // 今の接続をサーバー側から切る（既定 1006）
  dropNextServerMessage(): void; // 次に送る電文を 1 つ捨てる（seq は消費する）
  refuseConnections(count: number): void; // 次の count 回の接続を、開かずに閉じる（code 1006）
};

// placeholderRoundResult は確認用の RoundResult。画面に出す値として使わない。
export function placeholderRoundResult(record: GameRecord): RoundResult {
  let total = 0;
  for (let i = record.events.length - 1; i >= 0; i--) {
    const event = record.events[i];
    if (event !== undefined && event.type === "score.updated") {
      total = event.total;
      break;
    }
  }
  return {
    score: { total, breakdown: { requests: 0, expired: 0, ferryCost: 0, fairness: 0 } },
    stars: 0,
    stats: {
      succeeded: 0,
      rejected: 0,
      expired: 0,
      lettersDelivered: {},
      leaderChanges: 0,
      avgCommitLatencyMs: 0,
      longestSilenceMs: 0,
      finalLogs: [],
    },
    story: [],
    inputLogAvailable: false,
  };
}

type MockConn = {
  handlers: SocketHandlers;
  open: boolean;
  closed: boolean;
};

type Obj = Record<string, unknown>;

function isObject(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

const POLICY_TYPES: ReadonlySet<string> = new Set([
  "cmd.ferry.load",
  "cmd.ferry.unload",
  "cmd.ferry.autoload",
  "cmd.ferry.depart",
  "cmd.relay.load",
  "demo.trigger",
]);

// readOnlyAckPolicy は船などの操作（POLICY_TYPES）をすべて断る（リプレイ用）。
// 開始・一時停止・速度・「次の出来事まで」は AckPolicy の外で処理するので、今のまま使える。
export const readOnlyAckPolicy: AckPolicy = () => ({
  ok: false,
  code: "phase_invalid",
  message: "リプレイでは操作できません",
});

const SKIP_HORIZON_MS = 30000; // プロトコル §4 の cmd.game.skip「最大 30 秒先まで」
const EMPTY_EVENTS_EVERY_TICKS = 10; // プロトコル §5「1 秒に 1 回は送る」（tickMs 100 のとき）

export function createRecordServer(
  record: GameRecord,
  options: RecordServerOptions = {},
): RecordServer {
  const timers = options.timers ?? browserTimers;
  const allowedSpeeds = options.allowedSpeeds ?? DEFAULT_ALLOWED_SPEEDS;
  const tickMs = options.tickMs ?? 100;
  const ackPolicy: AckPolicy = options.ackPolicy ?? (() => ({ ok: true }));
  const events = record.events;
  const endsAt = record.snapshot.endsAt;
  const lastT = events.length > 0 ? (events[events.length - 1]?.t ?? 0) : record.snapshot.t;

  const received: ReceivedMessage[] = [];
  let current: MockConn | null = null;
  let envSeq = 0;
  let dropNext = false;
  let refuse = 0;

  let started = false;
  let finished = false;
  let paused = false;
  let speed = 1;
  let simTime = record.snapshot.t;
  let sentCount = 0; // 送ったイベントの数（記録の先頭から）
  let quietTicks = 0;
  let tickTimer: TimerId | null = null;

  function clock() {
    return { t: simTime, speed, paused, endsAt };
  }

  function deliverClose(conn: MockConn, code: number, reason: string): void {
    timers.setTimeout(() => conn.handlers.onClose(code, reason), 0);
  }

  function markClosed(conn: MockConn): void {
    conn.closed = true;
    conn.open = false;
    if (current === conn) {
      current = null;
    }
  }

  function sendToClient(type: string, payload: unknown): void {
    const conn = current;
    if (conn === null || conn.closed) {
      return;
    }
    envSeq++;
    if (dropNext) {
      dropNext = false;
      return;
    }
    const data = JSON.stringify({ v: Version, type, seq: envSeq, payload });
    timers.setTimeout(() => {
      if (!conn.closed) {
        conn.handlers.onMessage(data);
      }
    }, 0);
  }

  function sendAck(cmdId: string | null, result: ReturnType<AckPolicy>): void {
    const ack: Ack = result.ok
      ? { cmdId: cmdId ?? "", ok: true, t: simTime }
      : { cmdId: cmdId ?? "", ok: false, t: simTime, code: result.code, message: result.message };
    sendToClient("ack", ack);
  }

  function stopTicking(): void {
    if (tickTimer !== null) {
      timers.clearInterval(tickTimer);
      tickTimer = null;
    }
  }

  // flush は t <= simTime のまだ送っていないイベントをまとめて送る。最後まで送ったら round.result を送る。
  function flush(): boolean {
    const batch: ServerEvent[] = [];
    while (sentCount < events.length) {
      const event = events[sentCount];
      if (event === undefined || event.t > simTime) {
        break;
      }
      batch.push(event);
      sentCount++;
    }
    if (batch.length > 0) {
      sendToClient("events", { clock: clock(), events: batch });
      quietTicks = 0;
    }
    if (started && !finished && sentCount >= events.length) {
      finished = true;
      stopTicking();
      sendToClient("round.result", options.roundResult ?? placeholderRoundResult(record));
    }
    return batch.length > 0;
  }

  function tick(): void {
    if (finished || paused || current === null || !current.open) {
      return;
    }
    simTime = Math.min(simTime + tickMs * speed, lastT);
    if (!flush()) {
      quietTicks++;
      if (quietTicks >= EMPTY_EVENTS_EVERY_TICKS) {
        quietTicks = 0;
        sendToClient("events", { clock: clock(), events: [] });
      }
    }
  }

  function sendState(): void {
    sendToClient(
      "snapshot",
      snapshotFromRecord(record, {
        stage: options.stage,
        allowedSpeeds,
        islandsState: options.islandsState,
        score: options.score,
        sessionId: options.sessionId,
      }),
    );
    if (sentCount > 0) {
      sendToClient("events", { clock: clock(), events: events.slice(0, sentCount) });
    }
  }

  function skip(): void {
    for (let i = sentCount; i < events.length; i++) {
      const event = events[i];
      if (event !== undefined && event.type === "ferry.arrived") {
        if (event.t <= simTime + SKIP_HORIZON_MS) {
          simTime = Math.max(simTime, event.t);
        }
        break;
      }
    }
    paused = true;
    flush();
  }

  function handleClientMessage(conn: MockConn, data: string): void {
    let value: unknown;
    try {
      value = JSON.parse(data);
    } catch {
      value = undefined;
    }
    const obj: Obj = isObject(value) ? value : {};
    const msg: ReceivedMessage = {
      type: typeof obj["type"] === "string" ? obj["type"] : "",
      cmdId: typeof obj["cmdId"] === "string" ? obj["cmdId"] : null,
      payload: isObject(value) ? obj["payload"] : data,
      simTime,
    };
    received.push(msg);
    if (!isObject(value)) {
      return;
    }
    if (obj["v"] !== Version) {
      sendToClient("error", {
        code: "version_mismatch",
        message: "protocol version mismatch",
        fatal: true,
      });
      timers.setTimeout(() => {
        if (!conn.closed) {
          markClosed(conn);
          conn.handlers.onClose(1008, "version mismatch");
        }
      }, 0);
      return;
    }
    const payload: Obj = isObject(msg.payload) ? msg.payload : {};
    switch (msg.type) {
      case "hello":
      case "cmd.sync":
        sendState();
        return;
      case "ping":
        sendToClient("pong", {
          clientTime: typeof payload["clientTime"] === "number" ? payload["clientTime"] : 0,
          simTime,
        });
        return;
      case "cmd.game.start":
        if (started) {
          sendAck(msg.cmdId, { ok: false, code: "phase_invalid", message: "already started" });
          return;
        }
        started = true;
        if (!finished) {
          tickTimer = timers.setInterval(tick, tickMs);
        }
        sendAck(msg.cmdId, { ok: true });
        return;
      case "cmd.game.pause":
        paused = payload["paused"] === true;
        sendAck(msg.cmdId, { ok: true });
        return;
      case "cmd.game.speed": {
        const next = payload["speed"];
        if (typeof next === "number" && allowedSpeeds.includes(next)) {
          speed = next;
          sendAck(msg.cmdId, { ok: true });
        } else {
          sendAck(msg.cmdId, { ok: false, code: "bad_request", message: "speed not allowed" });
        }
        return;
      }
      case "cmd.game.skip":
        skip();
        sendAck(msg.cmdId, { ok: true });
        return;
      default:
        if (POLICY_TYPES.has(msg.type)) {
          sendAck(msg.cmdId, ackPolicy(msg));
        }
        // sub.engineer・読めない・知らない type・internal.* は何もしない
        return;
    }
  }

  const socketFactory: SocketFactory = (url, handlers) => {
    const conn: MockConn = { handlers, open: false, closed: false };
    const socket: SocketLike = {
      send(data) {
        if (conn.closed || !conn.open) {
          return;
        }
        handleClientMessage(conn, data);
      },
      close(code, reason) {
        if (conn.closed) {
          return;
        }
        markClosed(conn);
        deliverClose(conn, code ?? 1005, reason ?? "");
      },
    };
    const valid = url.includes("/ws/sessions/") && url.includes("?token=");
    if (!valid || refuse > 0) {
      if (valid) {
        refuse--;
      }
      conn.closed = true;
      deliverClose(conn, 1006, "");
      return socket;
    }
    const previous = current;
    if (previous !== null && !previous.closed) {
      markClosed(previous);
      deliverClose(previous, 4001, "replaced");
    }
    current = conn;
    timers.setTimeout(() => {
      if (!conn.closed) {
        conn.open = true;
        conn.handlers.onOpen();
      }
    }, 0);
    return socket;
  };

  return {
    socketFactory,
    received: () => received,
    simTime: () => simTime,
    disconnect(code = 1006) {
      const conn = current;
      if (conn === null) {
        return;
      }
      markClosed(conn);
      deliverClose(conn, code, "");
    },
    dropNextServerMessage() {
      dropNext = true;
    },
    refuseConnections(count) {
      refuse = Math.max(0, Math.floor(count));
    },
  };
}
