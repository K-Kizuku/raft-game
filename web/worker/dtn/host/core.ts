// ActorHost の中身（DTN-12・docs/dtn/persistence-and-recovery.md（§7 は DTN-07 の Go の実装の細部）・
// actor-protocol.md・handoff-and-contact.md）。
//
// 1 Actor に 1 つ（idFromName("<sessionId>/<actor>")）。正本は ctx.storage.sql（schema.ts）。Container の
// メモリとディスクは正本にしない。Go の SQLite adapter（server/internal/dtn/host）と同じ契約で、同じ fixture
// （server/internal/dtn/host/testdata/contract/*.json）を通す（contract.test.ts）。
//
//   POST /start      {sessionId, actor, deadlineAt, maxAlarms, initialEpoch?} → 200 {epoch}・409・503 start_failed
//   POST /host       要求のエンベロープ（§2.1）                 → 200 応答のエンベロープ（§2.2。エラーも 200）
//   POST /cla/accept transfer.accept のエンベロープ（RPC CLA）   → 200 応答のエンベロープ
//   POST /shutdown   {sessionId}                                → 200 {}・500 destroy_failed（アラームで再試行）
//   alarm()          自分の期限で Container を止めて保存を消す（DtnSession が止まっても Container を残さない）
//
// 確定の順序（§3.1）: エンベロープの確認 → 冪等の確認 → journal に intent → Container に送る →
// 1 つのトランザクションで bundles・then の効果・journal の applied → 応答（bundle の bytes を除く）。
// Host の確定の前に、応答を返さない。確定するのは ok と Actor の決定的な誤り（invalid・too_large）だけ（§7.2）。

import type { ContainerInstance } from "../../admin/guards.ts";
import type { SqlValue } from "../../admin/cf-types.ts";
import { decodeBase64, digestOf, parseStrictJson, sha256Hex, utf8Bytes } from "../codec.ts";
import {
  connectSocket,
  errorName,
  startContainer,
  waitHealthy,
  type LinkDeps,
} from "../container-link.ts";
import {
  contactEpochOf,
  counterpartOf,
  createContactSigner,
  parseContact,
  type Contact,
  type ContactSigner,
} from "../contact.ts";
import type { CallResult, DtnStateLike } from "../cf-types.ts";
import {
  ACTIVATE_THEN,
  ACTOR_PATTERN,
  CLEANUP_RETRY_MS,
  CONTAINER_KINDS,
  CONTAINER_PORT,
  CONTAINER_RESPONSE_TIMEOUT_MS,
  DEFAULT_HOST_LIMITS,
  errorResponse,
  GC_THEN,
  HOST_KINDS,
  isLetterId,
  isObject,
  isSafeNonNegative,
  MAX_CONTAINER_STARTS,
  MAX_RESTORE_MS,
  NOT_JOURNALED,
  okResponse,
  outputBundle,
  outputLetterId,
  parseEnvelope,
  parseLetterIds,
  parseResponse,
  PROTOCOL_VERSION,
  ProtocolError,
  requestDigestBody,
  responseDigestBody,
  roleOf,
  SESSION_ID_PATTERN,
  stripBundles,
  TRANSFER_ID_PATTERN,
  type Envelope,
  type ErrorCode,
  type HostLimits,
  type JsonObject,
  type Output,
  type ResponseEnvelope,
} from "../protocol.ts";
import { ActorChannel, ChannelLost, type SetTimer } from "./channel.ts";
import { checkLetters, checkPayload } from "./payload.ts";
import { ensureHostSchema, HOST_SCHEMA_VERSION } from "./schema.ts";

export const DEFAULT_ACTOR_START_TIMEOUT_MS = 60_000;

// 止める位置（契約テストの crashAt。persistence-and-recovery.md §7.3）。
export type HostPoint = "afterIntent" | "afterActorReply" | "afterCommit";

// deps.at が投げると、Host はそこで処理をやめて応答を返さない（プロセスが止まったのと同じ扱い）。
export class HostStopped extends Error {
  constructor(point: HostPoint) {
    super(`host stopped at ${point}`);
    this.name = "HostStopped";
  }
}

export interface ActorHostDeps extends LinkDeps {
  readonly log: (line: string) => void;
  readonly setTimer: SetTimer;
  readonly instance: ContainerInstance;
  readonly contactKey: string | undefined; // DTN_CONTACT_KEY（なければ Contact を使う要求はすべて断る）
  readonly startTimeoutMs: number;
  readonly responseTimeoutMs: number;
  readonly limits?: Partial<HostLimits>;
  readonly at?: (point: HostPoint) => void; // テスト用（止める位置）
  // 他の ActorHost の fetch（RPC CLA。ADR-0023）。idFromName("<sessionId>/<actor>")
  readonly callHost: (
    sessionId: string,
    actor: string,
    path: string,
    body: unknown,
  ) => Promise<CallResult>;
  // DtnSession の fetch（復元の許可）。idFromName(sessionId)
  readonly callSession: (sessionId: string, path: string, body: unknown) => Promise<CallResult>;
}

export const DEFAULT_HOST_DEPS = {
  startTimeoutMs: DEFAULT_ACTOR_START_TIMEOUT_MS,
  responseTimeoutMs: CONTAINER_RESPONSE_TIMEOUT_MS,
} as const;

type HostState =
  | "idle" // この DO のインスタンスでは、まだ Container につないでいない（作り直された DO を含む）
  | "starting"
  | "running"
  | "lost"
  | "restoring"
  | "failed"
  | "stopping"
  | "deleted";

interface JournalRow {
  readonly eventSeq: number;
  readonly kind: string;
  readonly reqDigest: string;
  readonly payload: string;
  readonly status: "intent" | "applied";
  readonly response: string | null;
  readonly respDigest: string | null;
}

interface TransferRow {
  readonly transferId: string;
  readonly role: "source" | "dest";
  readonly peer: string;
  readonly contactId: string;
  readonly letterIds: readonly number[];
  readonly digests: readonly { readonly letterId: number; readonly digest: string }[];
  readonly state: string;
  readonly ack: string | null;
}

interface BundleRow {
  readonly letterId: number;
  readonly bundle: string | null; // base64（DO の SQLite では TEXT で入れる。Go は BLOB）
  readonly digest: string;
  readonly size: number;
  readonly state: string;
  readonly place: string;
  readonly transferId: string | null;
}

interface Base {
  readonly actor: string;
  readonly epoch: number;
  readonly eventSeq: number;
}

interface CheckedBundle {
  readonly bundle: string;
  readonly digest: string;
  readonly size: number;
}

const HOST_STATUS_OUTPUT = "host.status";

function str(value: SqlValue | undefined): string | null {
  return typeof value === "string" ? value : null;
}

function num(value: SqlValue | undefined): number {
  return typeof value === "number" ? value : 0;
}

