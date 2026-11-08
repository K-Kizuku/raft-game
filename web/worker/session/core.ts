// GameSession の中身（T-308・DesignDoc §12.3・§12.5・ADR-0018・ADR-0023）。
//
//   POST /init     セッションの作成とコンテナの起動の開始（ルーターが reserve の ok の後でだけ呼ぶ。G1）
//   GET  /connect  トークンの検証と WebSocket の中継（ルーターが verifySessionId の後でだけ呼ぶ。G3）
//   alarm()        30 分の上限と切断から 5 分の打ち切り（G2。回数に上限）
//
// 状態: POST /init で starting → コンテナへの WebSocket を開いて internal.init（か internal.restore）を
// 送ったら running → 「終わり」で ended（戻らない）。ブラウザの接続 1 本に、コンテナへの WebSocket を 1 本張る。
//
// 入力ログと復元（T-308b）: ブラウザのコマンドを cmdId ごとに覚え、コンテナの ack が ok: true なら
// input_log に記録する。自動航行の船のボットの操作（T-724）は、コンテナの internal.input をそのまま記録する。コンテナが止まったと判断したら（monitor の終わり・ソケットの close・死活監視など）
// ブラウザを 1011 で閉じ、次の /connect（トークンの確認の後）で起動し直して internal.restore を送る。

import type { CostGuardClient } from "../admin/client.ts";
import { containerInstanceOf } from "../admin/ports.ts";
import { autoFerryEnabled, type Env } from "../routes/env.ts";
import { errorResponse, json, readJsonBody } from "../routes/http.ts";
import { SESSION_TOKEN_PATTERN, hashSessionToken } from "../security/token.ts";
import { desiredAlarmAt } from "./alarm.ts";
import {
  webSocketOf,
  type ContainerLike,
  type GameSessionStateLike,
  type WebSocketLike,
} from "./cf-types.ts";
import { containerStartTimeoutMsOf, readGameTimeLimits } from "./config.ts";
import {
  classifyBrowserText,
  classifyContainerText,
  exceedsBytes,
  utf8Bytes,
  type AckInfo,
  type BotInput,
  type ContainerMessage,
} from "./envelope.ts";
import {
  CLOCK_PERSIST_INTERVAL_MS,
  CONTAINER_PORT,
  DESTROY_RETRY_MS,
  IDLE_MS,
  IMAGE_NAME,
  MAX_BUFFERED_MESSAGES,
  MAX_CONTAINER_STARTS,
  MAX_INPUT_LOG_BYTES,
  MAX_INPUT_LOG_ROWS,
  MAX_MESSAGES_PER_SECOND,
  MAX_PENDING_COMMANDS,
  MAX_RESULT_BYTES,
  MAX_WS_MESSAGE_BYTES,
  PING_INTERVAL_MS,
  PING_MAX_MISSED,
  READY_POLL_INTERVAL_MS,
  READY_POLL_REQUEST_MS,
} from "./limits.ts";
import { PendingCommands } from "./pending.ts";
import {
  buildInputLogJson,
  buildRestoreMessage,
  commandJson,
  initPayload,
  isBotCommandType,
  recordDecision,
  restoreUntil,
} from "./restore.ts";
import {
  appendInputLog,
  ensureSessionSchema,
  inputLogStats,
  insertSession,
  readInputLog,
  readSession,
  updateSession,
  type SessionPatch,
  type SessionRow,
} from "./store.ts";

export interface GameSessionDeps {
  readonly now: () => number; // UNIX ミリ秒
  readonly sleep: (ms: number) => Promise<void>;
  readonly log: (line: string) => void;
  readonly createPair: () => readonly [client: WebSocketLike, server: WebSocketLike]; // 本番は WebSocketPair
  readonly upgradeResponse: (client: WebSocketLike) => Response; // 本番は new Response(null, { status: 101, webSocket })
  readonly timeoutSignal: (ms: number) => AbortSignal | undefined;
  readonly costGuard: CostGuardClient | undefined; // 本番は getCostGuardClient(env)
  // ms ごとに callback を呼ぶ。戻り値の関数で止める。本番は globalThis.setInterval・clearInterval
  readonly setInterval: (callback: () => void, ms: number) => () => void;
}

// コンテナが止まったと判断した理由（DesignDoc §12.3「入力ログと復元」）。
export type StopCause =
  "monitor" | "socket_closed" | "ping_timeout" | "start_timeout" | "not_running";

export type EndReason =
  | "round_ended"
  | "time_limit"
  | "idle"
  | "alarm_cap"
  | "start_timeout"
  | "container_lost"
  | "container_error";

const ROUTES: ReadonlyMap<string, "GET" | "POST"> = new Map([
  ["/init", "POST"],
  ["/connect", "GET"],
  ["/input-log", "GET"],
]);

const MAX_INIT_BODY_BYTES = 4096;
const INIT_KEYS: ReadonlySet<string> = new Set([
  "sessionId",
  "stageId",
  "seed",
  "demo",
  "tokenHash",
]);
const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;
const STAGE_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;
const TOKEN_HASH_PATTERN = /^[0-9a-f]{64}$/;
const MAX_RUN_SECONDS = 86400; // CostGuard の settle が受ける上限
const CONTAINER_ORIGIN = "http://container";
const PONG = JSON.stringify({ v: 1, type: "internal.pong", payload: {} });
const PING = JSON.stringify({ v: 1, type: "internal.ping", payload: {} });
// snapshot を受ける前のこれらの fatal の error は internal.init・internal.restore の誤り（つなぎ直しても同じ）。
const INIT_FAILURE_CODES: ReadonlySet<string> = new Set(["bad_request", "not_initialized"]);

interface InitBody {
  readonly sessionId: string;
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly tokenHash: string;
}

interface Dropped {
  binary: number;
  oversize: number;
  rate: number;
  malformed: number;
  internal: number;
  buffer: number;
  duplicate: number;
  pending_full: number;
  log_full: number;
}

// コンテナに送るブラウザの電文（受けた文字列と、読んだ値）。
interface Outgoing {
  readonly data: string;
  readonly type: string;
  readonly cmdId: unknown;
  readonly payload: unknown;
}

