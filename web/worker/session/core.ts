// GameSession の中身（T-308・DesignDoc §12.3・§12.5・ADR-0018・ADR-0023）。
//
//   POST /init     セッションの作成とコンテナの起動の開始（ルーターが reserve の ok の後でだけ呼ぶ。G1）
//   GET  /connect  トークンの検証と WebSocket の中継（ルーターが verifySessionId の後でだけ呼ぶ。G3）
//   alarm()        30 分の上限と切断から 5 分の打ち切り（G2。回数に上限）
//
// 状態: POST /init で starting → コンテナへの WebSocket を開いて internal.init を送ったら running →
// 「終わり」で ended（戻らない）。ブラウザの接続 1 本に、コンテナへの WebSocket を 1 本張る。
// 入力ログの記録と、コンテナが止まったときの復元は T-308b。

import type { CostGuardClient } from "../admin/client.ts";
import { containerInstanceOf } from "../admin/ports.ts";
import type { Env } from "../routes/env.ts";
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
  type ContainerMessage,
} from "./envelope.ts";
import {
  CONTAINER_PORT,
  DESTROY_RETRY_MS,
  IDLE_MS,
  IMAGE_NAME,
  MAX_BUFFERED_MESSAGES,
  MAX_MESSAGES_PER_SECOND,
  MAX_RESULT_BYTES,
  MAX_WS_MESSAGE_BYTES,
  READY_POLL_INTERVAL_MS,
  READY_POLL_REQUEST_MS,
} from "./limits.ts";
import {
  ensureSessionSchema,
  insertSession,
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
}

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
}

// ブラウザの接続 1 本（とコンテナへの WebSocket 1 本）。gen は接続ごとの世代の番号。
interface Connection {
  readonly gen: number;
  readonly browser: WebSocketLike;
  readonly connectedAt: number;
  container: WebSocketLike | null;
  readonly buffer: string[];
  lastSeq: number | null;
  closed: boolean;
  toContainer: number;
  toBrowser: number;
  readonly dropped: Dropped;
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
  #exited = false; // 見張っているコンテナが終わった
  #rateWindow = -1; // Math.floor(now / 1000)。DO に 1 つで、接続をまたいで続く
  #rateCount = 0;
  #cleaning: Promise<void> | null = null; // 動いている後始末（同時に 2 つ動かさない）

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
    return pathname === "/init" ? this.#init(request) : this.#connect(request);
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
    const image = container?.images[IMAGE_NAME];
    let started = false;
    if (alarmSet && container !== undefined && image !== undefined) {
      try {
        container.start({
          image,
          instance,
          enableInternet: false,
          env: { MODE: "single", PORT: String(CONTAINER_PORT) },
        });
        started = true;
      } catch (error) {
        this.#logError("start", error);
      }
    }
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

  // ---- コンテナの見張り ----

  #watch(container: ContainerLike): void {
    this.#watching = true;
    this.#exited = false;
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
      .then(() => {
        this.#exited = true;
        // 自分で destroy() した後の終わりは、すでに ended なので何もしない。
        this.#track(this.#endSession("container_lost"));
      });
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
      dropped: { binary: 0, oversize: 0, rate: 0, malformed: 0, internal: 0, buffer: 0 },
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

  async #attachContainer(conn: Connection): Promise<void> {
    const container = this.#ctx.container;
    if (!this.#watching) {
      // DO が作り直された。動いていれば見張りを付け直して続ける。
      if (container === undefined || !container.running) {
        await this.#endSession("container_lost");
        return;
      }
      this.#watch(container);
    }
    if (this.#exited || container === undefined) {
      await this.#endSession("container_lost");
      return;
    }

    const row = this.#row();
    if (row === null || row.state === "ended" || !this.#isCurrent(conn)) return;

    if (row.readyAt === null) {
      const ready = await this.#waitReady(container, row, conn);
      if (ready === "stale") return;
      if (ready === "timeout") {
        const state = this.#row()?.state;
        await this.#endSession(state === "starting" ? "start_timeout" : "container_lost");
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
    socket.addEventListener("message", (event) => this.#onContainerMessage(conn, event.data));
    socket.addEventListener("close", () => this.#onContainerClosed(conn));
    socket.addEventListener("error", () => this.#onContainerClosed(conn));

    // 同じ sessionId の 2 本目以降の /ws でも internal.init を送る（コンテナはつなぎ直すだけ。T-302）。
    // T-308b は stage_hash があれば internal.restore に変える（DesignDoc §12.3「復元の約束」）。
    safeSend(
      socket,
      JSON.stringify({
        v: 1,
        type: "internal.init",
        payload: {
          sessionId: current.sessionId,
          stageId: current.stageId,
          seed: current.seed,
          demo: current.demo,
        },
      }),
    );
    if (current.state === "starting") this.#update({ state: "running" });

    for (const message of conn.buffer.splice(0)) {
      if (safeSend(socket, message)) conn.toContainer += 1;
    }
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
    if (verdict !== "forward") {
      conn.dropped[verdict] += 1;
      return;
    }
    // 受けた文字列をそのまま送る（中身を書き換えない）。
    if (conn.container === null) {
      if (conn.buffer.length >= MAX_BUFFERED_MESSAGES) {
        conn.dropped.buffer += 1;
        return;
      }
      conn.buffer.push(data);
      return;
    }
    if (safeSend(conn.container, data)) conn.toContainer += 1;
  }

  #onBrowserClosed(conn: Connection): void {
    if (!this.#isCurrent(conn)) return;
    this.#detach(conn, {});
  }

  // ---- コンテナからの電文 ----

  #onContainerMessage(conn: Connection, data: unknown): void {
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
    // ほかの電文は、置き換えた・ブラウザが閉じた接続のソケットから届いたら、すべて捨てる。
    if (!this.#isCurrent(conn)) return;
    if (message.seq !== null) conn.lastSeq = message.seq;
    this.#handleContainerMessage(conn, data, message);
  }

  #handleContainerMessage(conn: Connection, data: string, message: ContainerMessage): void {
    switch (message.kind) {
      case "result":
        return; // #onContainerMessage で扱う
      case "ping":
        if (conn.container !== null) safeSend(conn.container, PONG);
        return;
      case "internal":
        return; // internal.pong・ほか（T-308b が internal.pong を死活監視に使う）
      case "forward":
        if (safeSend(conn.browser, data)) conn.toBrowser += 1;
        if (message.endsSession) this.#track(this.#endSession("container_error"));
        return;
    }
  }

  #saveResult(payload: unknown): void {
    const text = JSON.stringify(payload) ?? "null";
    if (exceedsBytes(text, MAX_RESULT_BYTES)) {
      this.#log("warn", "session.error", { op: "result", error: "too_large" });
      return;
    }
    this.#update({ resultJson: text, resultAt: this.#deps.now() });
  }

  #onContainerClosed(conn: Connection): void {
    if (!this.#isCurrent(conn)) return;
    if (this.#row()?.state === "ended") return;
    // 終わりにしない（ブラウザはつなぎ直す。コンテナが止まっていれば、見張りか次の /connect で終わる）。
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
    this.#update({ state: "ended", endReason: reason, endedAt });

    const conn = this.#conn;
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