function fail(code: ErrorCode, message: string = code): never {
  throw new ProtocolError(code, message);
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

// 誤りの応答の宛先（電文の actor・eventSeq。読めなければ空と 0）。
function rawBase(raw: unknown): { actor: string; eventSeq: number } {
  if (!isObject(raw)) return { actor: "", eventSeq: 0 };
  return {
    actor: typeof raw["actor"] === "string" ? raw["actor"] : "",
    eventSeq: isSafeNonNegative(raw["eventSeq"]) ? raw["eventSeq"] : 0,
  };
}

// Container の応答を確定するか（§7.2）。ok と、Actor が決定的に出した誤り（invalid・too_large）。
function commitsResponse(response: ResponseEnvelope): boolean {
  if (response.status === "ok") return true;
  const code = response.error?.code;
  return code === "invalid" || code === "too_large";
}

export class ActorHostCore {
  readonly #ctx: DtnStateLike;
  readonly #deps: ActorHostDeps;
  readonly #limits: HostLimits;
  readonly #signer: ContactSigner | null;
  #state: HostState = "idle";
  #channel: ActorChannel | null = null;
  #queue: Promise<unknown> = Promise.resolve();
  #starting: Promise<number> | null = null;
  #restoring: Promise<void> | null = null;
  #failedCode: ErrorCode = "not_ready";
  #watchGen = 0;
  readonly #tasks = new Set<Promise<void>>();

  constructor(ctx: DtnStateLike, deps: ActorHostDeps) {
    this.#ctx = ctx;
    this.#deps = deps;
    this.#limits = { ...DEFAULT_HOST_LIMITS, ...deps.limits };
    this.#signer = deps.contactKey === undefined ? null : createContactSigner(deps.contactKey);
    ensureHostSchema(ctx.storage.sql);
  }

  // ---- 入り口 ----

  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);
    if (request.method !== "POST") return json(405, { code: "method_not_allowed" });
    const text = await request.text();
    switch (pathname) {
      case "/start":
        return this.#start(text);
      case "/host":
        return json(200, await this.#hostText(text, false));
      case "/cla/accept":
        return json(200, await this.#hostText(text, true));
      case "/shutdown":
        return this.#shutdownRequest(text);
      default:
        return json(404, { code: "not_found" });
    }
  }

  async alarm(): Promise<void> {
    if (this.#meta("sessionId") === null) return; // 保存を消した後
    const deadlineAt = Number(this.#meta("deadlineAt") ?? "0");
    if (this.#meta("shutdownPending") === "1" || this.#deps.now() >= deadlineAt) {
      this.#log("info", "host.deadline", {});
      await this.#shutdown();
      return;
    }
    await this.#setAlarm(deadlineAt);
  }

  // テスト用: 進行中の非同期の処理（復元など）がすべて終わるまで待つ。
  async drain(): Promise<void> {
    while (this.#tasks.size > 0) await Promise.all([...this.#tasks]);
    await this.#queue.catch(() => undefined);
  }

  get state(): HostState {
    return this.#state;
  }

  // ---- meta ----

  #meta(key: string): string | null {
    const [row] = this.#ctx.storage.sql.exec("SELECT v FROM meta WHERE k = ?", key).toArray();
    return str(row?.["v"]);
  }

  #setMeta(key: string, value: string | number): void {
    this.#ctx.storage.sql.exec(
      "INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v",
      key,
      String(value),
    );
  }

  #epoch(): number {
    return Number(this.#meta("epoch") ?? "0");
  }

  #actor(): string {
    return this.#meta("actor") ?? "";
  }

  #base(env: { readonly actor: string; readonly eventSeq: number }): Base {
    return { actor: env.actor, epoch: this.#epoch(), eventSeq: env.eventSeq };
  }

  #log(level: "info" | "warn" | "error", event: string, fields: Record<string, unknown>): void {
    this.#deps.log(
      JSON.stringify({
        level,
        event,
        session: this.#meta("sessionId"),
        actor: this.#meta("actor"),
        epoch: this.#epoch(),
        ...fields,
      }),
    );
  }

  #track(promise: Promise<void>): void {
    const tracked = promise.catch((error: unknown) =>
      this.#log("error", "host.error", { op: "async", error: errorName(error) }),
    );
    this.#tasks.add(tracked);
    void tracked.finally(() => this.#tasks.delete(tracked));
  }

  #lock<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.#queue.then(fn, fn);
    this.#queue = run.catch(() => undefined);
    return run;
  }

  #at(point: HostPoint): void {
    this.#deps.at?.(point);
  }

  // ---- /start ----

  async #start(text: string): Promise<Response> {
    let body: unknown;
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      return json(400, { code: "invalid" });
    }
    if (!isObject(body)) return json(400, { code: "invalid" });
    const { sessionId, actor, deadlineAt, maxAlarms } = body;
    const initialEpoch = body["initialEpoch"] ?? 1;
    if (
      typeof sessionId !== "string" ||
      !SESSION_ID_PATTERN.test(sessionId) ||
      typeof actor !== "string" ||
      !ACTOR_PATTERN.test(actor) ||
      actor === "coordinator" ||
      !isSafeNonNegative(deadlineAt) ||
      !isSafeNonNegative(maxAlarms) ||
      maxAlarms < 1 ||
      !isSafeNonNegative(initialEpoch) ||
      initialEpoch < 1
    ) {
      return json(400, { code: "invalid" });
    }
    const stored = this.#meta("sessionId");
    if (stored !== null && (stored !== sessionId || this.#meta("actor") !== actor)) {
      return json(409, { code: "session_mismatch" });
    }
    if (this.#meta("shutdownPending") === "1") return json(409, { code: "session_ended" });
    if (stored === null) {
      this.#ctx.storage.transactionSync(() => {
        this.#setMeta("schemaVersion", HOST_SCHEMA_VERSION);
        this.#setMeta("sessionId", sessionId);
        this.#setMeta("actor", actor);
        this.#setMeta("epoch", initialEpoch - 1); // 最初の起動で initialEpoch になる
        this.#setMeta("maxCoordEpoch", 0);
        this.#setMeta("createdAt", new Date(this.#deps.now()).toISOString());
        this.#setMeta("journalRows", 0);
        this.#setMeta("storedBytes", 0);
        this.#setMeta("deadlineAt", deadlineAt);
        this.#setMeta("maxAlarms", maxAlarms);
        this.#setMeta("alarmCount", 0);
        this.#setMeta("starts", 0);
        this.#setMeta("containerUp", 0);
        this.#setMeta("restoreMs", 0);
      });
      // 期限のアラームを先に置く（今の GameSession と同じ。起動に失敗しても Container を残さない）
      if (!(await this.#setAlarm(deadlineAt))) return json(503, { code: "start_failed" });
    }
    if (this.#state === "running") return json(200, { epoch: this.#epoch() });
    this.#starting ??= this.#bootContainer().finally(() => {
      this.#starting = null;
    });
    try {
      const epoch = await this.#starting;
      this.#state = "running";
      return json(200, { epoch });
    } catch (error) {
      this.#log("error", "host.start_failed", { error: errorName(error) });
      this.#state = "failed";
      return json(503, { code: "start_failed" });
    }
  }

  // Container を起動し（epoch を 1 増やして保存）、準備を待ってからつなぐ。失敗したら例外。
  async #bootContainer(): Promise<number> {
    const container = this.#ctx.container;
    const starts = Number(this.#meta("starts") ?? "0");
    if (starts >= MAX_CONTAINER_STARTS) throw new Error("container start limit");
    const epoch = this.#epoch() + 1;
    this.#ctx.storage.transactionSync(() => {
      this.#setMeta("epoch", epoch);
      this.#setMeta("starts", starts + 1);
      this.#setMeta("containerUp", 1);
    });
    this.#state = "starting";
    const env = { ROLE: roleOf(this.#actor()), PORT: String(CONTAINER_PORT) };
    if (container === undefined || !startContainer(container, this.#deps.instance, env)) {
      throw new Error("container start failed");
    }
    const startedAt = this.#deps.now();
    this.#log("info", "container.start", { starts: starts + 1 });
    const watchGen = this.#watch();
    const ready = await waitHealthy(
      container,
      startedAt + this.#deps.startTimeoutMs,
      this.#deps,
      () => watchGen !== this.#watchGen,
    );
    if (ready !== "ready") throw new Error(`container not ready: ${ready}`);
    if (!(await this.#connect(epoch))) throw new Error("container connect failed");
    this.#log("info", "container.ready", { startMs: this.#deps.now() - startedAt });
    return epoch;
  }

  #watch(): number {
    const watchGen = ++this.#watchGen;
    const container = this.#ctx.container;
    let exit: Promise<void>;
    try {
      exit = container?.monitor() ?? Promise.resolve();
    } catch {
      exit = Promise.resolve();
    }
    void exit.then(
      () => this.#onContainerExit(watchGen),
      () => this.#onContainerExit(watchGen),
    );
    return watchGen;
  }

  async #connect(epoch: number): Promise<boolean> {
    const container = this.#ctx.container;
    if (container === undefined) return false;
    const socket = await connectSocket(container);
    if (socket === null) return false;
    this.#channel = new ActorChannel(socket, epoch, {
      setTimer: this.#deps.setTimer,
      timeoutMs: this.#deps.responseTimeoutMs,
      onLost: (cause) => this.#onLost(cause),
      maxBytes: this.#limits.maxMessageBytes,
    });
    return true;
  }

  // 作り直された DO のインスタンス（state が idle）: 動いている Container につなぎ直す（epoch は変えない）。
  // Container が動いていなければ、止まったとみなして復元を始める。
  async #ensureConnected(): Promise<void> {
    if (this.#state !== "idle" || this.#meta("sessionId") === null) return;
    const container = this.#ctx.container;
    if (container?.running === true) {
      this.#state = "starting";
      this.#watch();
      if (await this.#connect(this.#epoch())) {
        this.#state = "running";
        this.#log("info", "container.reattached", {});
        return;
      }
    }
    this.#state = "running";
    this.#onLost("not_attached");
  }

  #onContainerExit(watchGen: number): void {
    if (watchGen !== this.#watchGen) return;
    this.#onLost("monitor");
  }

  // Container が止まったとみなす。新しい要求に not_ready を返し、復元を始める（§4.1）。
  #onLost(cause: string): void {
    if (this.#state !== "running") return;
    this.#state = "lost";
    this.#channel?.close(cause);
    this.#channel = null;
    this.#log("warn", "container.lost", { cause });
    this.#track(this.#restore());
  }

  // host.status の container（§7.2: running・lost・stopped）。
  #containerStatus(): "running" | "lost" | "stopped" {
    switch (this.#state) {
      case "stopping":
      case "deleted":
        return "stopped";
      case "lost":
      case "restoring":
      case "failed":
        return "lost";
      default:
        return "running";
    }
  }

  // ---- /host・/cla/accept ----

  async #hostText(text: string, cla: boolean): Promise<ResponseEnvelope> {
    let raw: unknown;
    if (
      text.length > this.#limits.maxMessageBytes ||
      utf8Bytes(text) > this.#limits.maxMessageBytes
    ) {
      return errorResponse({ actor: "", epoch: this.#epoch(), eventSeq: 0 }, "too_large");
    }
    try {
      raw = parseStrictJson(text);
    } catch {
      return errorResponse(
        { actor: "", epoch: this.#epoch(), eventSeq: 0 },
        "invalid",
        "malformed JSON",
      );
    }
    const fallback = { ...rawBase(raw), epoch: this.#epoch() };
    let env: Envelope;
    try {
      env = parseEnvelope(raw);
    } catch (error) {
      return errorResponse(
        fallback,
        error instanceof ProtocolError ? error.code : "invalid",
        "malformed envelope",
      );
    }
    try {
      if (cla !== (env.kind === "transfer.accept")) fail("invalid", "kind not allowed here");
      return await this.#handle(env);
    } catch (error) {
      if (error instanceof HostStopped) throw error;
      if (error instanceof ProtocolError)
        return errorResponse(this.#base(env), error.code, error.message);
      this.#log("error", "host.error", { op: "handle", kind: env.kind, error: errorName(error) });
      return errorResponse(this.#base(env), "internal");
    }
  }

  // セッション・宛先・coordEpoch の確認と、coordEpoch の更新（persistence-and-recovery.md §5・§7.2）。
  // 要求の epoch は照合しない（Container に送るときに meta.epoch で上書きする。§7.2）。
  #checkEnvelope(env: Envelope): void {
    if (env.sessionId !== this.#meta("sessionId")) fail("session_mismatch");
    if (env.actor !== this.#actor()) fail("unknown_actor");
    const maxCoordEpoch = Number(this.#meta("maxCoordEpoch") ?? "0");
    if (env.coordEpoch < maxCoordEpoch) fail("stale_coord_epoch");
    if (env.coordEpoch > maxCoordEpoch) this.#setMeta("maxCoordEpoch", env.coordEpoch);
  }

  async #handle(env: Envelope): Promise<ResponseEnvelope> {
    if (this.#state === "deleted" || this.#meta("sessionId") === null) {
      // 保存を消した後（D-8）: shutdown だけ成功、ほかは not_ready
      if (env.kind === "shutdown") return okResponse(this.#base(env));
      fail("not_ready", "shut down");
    }
    this.#checkEnvelope(env);
    if (env.kind === "transfer.accept") return this.#accept(env);
    if (env.kind === "shutdown") return this.#lock(() => this.#shutdownKind(env));
    if (!CONTAINER_KINDS.has(env.kind) && !HOST_KINDS.has(env.kind))
      fail("invalid", "unknown kind");
    if (env.kind === "host.status") return this.#hostStatus(env);
    if (env.kind === "transfer.status") {
      return okResponse(this.#base(env), this.#transferStatus(env.payload));
    }
    const journaled = !NOT_JOURNALED.has(env.kind);
    if (journaled && env.eventSeq < 1) fail("invalid", "eventSeq must be 1 or more");
    await checkPayload(env.kind, env.payload, this.#limits);
    if (this.#meta("shutdownPending") === "1") fail("not_ready", "shutting down");
    if (!journaled) {
      return this.#lock(async () => {
        await this.#ensureConnected();
        this.#requireLive();
        return stripBundles(await this.#send(env));
      });
    }
    const reqDigest = await digestOf(requestDigestBody(env));
    return this.#lock(() => this.#journaled(env, reqDigest));
  }

  #requireLive(): void {
    if (this.#state === "failed") fail(this.#failedCode);
    if (this.#state !== "running" || this.#channel === null) fail("not_ready", "actor lost");
  }

  async #journaled(env: Envelope, reqDigest: string): Promise<ResponseEnvelope> {
    const row = this.#journalRow(env.eventSeq);
    if (row !== null) {
      if (row.reqDigest !== reqDigest) fail("conflict");
      if (row.status === "applied") return JSON.parse(row.response ?? "{}") as ResponseEnvelope;
      // intent: 止まった後の送り直し。§3.1 の 4 から続ける
    } else {
      const last = this.#lastRow();
      if (last !== null && env.eventSeq < last.eventSeq) fail("stale_event");
      if (last?.status === "intent")
        fail("not_ready", `eventSeq ${last.eventSeq} is pending; resend it first`);
    }
    // contact.* と transfer.abort は Container を使わない（復元の途中でも受ける）
    if (env.kind === "contact.open" || env.kind === "contact.close")
      return this.#contact(env, reqDigest);
    if (env.kind === "transfer.abort") return this.#abort(env, reqDigest);
    if (row === null) {
      if (this.#state === "failed") fail(this.#failedCode);
      if (this.#state !== "running" && this.#state !== "idle") fail("not_ready", "actor lost");
    }
    switch (env.kind) {
      case "transfer.prepare":
        return this.#prepare(env, reqDigest, row);
      case "transfer.activate":
        return this.#activate(env, reqDigest, row);
      case "transfer.gc":
        return this.#gc(env, reqDigest, row);
      default:
        if (row === null) this.#writeIntent(env, reqDigest, env.payload);
        this.#at("afterIntent");
        return this.#runContainer(env, env, null);
    }
  }

  // ---- journal ----

  #journalRow(eventSeq: number): JournalRow | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM journal WHERE event_seq = ?", eventSeq)
      .toArray();
    return row === undefined ? null : this.#toJournalRow(row);
  }

  #toJournalRow(row: Record<string, SqlValue>): JournalRow {
    return {
      eventSeq: num(row["event_seq"]),
      kind: str(row["kind"]) ?? "",
      reqDigest: str(row["req_digest"]) ?? "",
      payload: str(row["payload"]) ?? "{}",
      status: row["status"] === "applied" ? "applied" : "intent",
      response: str(row["response"]),
      respDigest: str(row["resp_digest"]),
    };
  }

  #lastRow(): JournalRow | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM journal ORDER BY event_seq DESC LIMIT 1")
      .toArray();
    return row === undefined ? null : this.#toJournalRow(row);
  }

  #lastAppliedSeq(): number {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT MAX(event_seq) AS m FROM journal WHERE status = 'applied'")
      .toArray();
    return num(row?.["m"]);
  }

  // 保存の量（§7.1: journalRows と storedBytes = bundles の bytes ＋ journal の payload ＋ response の長さ）。
  #stats(): { rows: number; bytes: number } {
    const sql = this.#ctx.storage.sql;
    const [j] = sql
      .exec(
        `SELECT COUNT(*) AS n, COALESCE(SUM(LENGTH(CAST(payload AS BLOB)) +
           COALESCE(LENGTH(CAST(response AS BLOB)), 0)), 0) AS b FROM journal`,
      )
      .toArray();
    const [b] = sql
      .exec(
        "SELECT COALESCE(SUM(CASE WHEN bundle IS NULL THEN 0 ELSE size END), 0) AS b FROM bundles",
      )
      .toArray();
    return { rows: num(j?.["n"]), bytes: num(j?.["b"]) + num(b?.["b"]) };
  }

  // meta の journalRows・storedBytes を表から数え直す（トランザクションの中で呼ぶ）。上限を超えたら too_large。
  #recount(): void {
    const stats = this.#stats();
    if (stats.bytes > this.#limits.maxStoredBytes) fail("too_large", "actor storage limit");
    this.#setMeta("journalRows", stats.rows);
    this.#setMeta("storedBytes", stats.bytes);
  }

  // intent の行を書く（§3.1 の 3）。行の数＋1・合計＋payload が上限を超えるなら too_large（何も書かない）。
  #writeIntent(env: Envelope, reqDigest: string, payload: JsonObject): void {
    const text = JSON.stringify(payload);
    const stats = this.#stats();
    if (stats.rows + 1 > this.#limits.maxJournalRows) fail("too_large", "journal row limit");
    if (stats.bytes + utf8Bytes(text) > this.#limits.maxStoredBytes)
      fail("too_large", "actor storage limit");
    this.#ctx.storage.transactionSync(() => {
      this.#ctx.storage.sql.exec(
        `INSERT INTO journal (event_seq, kind, req_digest, payload, status, response, resp_digest, epoch)
         VALUES (?, ?, ?, ?, 'intent', NULL, NULL, ?)`,
        env.eventSeq,
        env.kind,
        reqDigest,
        text,
        this.#epoch(),
      );
      // 復元（§4.1 の 4）はこれで init を送る（intent のまま止まっても）
      if (env.kind === "init" && this.#meta("initPayload") === null)
        this.#setMeta("initPayload", text);
      this.#recount();
    });
  }

  #markApplied(eventSeq: number, response: ResponseEnvelope, respDigest: string): void {
    this.#ctx.storage.sql.exec(
      "UPDATE journal SET status = 'applied', response = ?, resp_digest = ?, epoch = ? WHERE event_seq = ?",
      JSON.stringify(response),
      respDigest,
      this.#epoch(),
      eventSeq,
    );
  }

  // Host だけで処理した要求を applied で保存して返す（§3.2）。
  async #finishHostOnly(
    env: Envelope,
    reqDigest: string,
    response: ResponseEnvelope,
    effects: () => void,
  ): Promise<ResponseEnvelope> {
    const respDigest = await digestOf(responseDigestBody(response));
    if (this.#journalRow(env.eventSeq) === null) this.#writeIntent(env, reqDigest, env.payload);
    this.#ctx.storage.transactionSync(() => {
      effects();
      this.#markApplied(env.eventSeq, response, respDigest);
      this.#recount();
    });
    return response;
  }

  // ---- Container ----

  // Container に送って応答を待つ（epoch は meta.epoch で上書きする）。応答の epoch が違えば stale_epoch
  // （確定しない・失ったとみなさない）。応答しない・壊れた応答・別の要求の応答は not_ready（失ったとみなす）。
  async #send(request: Envelope): Promise<ResponseEnvelope> {
    await this.#ensureConnected();
    const channel = this.#channel;
    if (channel === null || this.#state !== "running") fail("not_ready", "actor lost");
    const epoch = this.#epoch();
    const sent: Record<string, unknown> = { ...request, epoch };
    let raw: unknown;
    try {
      raw = await channel.request(sent as Record<string, unknown> & { eventSeq: number });
    } catch (error) {
      if (error instanceof ChannelLost) fail("not_ready", "actor did not answer");
      throw error;
    }
    let response: ResponseEnvelope;
    try {
      response = parseResponse(raw);
    } catch {
      this.#onLost("malformed_response");
      fail("not_ready", "actor returned a malformed response");
    }
    if (response.epoch !== epoch)
      fail("stale_epoch", `response epoch ${response.epoch} is not ${epoch}`);
    if (response.actor !== request.actor || response.eventSeq !== request.eventSeq) {
      this.#onLost("mismatched_response");
      fail("not_ready", "actor returned a response for another request");
    }
    if (response.outputs.length > this.#limits.maxOutputs) {
      this.#onLost("too_many_outputs");
      fail("not_ready", "actor returned too many outputs");
    }
    return response;
  }

  // §3.1 の 4〜6。request は Container に送る要求、env は受けた要求（journal の行の番号・種類）。
  // extra は確定のトランザクションの中で行う追加の効果（transfer の状態）。
  async #runContainer(
    env: Envelope,
    request: Envelope,
    extra: (() => void) | null,
  ): Promise<ResponseEnvelope> {
    const response = await this.#send(request);
    this.#at("afterActorReply");
    const stored = stripBundles(response);
    if (!commitsResponse(response)) {
      // Container が処理しなかった（not_ready など）か、作り直しが要る（internal）。行は intent のまま
      if (response.error?.code === "internal") this.#onLost("actor_internal");
      return stored;
    }
    const ok = response.status === "ok";
    const bundles = ok
      ? await this.#checkOutputBundles(response.outputs)
      : new Map<number, CheckedBundle>();
    const letters =
      ok && ACTIVATE_THEN.has(request.kind)
        ? await checkLetters(request.kind, request.payload, this.#limits)
        : [];
    const respDigest = await digestOf(responseDigestBody(stored));
    const merge = ok && env.kind === "advance" ? await this.#mergePlan(env.eventSeq, stored) : null;
    this.#ctx.storage.transactionSync(() => {
      const intent = this.#journalRow(env.eventSeq);
      if (intent?.status !== "intent") fail("internal", "journal row is not an intent");
      if (ok) {
        this.#applyRequest(request.kind, request.payload, letters, env.eventSeq);
        this.#applyOutputs(response.outputs, bundles, env.eventSeq);
        extra?.();
      }
      if (merge !== null) {
        this.#ctx.storage.sql.exec("DELETE FROM journal WHERE event_seq = ?", merge.prevSeq);
        this.#markApplied(env.eventSeq, merge.response, merge.digest);
      } else {
        this.#markApplied(env.eventSeq, stored, respDigest);
      }
      this.#recount();
    });
    this.#at("afterCommit");
    // まとめた行でも、返すのはこの要求の応答
    return stored;
  }

  // 直前の行も applied の ok の advance なら、まとめる（§2・D-9）。まとめた上限への 1 回の再生の応答が
  // 上限（outputs の数・電文の長さ。bytes は data.size を base64 にした長さで見積もる）を超えるときはまとめない。
  async #mergePlan(
    eventSeq: number,
    stored: ResponseEnvelope,
  ): Promise<{ prevSeq: number; response: ResponseEnvelope; digest: string } | null> {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM journal WHERE event_seq < ? ORDER BY event_seq DESC LIMIT 1", eventSeq)
      .toArray();
    if (row === undefined) return null;
    const prev = this.#toJournalRow(row);
    if (prev.kind !== "advance" || prev.status !== "applied") return null;
    const prevResponse = JSON.parse(prev.response ?? "{}") as Partial<ResponseEnvelope>;
    if (prevResponse.status !== "ok") return null;
    const outputs = [...(prevResponse.outputs ?? []), ...stored.outputs];
    if (outputs.length > this.#limits.maxOutputs) return null;
    const merged: ResponseEnvelope = { ...stored, outputs };
    let size = utf8Bytes(JSON.stringify(merged));
    for (const output of outputs) {
      const s = output.data["size"];
      if (
        (output.type === "letter.created" || output.type === "bundle.exported") &&
        isSafeNonNegative(s) &&
        s > 0
      ) {
        size += 4 * Math.ceil(s / 3) + ',"bundle":""'.length;
      }
    }
    if (size > this.#limits.maxMessageBytes) return null;
    return {
      prevSeq: prev.eventSeq,
      response: merged,
      digest: await digestOf(responseDigestBody(merged)),
    };
  }

  // 出力の Bundle を確かめる（§7.2: letter.created は bytes が要る・上限・digest・data.size）。不正なら invalid。
  async #checkOutputBundles(outputs: readonly Output[]): Promise<Map<number, CheckedBundle>> {
    const out = new Map<number, CheckedBundle>();
    for (const [i, output] of outputs.entries()) {
      if (output.type !== "letter.created" && output.type !== "bundle.exported") continue;
      const letterId = outputLetterId(output);
      if (letterId === null) fail("invalid", `outputs[${i}]: needs data.letterId`);
      const bundle = outputBundle(output);
      if (bundle === null) fail("invalid", `outputs[${i}]: without bundle`);
      const bytes = decodeBase64(bundle);
      if (bytes === null) fail("invalid", `outputs[${i}]: bundle is not base64`);
      if (bytes.byteLength > this.#limits.maxBundleBytes)
        fail("invalid", `outputs[${i}]: bundle too large`);
      const digest = await sha256Hex(bytes);
      if (output.data["digest"] !== digest)
        fail("invalid", `outputs[${i}]: data.digest does not match`);
      const size = output.data["size"];
      if (size !== undefined && size !== bytes.byteLength)
        fail("invalid", `outputs[${i}]: data.size`);
      out.set(letterId, { bundle, digest, size: bytes.byteLength });
    }
    return out;
  }

  #putBundle(row: BundleRow, seq: number): void {
    this.#ctx.storage.sql.exec(
      `INSERT INTO bundles (letter_id, bundle, digest, size, state, place, transfer_id, updated_seq)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT (letter_id) DO UPDATE SET bundle = excluded.bundle, digest = excluded.digest,
         size = excluded.size, state = excluded.state, place = excluded.place,
         transfer_id = excluded.transfer_id, updated_seq = excluded.updated_seq`,
      row.letterId,
      row.bundle,
      row.digest,
      row.size,
      row.state,
      row.place,
      row.transferId,
      seq,
    );
  }

  // 行があれば state・place を変える（drop なら bytes を NULL にする）。行が無ければ何もしない。
  #markBundle(letterId: number, state: string, place: string, seq: number, drop: boolean): void {
    this.#ctx.storage.sql.exec(
      `UPDATE bundles SET state = ?, place = ?, updated_seq = ?,
         bundle = CASE WHEN ? = 1 THEN NULL ELSE bundle END
       WHERE letter_id = ?`,
      state,
      place,
      seq,
      drop ? 1 : 0,
      letterId,
    );
  }

  // 要求の効果（§7.2 の表。出力の効果より先）。
  #applyRequest(
    kind: string,
    payload: JsonObject,
    letters: readonly {
      letterId: number;
      bundle: string;
      digest: string;
      size: number;
      place: "port" | "relay" | null;
    }[],
    seq: number,
  ): void {
    if (GC_THEN.has(kind)) {
      for (const id of letterIdsOf(payload)) this.#markBundle(id, "gone", "moved", seq, true);
      return;
    }
    for (const l of letters) {
      const [state, place] =
        kind === "deliver"
          ? ["gone", "delivered"]
          : kind === "cargo.add"
            ? ["held", "cargo"]
            : ["held", l.place ?? "port"];
      this.#putBundle(
        {
          letterId: l.letterId,
          bundle: l.bundle,
          digest: l.digest,
          size: l.size,
          state,
          place,
          transferId: null,
        },
        seq,
      );
    }
  }

  // 出力の効果（出力の順）: letter.created の bytes を保存し、捨てた・配達した手紙の行を gone にする。
  #applyOutputs(
    outputs: readonly Output[],
    bundles: ReadonlyMap<number, CheckedBundle>,
    seq: number,
  ): void {
    for (const output of outputs) {
      const letterId = outputLetterId(output);
      if (letterId === null) continue;
      if (output.type === "letter.created") {
        const b = bundles.get(letterId);
        if (b === undefined) continue;
        this.#putBundle({ letterId, ...b, state: "held", place: "port", transferId: null }, seq);
      } else if (output.type === "letter.dropped") {
        this.#markBundle(letterId, "gone", "dropped", seq, false);
      } else if (output.type === "letter.delivered") {
        this.#markBundle(letterId, "gone", "delivered", seq, false);
      }
    }
  }

  // ---- 状態の照会（journal に入れない）----

  // §7.2: outputs: [{type: "host.status", data: {epoch, lastEventSeq, container}}]（DTN-12 は data に数を足す）。
  #hostStatus(env: Envelope): ResponseEnvelope {
    const stats = this.#stats();
    const data = {
      epoch: this.#epoch(),
      lastEventSeq: this.#lastAppliedSeq(),
      container: this.#containerStatus(),
      starts: Number(this.#meta("starts") ?? "0"),
      journalRows: stats.rows,
      storedBytes: stats.bytes,
      restoreMs: Number(this.#meta("restoreMs") ?? "0"),
    };
    return {
      ...okResponse(this.#base(env)),
      outputs: [{ t: 0, class: 0, seq: 0, type: HOST_STATUS_OUTPUT, data }],
    };
  }

  #transferStatus(payload: JsonObject): JsonObject {
    const transferId = payload["transferId"];
    if (typeof transferId !== "string" || !TRANSFER_ID_PATTERN.test(transferId)) fail("invalid");
    const row = this.#transfer(transferId);
    if (row === null) return { transferId, state: "unknown" };
    return {
      transferId,
      role: row.role,
      state: row.state,
      ack: row.ack === null ? null : (JSON.parse(row.ack) as unknown),
    };
  }

  // ---- Contact（handoff-and-contact.md §2）----

  async #verifyContact(raw: unknown): Promise<Contact> {
    const contact = parseContact(raw);
    if (this.#signer === null || !(await this.#signer.verify(contact))) {
      fail("contact_mismatch", "bad signature");
    }
    if (contact.sessionId !== this.#meta("sessionId")) fail("contact_mismatch", "session");
    if (counterpartOf(contact, this.#actor()) === null) fail("contact_mismatch", "not a party");
    return contact;
  }

  #contactState(contactId: string): string | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT state FROM contacts WHERE contact_id = ?", contactId)
      .toArray();
    return str(row?.["state"]);
  }

  async #contact(env: Envelope, reqDigest: string): Promise<ResponseEnvelope> {
    const contact = await this.#verifyContact(env.payload["contact"]);
    const base = this.#base(env);
    const body = JSON.stringify(contact);
    if (env.kind === "contact.open") {
      if (this.#contactState(contact.contactId) === "closed") {
        return this.#finishHostOnly(
          env,
          reqDigest,
          errorResponse(base, "contact_closed"),
          () => undefined,
        );
      }
      return this.#finishHostOnly(
        env,
        reqDigest,
        okResponse(base, { contactId: contact.contactId }),
        () => {
          this.#ctx.storage.sql.exec(
            "INSERT INTO contacts (contact_id, body, state) VALUES (?, ?, 'open') ON CONFLICT (contact_id) DO NOTHING",
            contact.contactId,
            body,
          );
        },
      );
    }
    return this.#finishHostOnly(
      env,
      reqDigest,
      okResponse(base, { contactId: contact.contactId }),
      () => {
        this.#ctx.storage.sql.exec(
          `INSERT INTO contacts (contact_id, body, state) VALUES (?, ?, 'closed')
         ON CONFLICT (contact_id) DO UPDATE SET state = 'closed'`,
          contact.contactId,
          body,
        );
      },
    );
  }

  // handoff-and-contact.md §2.2 の 1〜5（署名・セッション・当事者・epoch・閉じていない）。
  async #checkTransferContact(raw: unknown, peer: string): Promise<Contact> {
    const contact = await this.#verifyContact(raw);
    if (counterpartOf(contact, this.#actor()) !== peer) fail("contact_mismatch", "peer");
    if (contactEpochOf(contact, this.#actor()) !== this.#epoch())
      fail("stale_epoch", "contact epoch");
    if (this.#contactState(contact.contactId) === "closed") fail("contact_closed");
    return contact;
  }

  // ---- transfers（handoff-and-contact.md §3）----

  #transfer(transferId: string): TransferRow | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM transfers WHERE transfer_id = ?", transferId)
      .toArray();
    if (row === undefined) return null;
    return {
      transferId,
      role: row["role"] === "dest" ? "dest" : "source",
      peer: str(row["peer"]) ?? "",
      contactId: str(row["contact_id"]) ?? "",
      letterIds: JSON.parse(str(row["letter_ids"]) ?? "[]") as number[],
      digests: JSON.parse(str(row["digests"]) ?? "[]") as TransferRow["digests"],
      state: str(row["state"]) ?? "",
      ack: str(row["ack"]),
    };
  }

  #bundle(letterId: number): BundleRow | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM bundles WHERE letter_id = ?", letterId)
      .toArray();
    if (row === undefined) return null;
    return {
      letterId,
      bundle: str(row["bundle"]),
      digest: str(row["digest"]) ?? "",
      size: num(row["size"]),
      state: str(row["state"]) ?? "",
      place: str(row["place"]) ?? "",
      transferId: str(row["transfer_id"]),
    };
  }

  // 送り元: prepared にしてから受け取る Host の /cla/accept を呼ぶ（§3.1）。
  async #prepare(
    env: Envelope,
    reqDigest: string,
    row: JournalRow | null,
  ): Promise<ResponseEnvelope> {
    const p = env.payload;
    const transferId = p["transferId"];
    const to = p["to"];
    const t = p["t"];
    if (
      typeof transferId !== "string" ||
      !TRANSFER_ID_PATTERN.test(transferId) ||
      typeof to !== "string" ||
      !ACTOR_PATTERN.test(to) ||
      !isSafeNonNegative(t)
    ) {
      fail("invalid", "prepare payload");
    }
    const letterIds = parseLetterIds(p["letterIds"]);
    const contact = await this.#checkTransferContact(p["contact"], to);
    let transfer = this.#transfer(transferId);
    if (transfer !== null) {
      if (
        transfer.role !== "source" ||
        transfer.peer !== to ||
        !sameIds(transfer.letterIds, letterIds)
      ) {
        fail("conflict", "transfer differs");
      }
      if (transfer.state === "aborted") fail("conflict", "transfer aborted");
    }
    const base = this.#base(env);
    if (transfer === null) {
      // §2.2 の 6: 対象の手紙をすべて持っていて、別の transfer の途中でない
      for (const id of letterIds) {
        const b = this.#bundle(id);
        if (b === null || b.state !== "held") fail("invalid", "letter not held");
      }
      if (row === null) this.#writeIntent(env, reqDigest, env.payload);
      this.#at("afterIntent");
      // Bundle Age と CRC を更新した bytes を Actor から受け取る（bpv7-profile.md §3）
      const exported = await this.#send({
        ...env,
        kind: "bundle.export",
        payload: { t, letterIds },
      });
      if (exported.status !== "ok") {
        if (!commitsResponse(exported)) return stripBundles(exported);
        return this.#finishHostOnly(env, reqDigest, stripBundles(exported), () => undefined);
      }
      const bundles = await this.#checkOutputBundles(exported.outputs);
      if (!letterIds.every((id) => bundles.has(id)))
        fail("invalid", "bundle.export missing letters");
      const digests = letterIds.map((id) => ({
        letterId: id,
        digest: bundles.get(id)?.digest ?? "",
      }));
      this.#ctx.storage.transactionSync(() => {
        this.#ctx.storage.sql.exec(
          `INSERT INTO transfers (transfer_id, role, peer, contact_id, letter_ids, digests, state, ack)
           VALUES (?, 'source', ?, ?, ?, ?, 'prepared', NULL)`,
          transferId,
          to,
          contact.contactId,
          JSON.stringify(letterIds),
          JSON.stringify(digests),
        );
        for (const id of letterIds) {
          const b = bundles.get(id);
          const current = this.#bundle(id);
          if (b === undefined || current === null) continue;
          this.#putBundle({ ...current, ...b, state: "prepared", transferId }, env.eventSeq);
        }
        this.#recount();
      });
      transfer = this.#transfer(transferId);
    }
    if (transfer === null) fail("internal", "transfer missing");
    if (transfer.ack !== null) {
      return this.#finishHostOnly(
        env,
        reqDigest,
        okResponse(base, JSON.parse(transfer.ack) as unknown),
        () => undefined,
      );
    }
    // RPC CLA（handoff-and-contact.md §4）: bytes は Actor Host の間だけを動く
    const bundles = transfer.letterIds.map((id) => {
      const b = this.#bundle(id);
      return { letterId: id, bundle: b?.bundle ?? "", digest: b?.digest ?? "" };
    });
    const acceptEnv = {
      v: PROTOCOL_VERSION,
      sessionId: env.sessionId,
      actor: to,
      epoch: contactEpochOf(contact, to) ?? 0,
      coordEpoch: env.coordEpoch,
      eventSeq: env.eventSeq,
      kind: "transfer.accept",
      payload: { transferId, from: this.#actor(), contact, bundles },
    };
    let result: CallResult;
    try {
      result = await this.#deps.callHost(env.sessionId, to, "/cla/accept", acceptEnv);
    } catch (error) {
      this.#log("warn", "transfer.accept_failed", {
        transfer: transferId,
        error: errorName(error),
      });
      fail("not_ready", "peer unreachable"); // intent のまま。同じ eventSeq の送り直しで accept からやり直す
    }
    const ack = parseAck(result);
    if (ack === null) fail("not_ready", "peer response");
    if (ack.status !== "ok") {
      // 受け取り側が断った（Contact の外など）。prepared のまま（Coordinator が transfer.abort で取り消す）
      const code = ack.code;
      const refused: ResponseEnvelope = {
        ...errorResponse(base, "invalid"),
        error: { code, message: code },
      };
      return this.#finishHostOnly(env, reqDigest, refused, () => undefined);
    }
    return this.#finishHostOnly(env, reqDigest, okResponse(base, ack.result), () => {
      this.#ctx.storage.sql.exec(
        "UPDATE transfers SET ack = ? WHERE transfer_id = ?",
        JSON.stringify(ack.result),
        transferId,
      );
    });
  }

  // 受け取り側: pending で保存して ACK を返す（まだ使えない）。journal に入れず、transferId で冪等にする。
  // ロックを取らない（送り元の Host がロックを持ったまま呼ぶので、向きが逆の転送と行き詰まらないように）。
  // await は検査の前にだけ置き、検査と保存は 1 つの同期のトランザクションで行う。
  async #accept(env: Envelope): Promise<ResponseEnvelope> {
    const p = env.payload;
    const transferId = p["transferId"];
    const from = p["from"];
    const rawBundles = p["bundles"];
    if (
      typeof transferId !== "string" ||
      !TRANSFER_ID_PATTERN.test(transferId) ||
      typeof from !== "string" ||
      !ACTOR_PATTERN.test(from) ||
      !Array.isArray(rawBundles) ||
      rawBundles.length < 1
    ) {
      fail("invalid", "accept payload");
    }
    if (rawBundles.length > this.#limits.maxLetters) fail("too_large", "too many bundles");
    const contact = await this.#checkTransferContact(p["contact"], from);
    const bundles: { letterId: number; bundle: string; digest: string; size: number }[] = [];
    for (const raw of rawBundles as unknown[]) {
      if (!isObject(raw) || !isLetterId(raw["letterId"]) || typeof raw["bundle"] !== "string") {
        fail("invalid", "bundle");
      }
      const bytes = decodeBase64(raw["bundle"]);
      if (bytes === null) fail("invalid", "bundle base64");
      if (bytes.byteLength > this.#limits.maxBundleBytes) fail("too_large", "bundle");
      const digest = await sha256Hex(bytes);
      if (raw["digest"] !== digest) fail("invalid", "bundle digest");
      bundles.push({
        letterId: raw["letterId"],
        bundle: raw["bundle"],
        digest,
        size: bytes.byteLength,
      });
    }
    const letterIds = bundles.map((b) => b.letterId);
    if (new Set(letterIds).size !== letterIds.length) fail("invalid", "duplicate letter");
    const digests = bundles.map((b) => ({ letterId: b.letterId, digest: b.digest }));
    const ack = { transferId, digests };
    const base = this.#base(env);
    return this.#ctx.storage.transactionSync(() => {
      if (this.#contactState(contact.contactId) === "closed") fail("contact_closed");
      const existing = this.#transfer(transferId);
      if (existing !== null) {
        if (
          existing.role !== "dest" ||
          existing.peer !== from ||
          JSON.stringify(existing.digests) !== JSON.stringify(digests)
        ) {
          fail("conflict", "transfer differs");
        }
        if (existing.state === "aborted") fail("conflict", "transfer aborted");
        return okResponse(base, ack);
      }
      for (const id of letterIds) {
        const b = this.#bundle(id);
        if (b !== null && b.state !== "gone") fail("conflict", "letter already here");
      }
      this.#ctx.storage.sql.exec(
        `INSERT INTO transfers (transfer_id, role, peer, contact_id, letter_ids, digests, state, ack)
         VALUES (?, 'dest', ?, ?, ?, ?, 'pending', ?)`,
        transferId,
        from,
        contact.contactId,
        JSON.stringify(letterIds),
        JSON.stringify(digests),
        JSON.stringify(ack),
      );
      const place = roleOf(this.#actor()) === "ferry" ? "cargo" : "port";
      for (const b of bundles)
        this.#putBundle({ ...b, state: "pending", place, transferId }, env.eventSeq);
      this.#recount();
      return okResponse(base, ack);
    });
  }

  // 受け取り側: committed の後に then（deliver・cargo.add・unload）を Container に送る。
  // journal の payload には、Container に送る bytes を埋めた then を入れる（再生はこれだけを使う）。
  async #activate(
    env: Envelope,
    reqDigest: string,
    row: JournalRow | null,
  ): Promise<ResponseEnvelope> {
    let payload: JsonObject;
    if (row !== null) {
      payload = JSON.parse(row.payload) as JsonObject;
    } else {
      const { transferId, then } = parseThen(env.payload, ACTIVATE_THEN);
      const transfer = this.#transfer(transferId);
      if (transfer?.role !== "dest" || transfer.state !== "pending")
        fail("invalid", "transfer not pending");
      const letters = Array.isArray(then.payload["letters"])
        ? (then.payload["letters"] as unknown[])
        : [];
      const filled: JsonObject[] = [];
      for (const letter of letters) {
        if (!isObject(letter) || !isLetterId(letter["letterId"])) fail("invalid", "letters");
        const b = this.#bundle(letter["letterId"]);
        if (b?.transferId !== transferId || b.state !== "pending" || b.bundle === null) {
          fail("invalid", "unknown letter");
        }
        if (letter["digest"] !== undefined && letter["digest"] !== b.digest)
          fail("invalid", "digest");
        filled.push({ ...letter, digest: b.digest, bundle: b.bundle });
      }
      if (
        !sameIds(
          filled.map((l) => l["letterId"] as number),
          transfer.letterIds,
        )
      ) {
        fail("invalid", "letters differ from transfer");
      }
      payload = {
        ...env.payload,
        then: { kind: then.kind, payload: { ...then.payload, letters: filled } },
      };
      this.#writeIntent(env, reqDigest, payload);
    }
    this.#at("afterIntent");
    const { transferId, then } = parseThen(payload, ACTIVATE_THEN);
    return this.#runContainer(env, { ...env, kind: then.kind, payload: then.payload }, () => {
      this.#ctx.storage.sql.exec(
        "UPDATE transfers SET state = 'activated' WHERE transfer_id = ?",
        transferId,
      );
    });
  }

  // 送り元: then（port.remove・relay.remove・cargo.remove）を Container に送り、写しを消す（collected）。
  async #gc(env: Envelope, reqDigest: string, row: JournalRow | null): Promise<ResponseEnvelope> {
    const { transferId, then } = parseThen(env.payload, GC_THEN);
    if (row === null) {
      const transfer = this.#transfer(transferId);
      if (transfer?.role !== "source" || transfer.state !== "prepared" || transfer.ack === null) {
        fail("invalid", "transfer not acknowledged");
      }
      if (!sameIds(letterIdsOf(then.payload), transfer.letterIds))
        fail("invalid", "letters differ from transfer");
      this.#writeIntent(env, reqDigest, env.payload);
    }
    this.#at("afterIntent");
    return this.#runContainer(env, { ...env, kind: then.kind, payload: then.payload }, () => {
      this.#ctx.storage.sql.exec(
        "UPDATE transfers SET state = 'collected' WHERE transfer_id = ?",
        transferId,
      );
    });
  }

  // committed の前だけ取り消せる。知らない transferId は何もしない。
  async #abort(env: Envelope, reqDigest: string): Promise<ResponseEnvelope> {
    const transferId = env.payload["transferId"];
    if (typeof transferId !== "string" || !TRANSFER_ID_PATTERN.test(transferId)) fail("invalid");
    const transfer = this.#transfer(transferId);
    const base = this.#base(env);
    if (
      transfer !== null &&
      transfer.state !== "prepared" &&
      transfer.state !== "pending" &&
      transfer.state !== "aborted"
    ) {
      return this.#finishHostOnly(
        env,
        reqDigest,
        errorResponse(base, "invalid", "already committed"),
        () => undefined,
      );
    }
    return this.#finishHostOnly(
      env,
      reqDigest,
      okResponse(base, { transferId, state: "aborted" }),
      () => {
        if (transfer === null || transfer.state === "aborted") return;
        this.#ctx.storage.sql.exec(
          "UPDATE transfers SET state = 'aborted' WHERE transfer_id = ?",
          transferId,
        );
        if (transfer.role === "source") {
          this.#ctx.storage.sql.exec(
            "UPDATE bundles SET state = 'held', transfer_id = NULL, updated_seq = ? WHERE transfer_id = ? AND state = 'prepared'",
            env.eventSeq,
            transferId,
          );
        } else {
          this.#ctx.storage.sql.exec(
            "DELETE FROM bundles WHERE transfer_id = ? AND state = 'pending'",
            transferId,
          );
        }
      },
    );
  }

  // ---- 復元（§4.1）----

  #restore(): Promise<void> {
    this.#restoring ??= this.#runRestore().finally(() => {
      this.#restoring = null;
    });
    return this.#restoring;
  }

  async #runRestore(): Promise<void> {
    if (this.#state !== "lost") return;
    this.#state = "restoring";
    const startedAt = this.#deps.now();
    const sessionId = this.#meta("sessionId") ?? "";
    const starts = Number(this.#meta("starts") ?? "0");
    // 1 ゲームの復元の合計は DtnSession が数える（6 回を超えたら dtn_actor_lost）
    let permitted = false;
    if (starts < MAX_CONTAINER_STARTS) {
      try {
        const result = await this.#deps.callSession(sessionId, "/restore-permit", {
          actor: this.#actor(),
          starts: starts + 1,
        });
        permitted = result.status === 200;
      } catch (error) {
        this.#log("error", "host.error", { op: "restore_permit", error: errorName(error) });
      }
    }
    if (!permitted) {
      this.#giveUp("not_ready", "restore_denied");
      return;
    }
    const container = this.#ctx.container;
    if (container?.running === true) {
      try {
        await container.destroy();
      } catch (error) {
        this.#log("error", "host.error", { op: "destroy", error: errorName(error) });
        this.#giveUp("not_ready", "destroy_failed");
        return;
      }
    }
    try {
      await this.#bootContainer();
    } catch (error) {
      this.#log("error", "host.error", { op: "restore_start", error: errorName(error) });
      this.#giveUp("not_ready", "restore_start_failed");
      return;
    }
    this.#state = "restoring";
    const rows = this.#ctx.storage.sql
      .exec("SELECT * FROM journal WHERE status = 'applied' ORDER BY event_seq")
      .toArray()
      .map((row) => this.#toJournalRow(row));
    let replayed = 0;
    for (const row of rows) {
      const request = this.#replayRequest(row);
      if (request === null) continue;
      if (this.#deps.now() - startedAt > MAX_RESTORE_MS) {
        this.#giveUp("not_ready", "restore_timeout");
        return;
      }
      let response: ResponseEnvelope;
      try {
        const channel = this.#channel;
        if (channel === null) throw new ChannelLost("closed");
        response = parseResponse(
          await channel.request({ ...request, epoch: channel.epoch, replay: true }),
        );
      } catch (error) {
        this.#log("error", "host.error", { op: "replay", error: errorName(error) });
        this.#giveUp("not_ready", "replay_lost");
        return;
      }
      const digest = await digestOf(responseDigestBody(response));
      if (digest !== row.respDigest) {
        this.#giveUp("restore_failed", "digest_mismatch", row.eventSeq);
        return;
      }
      replayed += 1;
    }
    const elapsed = this.#deps.now() - startedAt;
    this.#setMeta("restoreMs", Number(this.#meta("restoreMs") ?? "0") + elapsed);
    // 最後の intent の行は、Coordinator が同じ eventSeq で送り直したときに §3.1 の 4 から続ける
    this.#state = "running";
    this.#log("info", "host.restored", { replayed, restoreMs: elapsed });
  }

  // journal の applied の行から、再生で Container に送る要求を作る（Host だけの種類は null）。
  // エンベロープの残りは列と meta から作る（§7.1）。
  #replayRequest(row: JournalRow): Envelope | null {
    const payload = JSON.parse(row.payload) as JsonObject;
    const base = {
      v: PROTOCOL_VERSION,
      sessionId: this.#meta("sessionId") ?? "",
      actor: this.#actor(),
      epoch: this.#epoch(),
      coordEpoch: Number(this.#meta("maxCoordEpoch") ?? "0"),
      eventSeq: row.eventSeq,
    };
    if (CONTAINER_KINDS.has(row.kind)) return { ...base, kind: row.kind, payload };
    if (row.kind === "transfer.activate" || row.kind === "transfer.gc") {
      const response = JSON.parse(row.response ?? "{}") as { status?: unknown };
      const then = payload["then"];
      if (response.status !== "ok" || !isObject(then) || !isObject(then["payload"])) return null;
      return { ...base, kind: String(then["kind"]), payload: then["payload"] };
    }
    return null;
  }

  #giveUp(code: ErrorCode, cause: string, eventSeq?: number): void {
    this.#state = "failed";
    this.#failedCode = code;
    this.#channel?.close(cause);
    this.#channel = null;
    this.#log("error", "host.restore_failed", {
      cause,
      ...(eventSeq === undefined ? {} : { eventSeq }),
    });
  }

  // ---- 終わり・片付け（persistence-and-recovery.md §6・§7.2・environment.md §4）----

  // 要求の shutdown（§7.2）: Container に shutdown を送り（応答が無くても続ける）、止めてから保存を消す。
  async #shutdownKind(env: Envelope): Promise<ResponseEnvelope> {
    const channel = this.#channel;
    if (channel !== null && this.#state === "running") {
      try {
        await channel.request({ ...env, epoch: this.#epoch() });
      } catch {
        // 応答が無くても続ける（失ったとはみなさない）
      }
    }
    const base = this.#base(env);
    return (await this.#shutdown())
      ? okResponse(base)
      : errorResponse(base, "internal", "destroy failed");
  }

  async #shutdownRequest(text: string): Promise<Response> {
    let body: unknown;
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      return json(400, { code: "invalid" });
    }
    const stored = this.#meta("sessionId");
    if (!isObject(body) || typeof body["sessionId"] !== "string")
      return json(400, { code: "invalid" });
    if (stored === null) return json(200, {}); // 何も持っていない（起動していない・消した後）
    if (stored !== body["sessionId"]) return json(409, { code: "session_mismatch" });
    return (await this.#shutdown()) ? json(200, {}) : json(500, { code: "destroy_failed" });
  }

  // Container を止めてから保存をすべて消す。止められなければ保存を残し、アラームで再試行する。何度呼んでもよい。
  async #shutdown(): Promise<boolean> {
    if (this.#meta("sessionId") === null) return true;
    this.#setMeta("shutdownPending", 1);
    this.#state = "stopping";
    this.#watchGen += 1;
    this.#channel?.close("shutdown");
    this.#channel = null;
    const container = this.#ctx.container;
    if (container !== undefined && (container.running || this.#meta("containerUp") === "1")) {
      try {
        await container.destroy();
      } catch (error) {
        this.#log("error", "host.error", { op: "destroy", error: errorName(error) });
        await this.#scheduleRetry();
        return false;
      }
    }
    this.#setMeta("containerUp", 0);
    this.#log("info", "host.shutdown", {});
    try {
      await this.#ctx.storage.deleteAlarm();
    } catch (error) {
      this.#log("error", "host.error", { op: "deleteAlarm", error: errorName(error) });
    }
    await this.#ctx.storage.deleteAll();
    ensureHostSchema(this.#ctx.storage.sql);
    this.#state = "deleted";
    return true;
  }

  async #scheduleRetry(): Promise<void> {
    if (!(await this.#setAlarm(this.#deps.now() + CLEANUP_RETRY_MS))) {
      // 予約のまま残る（最悪の費用で数えられる）
      this.#log("error", "host.error", { op: "destroy", error: "retry_exhausted" });
    }
  }

  // アラームの回数は maxAlarms の中（ADR-0018）。数を先に保存する。
  async #setAlarm(at: number): Promise<boolean> {
    const count = Number(this.#meta("alarmCount") ?? "0");
    const max = Number(this.#meta("maxAlarms") ?? "0");
    if (count >= max) return false;
    this.#setMeta("alarmCount", count + 1);
    try {
      await this.#ctx.storage.setAlarm(at);
      return true;
    } catch (error) {
      this.#log("error", "host.error", { op: "setAlarm", error: errorName(error) });
      return false;
    }
  }
}

// ---- 補助 ----

function sameIds(a: readonly number[], b: readonly number[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((id) => set.has(id));
}

function letterIdsOf(payload: JsonObject): number[] {
  const raw = payload["letterIds"];
  return Array.isArray(raw) ? (raw as unknown[]).filter(isLetterId) : [];
}

function parseThen(
  payload: JsonObject,
  allowed: ReadonlySet<string>,
): { transferId: string; then: { kind: string; payload: JsonObject } } {
  const transferId = payload["transferId"];
  const then = payload["then"];
  if (typeof transferId !== "string" || !TRANSFER_ID_PATTERN.test(transferId))
    fail("invalid", "transferId");
  if (
    !isObject(then) ||
    typeof then["kind"] !== "string" ||
    !allowed.has(then["kind"]) ||
    !isObject(then["payload"])
  ) {
    fail("invalid", "then");
  }
  return { transferId, then: { kind: then["kind"], payload: then["payload"] } };
}

// 受け取る Host の応答（ACK）。読めなければ null。
function parseAck(
  result: CallResult,
): { status: "ok"; result: unknown } | { status: "error"; code: string } | null {
  if (result.status !== 200 || !isObject(result.body)) return null;
  const body = result.body;
  if (body["status"] === "ok") return { status: "ok", result: body["result"] ?? null };
  if (body["status"] === "error" && isObject(body["error"])) {
    return { status: "error", code: String(body["error"]["code"]) };
  }
  return null;
}