// ブラウザの接続 1 本（とコンテナへの WebSocket 1 本）。gen は接続ごとの世代の番号。
interface Connection {
  readonly gen: number;
  readonly browser: WebSocketLike;
  readonly connectedAt: number;
  container: WebSocketLike | null;
  readonly buffer: Outgoing[];
  lastSeq: number | null;
  closed: boolean;
  toContainer: number;
  toBrowser: number;
  readonly dropped: Dropped;
  gotSnapshot: boolean; // コンテナへのソケットから snapshot を受けた
  stopPing: (() => void) | null; // 死活監視を止める
  pingOutstanding: boolean; // 送った internal.ping に internal.pong がまだない
  pingMissed: number; // 続けて internal.pong がない回数
}

interface LogStats {
  rows: number;
  bytes: number;
  lastT: number | null;
}

// 終わりの理由ごとに、ブラウザに送るもの（DO の error の code）と閉じるコード。
const END_NOTICE: Readonly<
  Record<
    EndReason,
    { readonly code: string | null; readonly close: number; readonly reason: string }
  >
> = {
  round_ended: { code: null, close: 1000, reason: "round ended" },
  container_error: { code: null, close: 1011, reason: "container error" }, // コンテナの error を中継済み
  time_limit: { code: "session_ended", close: 1000, reason: "session ended" },
  idle: { code: "session_ended", close: 1000, reason: "session ended" },
  alarm_cap: { code: "session_ended", close: 1000, reason: "session ended" },
  start_timeout: { code: "container_unavailable", close: 1011, reason: "container unavailable" },
  container_lost: { code: "container_unavailable", close: 1011, reason: "container unavailable" },
};

const NOTICE_MESSAGES: Readonly<Record<string, string>> = {
  session_ended: "Session ended",
  container_unavailable: "Container unavailable",
};

function badRequest(): Response {
  return errorResponse(400, "bad_request", "Invalid request body");
}

function notFound(): Response {
  return errorResponse(404, "not_found", "Not found");
}

function parseInitBody(value: unknown): InitBody | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const body: Readonly<Record<string, unknown>> = { ...value };
  if (Object.keys(body).some((key) => !INIT_KEYS.has(key))) return null;
  const { sessionId, stageId, seed, demo, tokenHash } = body;
  if (typeof sessionId !== "string" || !SESSION_ID_PATTERN.test(sessionId)) return null;
  if (typeof stageId !== "string" || !STAGE_ID_PATTERN.test(stageId)) return null;
  if (typeof seed !== "number" || !Number.isSafeInteger(seed) || seed < 0) return null;
  if (typeof demo !== "boolean") return null;
  if (typeof tokenHash !== "string" || !TOKEN_HASH_PATTERN.test(tokenHash)) return null;
  return { sessionId, stageId, seed, demo, tokenHash };
}

function sameInit(row: SessionRow, body: InitBody): boolean {
  return (
    row.sessionId === body.sessionId &&
    row.stageId === body.stageId &&
    row.seed === body.seed &&
    row.demo === body.demo &&
    row.tokenHash === body.tokenHash
  );
}

// 64 文字どうしを 1 文字ずつ XOR して比べる（定数時間）。
function constantTimeEqual(a: string, b: string): boolean {
  const length = Math.max(a.length, b.length);
  let diff = a.length ^ b.length;
  for (let i = 0; i < length; i++) diff |= (a.charCodeAt(i) || 0) ^ (b.charCodeAt(i) || 0);
  return diff === 0;
}

function safeClose(socket: WebSocketLike, code: number, reason: string): void {
  try {
    socket.close(code, reason);
  } catch {
    // すでに閉じている
  }
}

function safeSend(socket: WebSocketLike, message: string): boolean {
  try {
    socket.send(message);
    return true;
  } catch {
    return false;
  }
}

function errorName(error: unknown): string {
  return error instanceof Error ? error.name : "unknown";
}

export class GameSessionCore {
  readonly #ctx: GameSessionStateLike;
  readonly #env: Env;
  readonly #deps: GameSessionDeps;
  readonly #pending = new Set<Promise<void>>();

  #conn: Connection | null = null;
  #gen = 0;
  #watching = false; // monitor() を見張っている（DO が作り直されると false に戻る）
  // 見張りの世代。#watch() のたびと、止まったと判断したときに 1 増やす。monitor() の終わり・ソケット・
  // ack が「いまのコンテナのものか」を見分ける。
  #watchGen = 0;
  #stopped = false; // 止まったと判断し、まだ起動し直していない
  #stopping: Promise<void> | null = null; // 動いている「止まったときの処理」（1 つずつ動かす）
  #rateWindow = -1; // Math.floor(now / 1000)。DO に 1 つで、接続をまたいで続く
  #rateCount = 0;
  #cleaning: Promise<void> | null = null; // 動いている後始末（同時に 2 つ動かさない）

  // 入力ログ（DO に 1 つ）
  readonly #awaitingAck = new PendingCommands();
  #logStats: LogStats | null = null; // 最初に要るときに SQLite から読む
  #logFullWarned = false;

  // 最後に中継した clock.t（メモリ）と、SQLite に書いた値・時刻
  #clockLoaded = false;
  #clockT: number | null = null;
  #clockSaved: number | null = null;
  #clockSavedAt: number | null = null;

  constructor(ctx: GameSessionStateLike, env: Env, deps: GameSessionDeps) {
    this.#ctx = ctx;
    this.#env = env;
    this.#deps = deps;
    ensureSessionSchema(ctx.storage.sql);
  }

  // ---- 入口 ----

  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);
    const method = ROUTES.get(pathname);
    if (method === undefined) return notFound();
    if (request.method !== method) {
      return errorResponse(405, "method_not_allowed", "Method not allowed", { allow: method });
    }
    switch (pathname) {
      case "/init":
        return this.#init(request);
      case "/input-log":
        return this.#inputLog(request);
      default:
        return this.#connect(request);
    }
  }

  async alarm(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    if (row.state === "ended") {
      // 停止を確かめられていないコンテナがあれば、後始末をやり直す（回数は max_alarms の中）。
      // なければ何もしない（アラームを設定しない）。
      if (row.containerStartedAt !== null) {
        this.#update({ alarmAt: null });
        await this.#cleanup();
      }
      return;
    }
    this.#update({ alarmAt: null });
    const now = this.#deps.now();
    if (now >= row.deadlineAt) {
      await this.#endSession("time_limit");
      return;
    }
    if (!this.#connected()) {
      let idleSince = row.idleSince;
      if (idleSince === null) {
        idleSince = now;
        this.#update({ idleSince });
      }
      if (now >= idleSince + IDLE_MS) {
        await this.#endSession("idle");
        return;
      }
    }
    if (!(await this.#scheduleAlarm()) && this.#row()?.state !== "ended") {
      // 次のアラームがない（setAlarm が失敗した）。例外にしてランタイムに alarm() をやり直させる
      // （やり直すたびに数えるので、max_alarms で alarm_cap になって終わる）。
      throw new Error("alarm not scheduled");
    }
  }

  // テスト用: 進行中の非同期の処理（コンテナへの接続・終わり）がすべて終わるまで待つ。
  async drain(): Promise<void> {
    for (;;) {
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      if (this.#pending.size === 0) return;
      await Promise.allSettled([...this.#pending]);
    }
  }

  // ---- 共通 ----

  #row(): SessionRow | null {
    return readSession(this.#ctx.storage.sql);
  }

  #update(patch: SessionPatch): void {
    updateSession(this.#ctx.storage.sql, patch);
  }

  #log(
    level: "info" | "warn" | "error",
    event: string,
    fields: Record<string, unknown> = {},
  ): void {
    const sessionId = this.#row()?.sessionId ?? null;
    this.#deps.log(JSON.stringify({ level, event, sessionId, ...fields }));
  }

  #logError(op: string, error: unknown): void {
    this.#log("error", "session.error", { op, error: errorName(error) });
  }

  #track(promise: Promise<void>): void {
    const tracked = promise.catch((error: unknown) => this.#logError("async", error));
    this.#pending.add(tracked);
    void tracked.finally(() => this.#pending.delete(tracked));
  }

  #connected(): boolean {
    return this.#conn !== null && !this.#conn.closed;
  }

  #isCurrent(conn: Connection): boolean {
    return this.#conn === conn && !conn.closed;
  }

  // ---- POST /init ----

  async #init(request: Request): Promise<Response> {
    const read = await readJsonBody(request, MAX_INIT_BODY_BYTES);
    const body = read.ok ? parseInitBody(read.value) : null;
    if (body === null) return badRequest();

    const existing = this.#row();
    if (existing !== null) return this.#repeatedInit(existing, body);

    const limits = await readGameTimeLimits(this.#deps.costGuard);
    const raced = this.#row(); // view() を待つ間に別の /init が来たとき
    if (raced !== null) return this.#repeatedInit(raced, body);

    const now = this.#deps.now();
    insertSession(this.#ctx.storage.sql, {
      ...body,
      createdAt: now,
      deadlineAt: now + limits.maxMinutes * 60000,
      maxAlarms: limits.maxAlarms,
      idleSince: now, // まだ誰もつながっていない
    });

    // コンテナを起動する前にアラームを設定する（起動したコンテナには必ずアラームがある）。
    // 設定できなければ起動しない（fail closed。500 を返し、ルーターが予約を cancel する）。
    const alarmSet = await this.#scheduleAlarm();

    const instance = containerInstanceOf(this.#env);
    const container = this.#ctx.container;
    const started = alarmSet && this.#startContainer(container);
    if (container === undefined || !started) {
      // settle は呼ばない（ルーターが init の失敗で cancel を呼ぶ）。
      this.#update({ state: "ended", endReason: "start_failed", endedAt: now, alarmAt: null });
      try {
        await this.#ctx.storage.deleteAlarm();
      } catch (error) {
        this.#logError("deleteAlarm", error);
      }
      this.#log("error", "session.end", { reason: "start_failed" });
      return errorResponse(500, "start_failed", "Container start failed");
    }

    this.#update({ containerStarts: 1, containerStartedAt: now });
    this.#log("info", "container.start", { instance, containerStarts: 1 });
    this.#watch(container);
    this.#log("info", "session.init", {
      stageId: body.stageId,
      demo: body.demo,
      instance,
      maxMinutes: limits.maxMinutes,
      maxAlarms: limits.maxAlarms,
    });
    // /healthz を待たない（プロトコル §2「コンテナの起動を始めてから応答する」）。
    return json({}, 201);
  }

  #repeatedInit(row: SessionRow, body: InitBody): Response {
    return sameInit(row, body) ? json({}, 200) : errorResponse(409, "conflict", "Session exists");
  }

  // コンテナを起動する（POST /init と起動し直しで共通。引数は同じ）。起動できなければ false。
  #startContainer(container: ContainerLike | undefined): boolean {
    const image = container?.images[IMAGE_NAME];
    if (container === undefined || image === undefined) return false;
    try {
      container.start({
        image,
        instance: containerInstanceOf(this.#env),
        enableInternet: false,
        env: { MODE: "single", PORT: String(CONTAINER_PORT) },
      });
      return true;
    } catch (error) {
      this.#logError("start", error);
      return false;
    }
  }

  // ---- コンテナの見張り ----

  #watch(container: ContainerLike): void {
    this.#watchGen += 1;
    const watchGen = this.#watchGen;
    this.#watching = true;
    this.#stopped = false;
    let exit: Promise<void>;
    try {
      exit = container.monitor();
    } catch (error) {
      exit = Promise.reject(error instanceof Error ? error : new Error("monitor failed"));
    }
    void exit
      .then(
        () => undefined,
        () => undefined,
      )
      .then(() => this.#onMonitorEnded(watchGen));
  }

  #onMonitorEnded(watchGen: number): void {
    // 自分で destroy() した古いコンテナの終わり・終わった後の終わりは無視する。
    if (watchGen !== this.#watchGen) return;
    const row = this.#row();
    if (row === null || row.state === "ended") return;
    if (this.#ctx.container?.running === true) {
      // monitor() だけで判断しない（DO が作り直されたときの振る舞いは未確認。ADR-0016）。
      // 次の /connect で見張り直す。止まっていればソケットの close か死活監視で分かる。
      this.#watching = false;
      this.#log("warn", "container.monitor_ended");
      return;
    }
    this.#track(this.#onContainerStopped("monitor", watchGen));
  }

  // ---- コンテナが止まったとき（何度呼ばれても 1 つずつ動く） ----

  #onContainerStopped(cause: StopCause, watchGen: number): Promise<void> {
    const previous = this.#stopping ?? Promise.resolve();
    const next = previous
      .then(() => this.#handleStopped(cause, watchGen))
      .catch((error: unknown) => this.#logError("stopped", error));
    this.#stopping = next;
    void next.finally(() => {
      if (this.#stopping === next) this.#stopping = null;
    });
    return next;
  }

  async #handleStopped(cause: StopCause, watchGen: number): Promise<void> {
    if (watchGen !== this.#watchGen) return;
    const row = this.#row();
    if (row === null || row.state === "ended") return;
    this.#persistClock();

    // 試し直さない
    if (this.#deps.now() >= row.deadlineAt) {
      await this.#endSession("time_limit");
      return;
    }
    if (row.containerStarts >= MAX_CONTAINER_STARTS) {
      await this.#endSession("container_lost");
      return;
    }
    if (row.stageHash === null && this.#stats().rows > 0) {
      // internal.init で始め直すと、記録したコマンドが消える
      await this.#endSession("container_lost");
      return;
    }

    // ブラウザを閉じる（error は送らない。ブラウザはつなぎ直す）。not_running は /connect の中なので閉じない。
    const conn = this.#conn;
    if (cause !== "not_running" && conn !== null && !conn.closed) {
      this.#detach(conn, { browser: [1011, "container restarting"] });
    }

    const container = this.#ctx.container;
    if (container?.running === true) {
      try {
        await container.destroy();
      } catch (error) {
        this.#logError("destroy", error);
        // container_started_at を残すので、後始末のやり直しが働く
        await this.#endSession("container_lost");
        return;
      }
    }

    const current = this.#row();
    if (current === null || current.state === "ended" || watchGen !== this.#watchGen) return;
    const patch: { runMs?: number; containerStartedAt: null; readyAt: null } = {
      containerStartedAt: null,
      readyAt: null,
    };
    if (current.containerStartedAt !== null) {
      patch.runMs = current.runMs + Math.max(0, this.#deps.now() - current.containerStartedAt);
    }
    this.#update(patch);
    this.#watchGen += 1;
    this.#watching = false;
    this.#stopped = true;
    this.#awaitingAck.clear(); // 止まったコンテナからは ack が来ない
    this.#log("info", "container.stopped", { cause, containerStarts: current.containerStarts });
  }

  // 止まっているコンテナを起動し直す（/connect の後の #attachContainer の中だけ）。予約はしない。
  async #restartContainer(): Promise<boolean> {
    const row = this.#row();
    if (row === null || row.state === "ended") return false;
    if (this.#deps.now() >= row.deadlineAt) {
      await this.#endSession("time_limit");
      return false;
    }
    if (row.containerStarts >= MAX_CONTAINER_STARTS) {
      await this.#endSession("container_lost");
      return false;
    }
    const container = this.#ctx.container;
    if (container === undefined || !this.#startContainer(container)) {
      await this.#endSession("container_lost");
      return false;
    }
    const containerStarts = row.containerStarts + 1;
    this.#update({ containerStarts, containerStartedAt: this.#deps.now(), readyAt: null });
    this.#log("info", "container.start", {
      instance: containerInstanceOf(this.#env),
      containerStarts,
      restart: true,
    });
    this.#watch(container);
    return true;
  }

  // ---- GET /connect ----

  async #connect(request: Request): Promise<Response> {
    if ((request.headers.get("upgrade") ?? "").toLowerCase() !== "websocket") {
      return errorResponse(426, "upgrade_required", "WebSocket upgrade required");
    }
    // 行がない・トークンがない・形が違う・違う・終わった、を区別しない（ソケットを作らず、コンテナに触れない）。
    const row = this.#row();
    if (row === null) return notFound();
    const token = request.headers.get("x-session-token");
    if (token === null || !SESSION_TOKEN_PATTERN.test(token)) return notFound();
    const hash = await hashSessionToken(token);
    if (!constantTimeEqual(hash, row.tokenHash)) return notFound();
    const state = this.#row()?.state; // ハッシュを待つ間に終わったときのため、読み直す
    if (state === undefined || state === "ended") return notFound();

    const old = this.#conn;
    if (old !== null && !old.closed) {
      old.closed = true;
      this.#stopPing(old);
      safeClose(old.browser, 4001, "replaced");
      if (old.container !== null) safeClose(old.container, 1000, "replaced");
      this.#log("info", "session.replaced", this.#connectionStats(old));
    }

    const [client, server] = this.#deps.createPair();
    server.accept();
    this.#gen += 1;
    const conn: Connection = {
      gen: this.#gen,
      browser: server,
      connectedAt: this.#deps.now(),
      container: null,
      buffer: [],
      lastSeq: null,
      closed: false,
      toContainer: 0,
      toBrowser: 0,
      dropped: {
        binary: 0,
        oversize: 0,
        rate: 0,
        malformed: 0,
        internal: 0,
        buffer: 0,
        duplicate: 0,
        pending_full: 0,
        log_full: 0,
      },
      gotSnapshot: false,
      stopPing: null,
      pingOutstanding: false,
      pingMissed: 0,
    };
    this.#conn = conn;
    server.addEventListener("message", (event) => this.#onBrowserMessage(conn, event.data));
    server.addEventListener("close", () => this.#onBrowserClosed(conn));
    server.addEventListener("error", () => this.#onBrowserClosed(conn));

    this.#update({ idleSince: null });
    await this.#scheduleAlarm();
    this.#log("info", "session.connect", { gen: conn.gen });

    if (this.#isCurrent(conn)) this.#track(this.#attachContainer(conn));
    return this.#deps.upgradeResponse(client);
  }

  // ---- GET /input-log（T-714・FR-LOG-03） ----

  // 入力ログ（形式 1）を添付ファイルの JSON で返す。トークンの確かめ方は #connect と同じ（行がない・
  // トークンがない・形が違う・違う、を区別しない）。終わったセッションでも返す（ラウンドの後に取り出すため）。
  // コンテナに触れない・アラームを設定しない・行を書き換えない。
  async #inputLog(request: Request): Promise<Response> {
    const row = this.#row();
    if (row === null) return notFound();
    const token = request.headers.get("x-session-token");
    if (token === null || !SESSION_TOKEN_PATTERN.test(token)) return notFound();
    const hash = await hashSessionToken(token);
    if (!constantTimeEqual(hash, row.tokenHash)) return notFound();

    const sql = this.#ctx.storage.sql;
    const body = buildInputLogJson(row, readInputLog(sql));
    const stats = inputLogStats(sql);
    // トークン・本文は出さない（1 回の要求に 1 行）。
    this.#log("info", "session.input_log", { rows: stats.rows, bytes: stats.bytes });
    return new Response(body, {
      status: 200,
      headers: {
        "content-type": "application/json; charset=utf-8",
        "cache-control": "no-store",
        // stageId は STAGE_ID_PATTERN に合う値だけが入っている。seed は 10 進の整数。
        "content-disposition": `attachment; filename="raft-game-${row.stageId}-${String(row.seed)}.json"`,
      },
    });
  }

  #connectionStats(conn: Connection): Record<string, unknown> {
    return {
      gen: conn.gen,
      connectedMs: this.#deps.now() - conn.connectedAt,
      toContainer: conn.toContainer,
      toBrowser: conn.toBrowser,
      dropped: { ...conn.dropped },
    };
  }

  // ---- コンテナへの接続（/connect の後、非同期） ----

  #attachable(conn: Connection): boolean {
    const row = this.#row();
    return row !== null && row.state !== "ended" && this.#isCurrent(conn);
  }

  async #attachContainer(conn: Connection): Promise<void> {
    // 1. 止まったときの処理が動いていれば、終わるのを待つ（start() を 2 回呼ばない）
    while (this.#stopping !== null) await this.#stopping;
    if (!this.#attachable(conn)) return;

    // 2. 見張りがない（DO が作り直された・monitor() が running のまま終わった）
    const container = this.#ctx.container;
    if (container === undefined) {
      await this.#endSession("container_lost");
      return;
    }
    if (!this.#watching && !this.#stopped) {
      if (container.running) {
        this.#watch(container);
      } else {
        await this.#onContainerStopped("not_running", this.#watchGen);
        while (this.#stopping !== null) await this.#stopping;
        if (!this.#attachable(conn)) return;
      }
    }

    // 3. 止まっているなら起動し直す
    if (this.#stopped && !(await this.#restartContainer())) return;
    const watchGen = this.#watchGen;

    const row = this.#row();
    if (row === null || row.state === "ended" || !this.#isCurrent(conn)) return;

    if (row.readyAt === null) {
      const ready = await this.#waitReady(container, row, conn);
      if (ready === "stale") return;
      if (ready === "timeout") {
        if (this.#row()?.state === "starting") {
          await this.#endSession("start_timeout");
        } else {
          await this.#onContainerStopped("start_timeout", watchGen);
        }
        return;
      }
    }

    let socket: WebSocketLike | null = null;
    try {
      const response = await container
        .getTcpPort(CONTAINER_PORT)
        .fetch(`${CONTAINER_ORIGIN}/ws`, { headers: { Upgrade: "websocket" } });
      socket = webSocketOf(response);
    } catch (error) {
      this.#logError("containerConnect", error);
    }

    const current = this.#row();
    if (!this.#isCurrent(conn) || current === null || current.state === "ended") {
      if (socket !== null) safeClose(socket, 1000, "browser closed");
      return;
    }
    if (socket === null) {
      // 終わりにしない（ブラウザはつなぎ直す。打ち切りはアラームが行う）。
      this.#detach(conn, { browser: [1011, "container connect failed"] });
      return;
    }

    socket.accept();
    conn.container = socket;
    socket.addEventListener("message", (event) =>
      this.#onContainerMessage(conn, watchGen, event.data),
    );
    socket.addEventListener("close", () => this.#onContainerClosed(conn, watchGen));
    socket.addEventListener("error", () => this.#onContainerClosed(conn, watchGen));

    // stage_hash があれば internal.restore、なければ internal.init。生きているコンテナは、同じ sessionId の
    // internal.init・internal.restore をつなぎ直しとして扱う（T-302）ので、起動し直したかどうかによらない。
    safeSend(socket, this.#firstMessage(current));
    if (current.state === "starting") this.#update({ state: "running" });
    conn.stopPing = this.#deps.setInterval(
      () => this.#onPingTick(conn, watchGen),
      PING_INTERVAL_MS,
    );

    for (const item of conn.buffer.splice(0)) this.#sendToContainer(conn, socket, item);
  }

  // AUTO_FERRY（T-724）は autoFerry で渡す（"1" のときだけキーを入れる）。cmd.ferry.auto を断る ack
  // （feature_disabled）はコンテナが返す（DO は seq を持つ ack を作れないため）。
  #firstMessage(row: SessionRow): string {
    const autoFerry = autoFerryEnabled(this.#env);
    if (row.stageHash === null) {
      return JSON.stringify({ v: 1, type: "internal.init", payload: initPayload(row, autoFerry) });
    }
    const commands = readInputLog(this.#ctx.storage.sql);
    const until = restoreUntil(this.#clock(), commands.at(-1)?.t ?? null);
    const message = buildRestoreMessage(row, commands, until, autoFerry);
    this.#log("info", "session.restore", {
      commands: commands.length,
      until,
      bytes: utf8Bytes(message),
    });
    return message;
  }

  // ---- 死活監視（internal.ping） ----

  #onPingTick(conn: Connection, watchGen: number): void {
    const socket = conn.container;
    if (!this.#isCurrent(conn) || socket === null || this.#row()?.state !== "running") {
      this.#stopPing(conn);
      return;
    }
    if (conn.pingOutstanding) {
      conn.pingMissed += 1;
      if (conn.pingMissed >= PING_MAX_MISSED) {
        this.#stopPing(conn);
        this.#track(this.#onContainerStopped("ping_timeout", watchGen));
        return;
      }
    }
    conn.pingOutstanding = true;
    safeSend(socket, PING);
  }

  #stopPing(conn: Connection): void {
    const stop = conn.stopPing;
    conn.stopPing = null;
    if (stop !== null) stop();
  }

  async #waitReady(
    container: ContainerLike,
    row: SessionRow,
    conn: Connection,
  ): Promise<"ready" | "timeout" | "stale"> {
    const deadline =
      (row.containerStartedAt ?? this.#deps.now()) + containerStartTimeoutMsOf(this.#env);
    const port = container.getTcpPort(CONTAINER_PORT);
    for (;;) {
      const left = deadline - this.#deps.now();
      const signal = this.#deps.timeoutSignal(Math.max(1, Math.min(left, READY_POLL_REQUEST_MS)));
      let ok = false;
      try {
        const response = await port.fetch(
          `${CONTAINER_ORIGIN}/healthz`,
          signal === undefined ? {} : { signal },
        );
        ok = response.status === 200;
        await response.body?.cancel();
      } catch {
        // 失敗として続ける
      }
      if (!this.#isCurrent(conn) || this.#row()?.state === "ended") return "stale";
      if (ok) {
        const now = this.#deps.now();
        this.#update({ readyAt: now });
        const startedAt = this.#row()?.containerStartedAt ?? null;
        this.#log("info", "container.ready", {
          startMs: startedAt === null ? null : now - startedAt,
        });
        return "ready";
      }
      if (this.#deps.now() >= deadline) return "timeout";
      await this.#deps.sleep(READY_POLL_INTERVAL_MS);
      if (!this.#isCurrent(conn)) return "stale";
    }
  }

  // ---- ブラウザからの電文 ----

  #onBrowserMessage(conn: Connection, data: unknown): void {
    if (!this.#isCurrent(conn)) return;
    if (typeof data !== "string") {
      conn.dropped.binary += 1;
      this.#detach(conn, { browser: [1003, "binary not supported"] });
      return;
    }
    if (exceedsBytes(data, MAX_WS_MESSAGE_BYTES)) {
      conn.dropped.oversize += 1;
      this.#detach(conn, { browser: [1009, "message too big"] });
      return;
    }
    // 1 秒の枠ごとに数え、31 通目からはその枠の終わりまで捨てる（返事はしない）。
    const window = Math.floor(this.#deps.now() / 1000);
    if (window !== this.#rateWindow) {
      this.#rateWindow = window;
      this.#rateCount = 0;
    }
    this.#rateCount += 1;
    if (this.#rateCount > MAX_MESSAGES_PER_SECOND) {
      conn.dropped.rate += 1;
      return;
    }
    const verdict = classifyBrowserText(data);
    if (verdict.kind !== "forward") {
      conn.dropped[verdict.kind] += 1;
      return;
    }
    const item: Outgoing = {
      data,
      type: verdict.type,
      cmdId: verdict.cmdId,
      payload: verdict.payload,
    };
    if (conn.container === null) {
      if (conn.buffer.length >= MAX_BUFFERED_MESSAGES) {
        conn.dropped.buffer += 1;
        return;
      }
      conn.buffer.push(item);
      return;
    }
    this.#sendToContainer(conn, conn.container, item);
  }

  // コンテナへ送る直前に、記録する種類か・送ってよいかを決める（DesignDoc §12.3「入力ログと復元」）。
  // 受けた文字列をそのまま送る（中身を書き換えない）。
  #sendToContainer(conn: Connection, socket: WebSocketLike, item: Outgoing): void {
    const decision = recordDecision(item.type, item.cmdId);
    if (decision === "forward") {
      if (safeSend(socket, item.data)) conn.toContainer += 1;
      return;
    }
    const cmdId = item.cmdId;
    if (decision === "drop" || typeof cmdId !== "string") {
      conn.dropped.malformed += 1; // 覚えられない cmdId（Go は適用するので送らない）
      return;
    }
    if (this.#awaitingAck.has(cmdId)) {
      conn.dropped.duplicate += 1;
      return;
    }
    const now = this.#deps.now();
    this.#awaitingAck.purge(now);
    if (this.#awaitingAck.size >= MAX_PENDING_COMMANDS) {
      conn.dropped.pending_full += 1;
      return;
    }
    const cmd = commandJson(item.type, item.payload);
    const bytes = utf8Bytes(cmd);
    const stats = this.#stats();
    if (
      stats.rows + this.#awaitingAck.size >= MAX_INPUT_LOG_ROWS ||
      stats.bytes + this.#awaitingAck.bytes + bytes > MAX_INPUT_LOG_BYTES
    ) {
      // 返事はしない（記録とコンテナの状態を食い違わせないため、コンテナに送らない）
      conn.dropped.log_full += 1;
      if (!this.#logFullWarned) {
        this.#logFullWarned = true;
        this.#log("warn", "input_log.full", { rows: stats.rows, bytes: stats.bytes });
      }
      return;
    }
    if (safeSend(socket, item.data)) {
      conn.toContainer += 1;
      this.#awaitingAck.add(cmdId, { cmd, bytes }, now);
    }
  }

  #onBrowserClosed(conn: Connection): void {
    if (!this.#isCurrent(conn)) return;
    this.#detach(conn, {});
  }

  // ---- 入力ログ ----

  #stats(): LogStats {
    if (this.#logStats === null) {
      const stats = inputLogStats(this.#ctx.storage.sql);
      this.#logStats = { rows: stats.rows, bytes: stats.bytes, lastT: stats.lastT };
    }
    return this.#logStats;
  }

  #onAck(ack: AckInfo): void {
    const entry = this.#awaitingAck.take(ack.cmdId);
    if (entry === undefined || !ack.ok) return;
    this.#append(ack.t, entry.cmd, entry.bytes);
  }

  // internal.input（自動航行の船のボットが適用したコマンド。T-724）を記録する。ボットの操作はコンテナで適用済みなので、
  // ブラウザのコマンドと違って送る前に止められない。形が違う・ボットが出さない種類・上限を超えるものは記録せず warn。
  #onBotInput(input: BotInput | null): void {
    if (input === null || !isBotCommandType(input.type)) {
      this.#log("warn", "session.error", { op: "input_log_bot", error: "malformed" });
      return;
    }
    const cmd = commandJson(input.type, input.payload);
    const bytes = utf8Bytes(cmd);
    const stats = this.#stats();
    if (
      stats.rows + this.#awaitingAck.size >= MAX_INPUT_LOG_ROWS ||
      stats.bytes + this.#awaitingAck.bytes + bytes > MAX_INPUT_LOG_BYTES
    ) {
      if (!this.#logFullWarned) {
        this.#logFullWarned = true;
        this.#log("warn", "input_log.full", { rows: stats.rows, bytes: stats.bytes });
      }
      return;
    }
    this.#append(input.t, cmd, bytes);
  }

  #append(t: number, cmd: string, bytes: number): void {
    const stats = this.#stats();
    if (stats.lastT !== null && t < stats.lastT) {
      // Go は Now() を返すので起きない。sim.Replay は時刻の逆転を誤りにするので記録しない。
      this.#log("warn", "session.error", { op: "input_log_order" });
      return;
    }
    appendInputLog(this.#ctx.storage.sql, t, cmd);
    stats.rows += 1;
    stats.bytes += bytes;
    stats.lastT = t;
  }

  // ---- 最後の clock.t ----

  // メモリの値。DO が作り直されたら last_clock_t から始める。
  #clock(): number | null {
    if (!this.#clockLoaded) {
      this.#clockLoaded = true;
      this.#clockT = this.#row()?.lastClockT ?? null;
      this.#clockSaved = this.#clockT;
    }
    return this.#clockT;
  }

  #onClock(t: number): void {
    this.#clock();
    this.#clockT = t; // 小さくなっても上書きする
    const savedAt = this.#clockSavedAt;
    if (savedAt === null || this.#deps.now() - savedAt >= CLOCK_PERSIST_INTERVAL_MS) {
      this.#persistClock();
    }
  }

  // メモリの値が SQLite と違えば書く。
  #persistClock(): void {
    const t = this.#clock();
    if (t === this.#clockSaved || t === null) return;
    this.#update({ lastClockT: t });
    this.#clockSaved = t;
    this.#clockSavedAt = this.#deps.now();
  }

  // ---- コンテナからの電文 ----

  #onContainerMessage(conn: Connection, watchGen: number, data: unknown): void {
    if (typeof data !== "string") return;
    const message = classifyContainerText(data);
    if (message.kind === "result") {
      // internal.result は、ブラウザが閉じた・置き換えた接続のソケットから届いても、同じコンテナの
      // 結果なので保存し、セッションを終える（ブラウザは round.result を受けるとすぐ閉じるため、
      // 閉じた後に届きうる）。終わりの処理は 1 回だけ動き、2 回目以降は保存だけになる。
      this.#saveResult(message.payload);
      this.#track(this.#endSession("round_ended"));
      return;
    }
    // ack・internal.input は、置き換えた・ブラウザが閉じた接続のソケットから届いても、いまのコンテナのものなら
    // 記録に使う（Go は「いまの接続」に送るので、どちらのソケットにも届きうる）。
    if (message.kind === "forward" && message.ack !== null && watchGen === this.#watchGen) {
      this.#onAck(message.ack);
    }
    if (message.kind === "input") {
      if (watchGen === this.#watchGen) this.#onBotInput(message.input);
      return; // ブラウザに送らない（internal.*）
    }
    // ほかの電文は、置き換えた・ブラウザが閉じた接続のソケットから届いたら、すべて捨てる。
    if (!this.#isCurrent(conn)) return;
    if (message.seq !== null) conn.lastSeq = message.seq;
    this.#handleContainerMessage(conn, data, message);
  }

  #handleContainerMessage(conn: Connection, data: string, message: ContainerMessage): void {
    switch (message.kind) {
      case "result":
      case "input":
        return; // #onContainerMessage で扱う
      case "ping":
        if (conn.container !== null) safeSend(conn.container, PONG);
        return;
      case "pong":
        conn.pingOutstanding = false;
        conn.pingMissed = 0;
        return;
      case "internal":
        return;
      case "forward": {
        this.#saveSessionInfo(message.stageHash, message.build);
        if (message.clockT !== null) this.#onClock(message.clockT);
        const initFailure =
          !conn.gotSnapshot &&
          message.fatalCode !== null &&
          INIT_FAILURE_CODES.has(message.fatalCode);
        if (message.type === "snapshot") conn.gotSnapshot = true;
        if (safeSend(conn.browser, data)) conn.toBrowser += 1;
        if (message.endsSession || initFailure) this.#track(this.#endSession("container_error"));
        return;
      }
    }
  }

  // snapshot の stageHash・build が保存してある値と違えば上書きする（読めない値では消さない）。
  #saveSessionInfo(stageHash: string | null, build: string | null): void {
    if (stageHash === null && build === null) return;
    const row = this.#row();
    if (row === null) return;
    const patch: { stageHash?: string; build?: string } = {};
    if (stageHash !== null && stageHash !== row.stageHash) patch.stageHash = stageHash;
    if (build !== null && build !== row.build) patch.build = build;
    this.#update(patch);
  }

  #saveResult(payload: unknown): void {
    const text = JSON.stringify(payload) ?? "null";
    if (exceedsBytes(text, MAX_RESULT_BYTES)) {
      this.#log("warn", "session.error", { op: "result", error: "too_large" });
      return;
    }
    this.#update({ resultJson: text, resultAt: this.#deps.now() });
  }

  #onContainerClosed(conn: Connection, watchGen: number): void {
    if (!this.#isCurrent(conn)) return;
    if (this.#row()?.state === "ended") return;
    if (this.#ctx.container?.running !== true) {
      // コンテナが止まった。起動し直す準備をする（ブラウザは 1011 で閉じられ、つなぎ直す）。
      this.#track(this.#onContainerStopped("socket_closed", watchGen));
      return;
    }
    // 終わりにしない（ブラウザはつなぎ直す）。
    this.#detach(conn, { browser: [1011, "container closed"], containerAlreadyClosed: true });
  }

  // ---- 接続を閉じる（終わり以外） ----

  #detach(
    conn: Connection,
    how: {
      readonly browser?: readonly [code: number, reason: string];
      readonly containerAlreadyClosed?: boolean;
    },
  ): void {
    if (conn.closed) return;
    conn.closed = true;
    this.#stopPing(conn);
    if (how.browser !== undefined) safeClose(conn.browser, how.browser[0], how.browser[1]);
    // コンテナはこれで自動の一時停止をする（T-303 の onDetach）。
    if (conn.container !== null && how.containerAlreadyClosed !== true) {
      safeClose(conn.container, 1000, "browser closed");
    }
    this.#log("info", "session.disconnect", this.#connectionStats(conn));
    if (this.#conn !== conn) return;
    this.#conn = null;
    const row = this.#row();
    if (row === null || row.state === "ended") return;
    this.#persistClock();
    this.#update({ idleSince: this.#deps.now() });
    this.#track(this.#scheduleAlarm().then(() => undefined));
  }

  // ---- アラーム ----

  // 早いほうにだけ動かす（遅くしたいときはそのまま鳴らし、鳴ったときに決め直す）。
  // 戻り値: アラームが設定されている（今回設定した・前のものが残っている）なら true。
  // 終わった・終わりになった（alarm_cap）・setAlarm が失敗して前のアラームもないなら false。
  async #scheduleAlarm(): Promise<boolean> {
    const row = this.#row();
    if (row === null || row.state === "ended") return false;
    const at = desiredAlarmAt(row, this.#connected(), this.#deps.now());
    if (row.alarmAt !== null && at >= row.alarmAt) return true;
    if (row.alarmCount >= row.maxAlarms) {
      await this.#endSession("alarm_cap");
      return false;
    }
    return this.#setAlarmAt(row, at);
  }

  // 数を先に保存する（setAlarm が失敗しても数は戻さない。安全側）。失敗したら alarm_at を前の値に戻し、
  // 設定できていないアラームを「ある」と記録しない（前のアラームは置き換わらずに残る）。
  async #setAlarmAt(row: SessionRow, at: number): Promise<boolean> {
    this.#update({ alarmCount: row.alarmCount + 1, alarmAt: at });
    try {
      await this.#ctx.storage.setAlarm(at);
      return true;
    } catch (error) {
      this.#logError("setAlarm", error);
      if (this.#row()?.alarmAt === at) this.#update({ alarmAt: row.alarmAt });
      return row.alarmAt !== null;
    }
  }

  // ---- 終わり（何度呼んでも 1 回だけ動く） ----

  async #endSession(reason: EndReason): Promise<void> {
    const row = this.#row();
    if (row === null || row.state === "ended") return;
    const endedAt = this.#deps.now();
    // 最初に保存する（以後の /connect は 404、alarm() は何もしない）。
    this.#persistClock();
    this.#update({ state: "ended", endReason: reason, endedAt });

    const conn = this.#conn;
    if (conn !== null) this.#stopPing(conn);
    if (conn !== null && !conn.closed) {
      conn.closed = true;
      const notice = END_NOTICE[reason];
      if (notice.code !== null) {
        safeSend(
          conn.browser,
          JSON.stringify({
            v: 1,
            type: "error",
            seq: (conn.lastSeq ?? 0) + 1,
            payload: {
              code: notice.code,
              message: NOTICE_MESSAGES[notice.code] ?? "Session ended",
              fatal: true,
            },
          }),
        );
      }
      safeClose(conn.browser, notice.close, notice.reason);
      if (conn.container !== null) safeClose(conn.container, 1000, "session ended");
      this.#log("info", "session.disconnect", this.#connectionStats(conn));
    }

    await this.#cleanup();
  }

  // ---- 後始末（コンテナの停止と精算） ----
  //
  // destroy() が成功したら（停止を確かめたら）精算してアラームを消す。失敗したら精算せず
  // （container_started_at を残し、帳簿は予約のまま＝最悪の費用）、アラームで後始末をやり直す。
  // やり直しのアラームも max_alarms の中に数える。上限に達したら、ログを 1 行出してあきらめる。
  // 同時に 2 つ動かない（終わりの処理の途中で鳴ったアラームは、動いている後始末を待つ）。
  #cleanup(): Promise<void> {
    if (this.#cleaning === null) {
      const running = this.#runCleanup().finally(() => {
        this.#cleaning = null;
      });
      this.#cleaning = running;
    }
    return this.#cleaning;
  }

  async #runCleanup(): Promise<void> {
    const row = this.#row();
    if (row === null || row.state !== "ended") return;
    const container = this.#ctx.container;
    if (row.containerStartedAt !== null && container !== undefined) {
      try {
        await container.destroy();
      } catch (error) {
        this.#logError("destroy", error);
        await this.#scheduleCleanupRetry();
        return;
      }
    }
    await this.#settleAndClear();
  }

  async #scheduleCleanupRetry(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    const retryAt = this.#deps.now() + DESTROY_RETRY_MS;
    // すでに設定してあるアラームが鳴れば、そこでやり直す（数を増やさない）。
    if (row.alarmAt !== null && (row.alarmAt <= retryAt || row.alarmCount >= row.maxAlarms)) {
      return;
    }
    if (row.alarmCount < row.maxAlarms && (await this.#setAlarmAt(row, retryAt))) return;
    // やり直す手段がない。帳簿は予約のまま（最悪の費用で数えられる）。
    this.#log("error", "session.error", { op: "destroy", error: "retry_exhausted" });
  }

  // 停止を確かめた後でだけ呼ぶ。精算はやり直さない（失敗したら帳簿は予約のまま。安全側）。
  async #settleAndClear(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    let runMs = row.runMs;
    if (row.containerStartedAt !== null) {
      runMs += Math.max(0, this.#deps.now() - row.containerStartedAt);
      this.#update({ runMs, containerStartedAt: null });
    }

    const runSeconds = Math.min(Math.ceil(runMs / 1000), MAX_RUN_SECONDS);
    let settled = row.settled;
    const costGuard = this.#deps.costGuard;
    if (settled) {
      // すでに精算した
    } else if (costGuard === undefined) {
      this.#log("error", "session.error", { op: "settle", error: "no_cost_guard" });
    } else {
      try {
        await costGuard.settle(row.sessionId, runSeconds);
        settled = true;
        this.#update({ settled: true });
      } catch (error) {
        this.#logError("settle", error);
      }
    }

    try {
      await this.#ctx.storage.deleteAlarm();
    } catch (error) {
      this.#logError("deleteAlarm", error);
    }
    this.#update({ alarmAt: null });

    const final = this.#row();
    this.#log("info", "session.end", {
      reason: final?.endReason ?? row.endReason,
      runSeconds,
      settled,
      alarmCount: final?.alarmCount ?? row.alarmCount,
      containerStarts: final?.containerStarts ?? row.containerStarts,
    });
  }
}
