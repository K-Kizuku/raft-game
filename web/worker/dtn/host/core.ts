// ActorHost の中身（DTN-12・docs/dtn/persistence-and-recovery.md・actor-protocol.md・handoff-and-contact.md）。
//
// 1 Actor に 1 つ（idFromName("<sessionId>/<actor>")）。正本は ctx.storage.sql（schema.ts）。Container の
// メモリとディスクは正本にしない。Go の SQLite adapter（DTN-07）と同じ契約（同じ JSON の fixture を通す）。
//
//   POST /start      {sessionId, actor, deadlineAt, maxAlarms}  → 200 {epoch}・409 session_mismatch・503 start_failed
//   POST /host       要求のエンベロープ（§2.1）                 → 200 応答のエンベロープ（§2.2。エラーも 200）
//   POST /cla/accept transfer.accept のエンベロープ（RPC CLA）   → 200 応答のエンベロープ
//   POST /shutdown   {sessionId}                                → 200 {}・500 destroy_failed（アラームで再試行）
//   alarm()          自分の期限で Container を止めて保存を消す（DtnSession が止まっても Container を残さない）
//
// 確定の順序（§3.1）: エンベロープの確認 → 冪等の確認 → journal に intent → Container に送る →
// 1 つのトランザクションで bundles・then の効果・journal の applied → 応答（bundle の bytes を除く）。
// Host の確定の前に、応答を返さない。

import type { ContainerInstance } from "../../admin/guards.ts";
import type { SqlValue } from "../../admin/cf-types.ts";
import { decodeBase64, digestOf, sha256Hex } from "../codec.ts";
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
  EPOCH_FREE,
  errorResponse,
  exceedsMessageLimit,
  GC_THEN,
  HOST_KINDS,
  isLetterId,
  isObject,
  isSafeNonNegative,
  MAX_ACTOR_STORAGE_BYTES,
  MAX_BUNDLE_BYTES,
  MAX_CONTAINER_STARTS,
  MAX_JOURNAL_ROWS,
  MAX_LETTERS_PER_CALL,
  MAX_RESTORE_MS,
  NOT_JOURNALED,
  okResponse,
  outputBundle,
  outputLetterId,
  parseEnvelope,
  parseLetterIds,
  ProtocolError,
  requestDigestBody,
  responseDigestBody,
  roleOf,
  SESSION_ID_PATTERN,
  stripBundles,
  TRANSFER_ID_PATTERN,
  type Envelope,
  type ErrorCode,
  type JsonObject,
  type ResponseEnvelope,
} from "../protocol.ts";
import { ActorChannel, ChannelLost, type SetTimer } from "./channel.ts";
import { ensureHostSchema, HOST_SCHEMA_VERSION } from "./schema.ts";

export const DEFAULT_ACTOR_START_TIMEOUT_MS = 60_000;

export interface ActorHostDeps extends LinkDeps {
  readonly log: (line: string) => void;
  readonly setTimer: SetTimer;
  readonly instance: ContainerInstance;
  readonly contactKey: string | undefined; // DTN_CONTACT_KEY（なければ Contact を使う要求はすべて断る）
  readonly startTimeoutMs: number;
  readonly responseTimeoutMs: number;
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
  "idle" | "starting" | "running" | "lost" | "restoring" | "failed" | "stopping" | "deleted";

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
  readonly bundle: string | null;
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

// 応答をつなげた advance の行で、最後の要求の outputs が始まる位置（persistence-and-recovery.md §2 の追記）。
const OUTPUTS_FROM = "outputsFrom";

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

export class ActorHostCore {
  readonly #ctx: DtnStateLike;
  readonly #deps: ActorHostDeps;
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
    return this.#meta("actor") ?? "coordinator";
  }

  #base(eventSeq: number): Base {
    return { actor: this.#actor(), epoch: this.#epoch(), eventSeq };
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
    if (
      typeof sessionId !== "string" ||
      !SESSION_ID_PATTERN.test(sessionId) ||
      typeof actor !== "string" ||
      !ACTOR_PATTERN.test(actor) ||
      actor === "coordinator" ||
      !isSafeNonNegative(deadlineAt) ||
      !isSafeNonNegative(maxAlarms) ||
      maxAlarms < 1
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
        this.#setMeta("epoch", 0);
        this.#setMeta("maxCoordEpoch", 0);
        this.#setMeta("createdAt", this.#deps.now());
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
    const watchGen = ++this.#watchGen;
    let exit: Promise<void>;
    try {
      exit = container.monitor();
    } catch {
      exit = Promise.resolve();
    }
    void exit.then(
      () => this.#onContainerExit(watchGen),
      () => this.#onContainerExit(watchGen),
    );
    const ready = await waitHealthy(
      container,
      startedAt + this.#deps.startTimeoutMs,
      this.#deps,
      () => watchGen !== this.#watchGen,
    );
    if (ready !== "ready") throw new Error(`container not ready: ${ready}`);
    const socket = await connectSocket(container);
    if (socket === null) throw new Error("container connect failed");
    this.#channel = new ActorChannel(socket, epoch, {
      setTimer: this.#deps.setTimer,
      timeoutMs: this.#deps.responseTimeoutMs,
      onLost: (cause) => this.#onLost(cause),
    });
    this.#log("info", "container.ready", { startMs: this.#deps.now() - startedAt });
    return epoch;
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

  // ---- /host・/cla/accept ----

  async #hostText(text: string, cla: boolean): Promise<ResponseEnvelope> {
    const unknownBase = this.#base(0);
    if (exceedsMessageLimit(text)) return errorResponse(unknownBase, "too_large");
    let env: Envelope;
    try {
      env = parseEnvelope(JSON.parse(text) as unknown);
    } catch (error) {
      return errorResponse(
        unknownBase,
        error instanceof ProtocolError ? error.code : "invalid",
        "malformed envelope",
      );
    }
    try {
      if (cla !== (env.kind === "transfer.accept")) fail("invalid", "kind not allowed here");
      return await this.#handle(env);
    } catch (error) {
      if (error instanceof ProtocolError) {
        return errorResponse(this.#base(env.eventSeq), error.code, error.message);
      }
      this.#log("error", "host.error", { op: "handle", kind: env.kind, error: errorName(error) });
      return errorResponse(this.#base(env.eventSeq), "internal");
    }
  }

  // 版・セッション・宛先・coordEpoch・epoch の確認（§2.1・persistence-and-recovery.md §5）。
  #checkEnvelope(env: Envelope): void {
    const sessionId = this.#meta("sessionId");
    if (sessionId === null || env.sessionId !== sessionId) fail("session_mismatch");
    if (env.actor !== this.#actor()) fail("unknown_actor");
    const maxCoordEpoch = Number(this.#meta("maxCoordEpoch") ?? "0");
    if (env.coordEpoch < maxCoordEpoch) fail("stale_coord_epoch");
    if (env.coordEpoch > maxCoordEpoch) this.#setMeta("maxCoordEpoch", env.coordEpoch);
    if (!EPOCH_FREE.has(env.kind)) {
      const epoch = this.#epoch();
      if (env.epoch < epoch) fail("stale_epoch");
      if (env.epoch > epoch) fail("invalid", "epoch from the future");
    }
  }

  async #handle(env: Envelope): Promise<ResponseEnvelope> {
    if (env.kind === "transfer.accept") {
      this.#checkEnvelope(env);
      return this.#accept(env);
    }
    if (!CONTAINER_KINDS.has(env.kind) && !HOST_KINDS.has(env.kind))
      fail("invalid", "unknown kind");
    this.#checkEnvelope(env);
    if (env.kind === "host.status") return okResponse(this.#base(env.eventSeq), this.#status());
    if (env.kind === "transfer.status") {
      return okResponse(this.#base(env.eventSeq), this.#transferStatus(env.payload));
    }
    if (this.#meta("shutdownPending") === "1") fail("not_ready", "shutting down");
    if (NOT_JOURNALED.has(env.kind)) {
      return this.#lock(async () => {
        this.#requireRunning();
        return stripBundles(await this.#send(env));
      });
    }
    const reqDigest = await digestOf(requestDigestBody(env));
    return this.#lock(() => this.#journaled(env, reqDigest));
  }

  #requireRunning(): void {
    if (this.#state === "failed") fail(this.#failedCode);
    if (this.#state !== "running" || this.#channel === null) fail("not_ready");
  }

  async #journaled(env: Envelope, reqDigest: string): Promise<ResponseEnvelope> {
    const row = this.#journalRow(env.eventSeq);
    if (row !== null) {
      if (row.reqDigest !== reqDigest) fail("conflict");
      if (row.status === "applied") return this.#storedResponse(row);
    } else {
      const last = this.#lastEventSeq();
      if (last !== null && env.eventSeq < last) fail("stale_event");
    }
    // contact.* と transfer.abort は Container を使わない（復元の途中でも受ける）
    if (env.kind === "contact.open" || env.kind === "contact.close") {
      return this.#contact(env, reqDigest);
    }
    if (env.kind === "transfer.abort") return this.#abort(env, reqDigest);
    this.#requireRunning();
    switch (env.kind) {
      case "transfer.prepare":
        return this.#prepare(env, reqDigest, row);
      case "transfer.activate":
        return this.#activate(env, reqDigest, row);
      case "transfer.gc":
        return this.#gc(env, reqDigest, row);
      default:
        if (row === null) this.#writeIntent(env, reqDigest, env);
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

  #lastEventSeq(): number | null {
    const [row] = this.#ctx.storage.sql.exec("SELECT MAX(event_seq) AS m FROM journal").toArray();
    const value = row?.["m"];
    return typeof value === "number" ? value : null;
  }

  #storageStats(): { rows: number; bytes: number } {
    const sql = this.#ctx.storage.sql;
    const [j] = sql
      .exec(
        "SELECT COUNT(*) AS n, COALESCE(SUM(LENGTH(payload) + COALESCE(LENGTH(response), 0)), 0) AS b FROM journal",
      )
      .toArray();
    const [b] = sql
      .exec("SELECT COALESCE(SUM(COALESCE(LENGTH(bundle), 0)), 0) AS b FROM bundles")
      .toArray();
    return { rows: num(j?.["n"]), bytes: num(j?.["b"]) + num(b?.["b"]) };
  }

  // intent の行を書く（§3.1 の 3）。上限（20,000 行・8 MiB）を超えるなら too_large。
  #writeIntent(env: Envelope, reqDigest: string, stored: Envelope): void {
    const payload = JSON.stringify(stored);
    const stats = this.#storageStats();
    if (stats.rows >= MAX_JOURNAL_ROWS || stats.bytes + payload.length > MAX_ACTOR_STORAGE_BYTES) {
      fail("too_large", "actor storage limit");
    }
    this.#ctx.storage.sql.exec(
      `INSERT INTO journal (event_seq, kind, req_digest, payload, status, response, resp_digest, epoch)
       VALUES (?, ?, ?, ?, 'intent', NULL, NULL, ?)`,
      env.eventSeq,
      env.kind,
      reqDigest,
      payload,
      this.#epoch(),
    );
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

  #storedResponse(row: JournalRow): ResponseEnvelope {
    const parsed = JSON.parse(row.response ?? "{}") as Record<string, unknown>;
    const from = parsed[OUTPUTS_FROM];
    delete parsed[OUTPUTS_FROM];
    if (typeof from === "number" && Array.isArray(parsed["outputs"])) {
      parsed["outputs"] = (parsed["outputs"] as unknown[]).slice(from);
    }
    return parsed as unknown as ResponseEnvelope;
  }

  // Host だけで処理した要求を applied で保存して返す（§3.2）。
  async #finishHostOnly(
    env: Envelope,
    reqDigest: string,
    response: ResponseEnvelope,
    effects: () => void,
  ): Promise<ResponseEnvelope> {
    const respDigest = await digestOf(responseDigestBody(response));
    this.#ctx.storage.transactionSync(() => {
      if (this.#journalRow(env.eventSeq) === null) this.#writeIntent(env, reqDigest, env);
      effects();
      this.#markApplied(env.eventSeq, response, respDigest);
    });
    return response;
  }

  // ---- Container ----

  // Container に送って応答を待つ。止まっていたら（ChannelLost）not_ready（intent は残り、復元の後の送り直しで続ける）。
  async #send(request: Envelope): Promise<ResponseEnvelope> {
    const channel = this.#channel;
    if (channel === null) fail("not_ready");
    const sent = { ...request, epoch: channel.epoch };
    let response: ResponseEnvelope;
    try {
      response = await channel.request(sent);
    } catch (error) {
      if (error instanceof ChannelLost) fail("not_ready", "container lost");
      throw error;
    }
    if (response.status === "error" && response.error?.code === "internal") {
      // Actor の中の失敗: Container を作り直して復元する（§6）
      this.#onLost("actor_internal");
      fail("not_ready", "actor internal error");
    }
    return response;
  }

  // §3.1 の 4〜6。request は Container に送る要求、env は受けた要求（journal の行の番号・種類）。
  async #runContainer(
    env: Envelope,
    request: Envelope,
    extra: ((response: ResponseEnvelope) => void) | null,
  ): Promise<ResponseEnvelope> {
    const response = await this.#send(request);
    const bundles = await this.#checkBundles(response);
    let stored: ResponseEnvelope & { [OUTPUTS_FROM]?: number } = stripBundles(response);
    let digestTarget = response;
    let merge: JournalRow | null = null;
    if (env.kind === "advance" && response.status === "ok") {
      merge = this.#mergeCandidate(env.eventSeq);
      if (merge !== null) {
        const prev = JSON.parse(merge.response ?? "{}") as { outputs?: unknown[] };
        const prevOutputs = (prev.outputs ?? []) as ResponseEnvelope["outputs"];
        const outputs = [...prevOutputs, ...stored.outputs];
        stored = { ...stored, outputs, [OUTPUTS_FROM]: prevOutputs.length };
        digestTarget = { ...response, outputs };
      }
    }
    const respDigest = await digestOf(responseDigestBody(digestTarget));
    this.#ctx.storage.transactionSync(() => {
      if (response.status === "ok") {
        this.#applyOutputs(response, bundles, env.eventSeq);
        this.#applyKind(request.kind, request.payload, env.eventSeq);
        extra?.(response);
        if (request.kind === "init") this.#setMeta("initPayload", JSON.stringify(request.payload));
      }
      if (merge !== null) {
        this.#ctx.storage.sql.exec("DELETE FROM journal WHERE event_seq = ?", merge.eventSeq);
      }
      this.#markApplied(env.eventSeq, stored, respDigest);
    });
    return stripBundles(response);
  }

  // 直前の journal の行が applied の advance なら、それをまとめる（persistence-and-recovery.md §2・D-9）。
  #mergeCandidate(eventSeq: number): JournalRow | null {
    const [row] = this.#ctx.storage.sql
      .exec("SELECT * FROM journal WHERE event_seq < ? ORDER BY event_seq DESC LIMIT 1", eventSeq)
      .toArray();
    if (row === undefined) return null;
    const prev = this.#toJournalRow(row);
    if (prev.kind !== "advance" || prev.status !== "applied") return null;
    const response = JSON.parse(prev.response ?? "{}") as { status?: unknown };
    return response.status === "ok" ? prev : null;
  }

  // 応答の Bundle を確かめる（digest の SHA-256・16 KiB）。bytes は解釈しない。
  async #checkBundles(
    response: ResponseEnvelope,
  ): Promise<Map<number, { bundle: string; digest: string; size: number }>> {
    const out = new Map<number, { bundle: string; digest: string; size: number }>();
    for (const output of response.outputs) {
      const bundle = outputBundle(output);
      if (bundle === null) continue;
      const letterId = outputLetterId(output);
      const bytes = decodeBase64(bundle);
      if (letterId === null || bytes === null) fail("invalid", "bundle");
      if (bytes.byteLength > MAX_BUNDLE_BYTES) fail("too_large", "bundle");
      const digest = await sha256Hex(bytes);
      if (output.data["digest"] !== undefined && output.data["digest"] !== digest) {
        fail("invalid", "bundle digest");
      }
      out.set(letterId, { bundle, digest, size: bytes.byteLength });
    }
    return out;
  }

  #upsertBundle(row: BundleRow, seq: number): void {
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

  #setBundleState(
    letterId: number,
    state: string,
    place: string,
    seq: number,
    keepBytes: boolean,
  ): void {
    this.#ctx.storage.sql.exec(
      `UPDATE bundles SET state = ?, place = ?, updated_seq = ?,
         bundle = CASE WHEN ? = 1 THEN bundle ELSE NULL END,
         transfer_id = CASE WHEN ? = 'held' THEN NULL ELSE transfer_id END
       WHERE letter_id = ?`,
      state,
      place,
      seq,
      keepBytes ? 1 : 0,
      state,
      letterId,
    );
  }

  // 出力の効果: 作った手紙の bytes を保存し、捨てた・配達した手紙の bytes を消す。
  #applyOutputs(
    response: ResponseEnvelope,
    bundles: ReadonlyMap<number, { bundle: string; digest: string; size: number }>,
    seq: number,
  ): void {
    for (const output of response.outputs) {
      const letterId = outputLetterId(output);
      if (letterId === null) continue;
      if (output.type === "letter.created") {
        const b = bundles.get(letterId);
        if (b === undefined) continue;
        this.#upsertBundle(
          {
            letterId,
            bundle: b.bundle,
            digest: b.digest,
            size: b.size,
            state: "held",
            place: "port",
            transferId: null,
          },
          seq,
        );
      } else if (output.type === "letter.dropped") {
        this.#setBundleState(letterId, "gone", "dropped", seq, false);
      } else if (output.type === "letter.delivered") {
        this.#setBundleState(letterId, "gone", "delivered", seq, false);
      }
    }
  }

  // 要求の種類ごとの bundles の効果（then の効果を含む）。
  #applyKind(kind: string, payload: JsonObject, seq: number): void {
    if (GC_THEN.has(kind)) {
      for (const id of letterIdsOf(payload)) this.#setBundleState(id, "gone", "moved", seq, false);
      return;
    }
    if (!ACTIVATE_THEN.has(kind)) return;
    const letters = Array.isArray(payload["letters"]) ? (payload["letters"] as unknown[]) : [];
    for (const letter of letters) {
      if (!isObject(letter) || !isLetterId(letter["letterId"])) continue;
      const id = letter["letterId"];
      if (kind === "deliver") this.#setBundleState(id, "gone", "delivered", seq, false);
      else if (kind === "cargo.add") this.#setBundleState(id, "held", "cargo", seq, true);
      else
        this.#setBundleState(id, "held", letter["place"] === "relay" ? "relay" : "port", seq, true);
    }
  }

  // ---- 状態の照会（journal に入れない）----

  #status(): JsonObject {
    const stats = this.#storageStats();
    return {
      epoch: this.#epoch(),
      lastEventSeq: this.#lastEventSeq(),
      maxCoordEpoch: Number(this.#meta("maxCoordEpoch") ?? "0"),
      container: this.#state,
      starts: Number(this.#meta("starts") ?? "0"),
      journalRows: stats.rows,
      storageBytes: stats.bytes,
      restoreMs: Number(this.#meta("restoreMs") ?? "0"),
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

  // ---- Contact（§2）----

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
    const base = this.#base(env.eventSeq);
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

  // §2.2 の 1〜5（署名・セッション・当事者・epoch・閉じていない）。相手の名前を返す。
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
    if (transfer === null) {
      // §2.2 の 6: 対象の手紙をすべて持っていて、別の transfer の途中でない
      for (const id of letterIds) {
        const b = this.#bundle(id);
        if (b === null || b.state !== "held") fail("invalid", "letter not held");
      }
      if (row === null) this.#writeIntent(env, reqDigest, env);
      // Bundle Age と CRC を更新した bytes を Actor から受け取る（bpv7-profile.md §3）
      const exported = await this.#send({
        ...env,
        kind: "bundle.export",
        payload: { t, letterIds },
      });
      if (exported.status !== "ok") {
        return this.#finishHostOnly(
          env,
          reqDigest,
          stripBundles({ ...exported, eventSeq: env.eventSeq }),
          () => undefined,
        );
      }
      const bundles = await this.#checkBundles(exported);
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
          this.#upsertBundle(
            {
              ...current,
              bundle: b.bundle,
              digest: b.digest,
              size: b.size,
              state: "prepared",
              transferId,
            },
            env.eventSeq,
          );
        }
      });
      transfer = this.#transfer(transferId);
    }
    if (transfer === null) fail("internal", "transfer missing");
    const base = this.#base(env.eventSeq);
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
      v: env.v,
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
    let ack: ResponseEnvelope;
    try {
      ack = parseAckResponse(result);
    } catch {
      fail("not_ready", "peer response");
    }
    if (ack.status !== "ok") {
      // 受け取り側が断った（Contact の外など）。prepared のまま（Coordinator が transfer.abort で取り消す）
      const code = ack.error?.code ?? "internal";
      return this.#finishHostOnly(
        env,
        reqDigest,
        { ...errorResponse(base, "invalid"), error: { code, message: ack.error?.message ?? code } },
        () => undefined,
      );
    }
    const ackBody = ack.result ?? null;
    return this.#finishHostOnly(env, reqDigest, okResponse(base, ackBody), () => {
      this.#ctx.storage.sql.exec(
        "UPDATE transfers SET ack = ? WHERE transfer_id = ?",
        JSON.stringify(ackBody),
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
      rawBundles.length < 1 ||
      rawBundles.length > MAX_LETTERS_PER_CALL
    ) {
      fail("invalid", "accept payload");
    }
    const contact = await this.#checkTransferContact(p["contact"], from);
    const bundles: { letterId: number; bundle: string; digest: string; size: number }[] = [];
    for (const raw of rawBundles as unknown[]) {
      if (!isObject(raw) || !isLetterId(raw["letterId"]) || typeof raw["bundle"] !== "string") {
        fail("invalid", "bundle");
      }
      const bytes = decodeBase64(raw["bundle"]);
      if (bytes === null) fail("invalid", "bundle base64");
      if (bytes.byteLength > MAX_BUNDLE_BYTES) fail("too_large", "bundle");
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
    const base = this.#base(env.eventSeq);
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
      const stats = this.#storageStats();
      const adding = bundles.reduce((sum, b) => sum + b.bundle.length, 0);
      if (stats.bytes + adding > MAX_ACTOR_STORAGE_BYTES) fail("too_large", "actor storage limit");
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
      for (const b of bundles) {
        this.#upsertBundle({ ...b, state: "pending", place, transferId }, env.eventSeq);
      }
      return okResponse(base, ack);
    });
  }

  // 受け取り側: committed の後に then（deliver・cargo.add・unload）を Container に送る。
  async #activate(
    env: Envelope,
    reqDigest: string,
    row: JournalRow | null,
  ): Promise<ResponseEnvelope> {
    let stored: Envelope;
    if (row !== null) {
      stored = JSON.parse(row.payload) as Envelope;
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
      // journal の payload には Container に送る bytes も入れる（再生はこれだけを使う）
      stored = {
        ...env,
        payload: {
          ...env.payload,
          then: { kind: then.kind, payload: { ...then.payload, letters: filled } },
        },
      };
      this.#writeIntent(env, reqDigest, stored);
    }
    const { transferId, then } = parseThen(stored.payload, ACTIVATE_THEN);
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
      this.#writeIntent(env, reqDigest, env);
    }
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
    const base = this.#base(env.eventSeq);
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
      const request = replayRequest(row);
      if (request === null) continue;
      if (this.#deps.now() - startedAt > MAX_RESTORE_MS) {
        this.#giveUp("not_ready", "restore_timeout");
        return;
      }
      let response: ResponseEnvelope;
      try {
        const channel = this.#channel;
        if (channel === null) throw new ChannelLost("closed");
        response = await channel.request({ ...request, epoch: channel.epoch, replay: true });
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

  // ---- 終わり・片付け（persistence-and-recovery.md §6・environment.md §4）----

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

function parseAckResponse(result: CallResult): ResponseEnvelope {
  if (result.status !== 200 || !isObject(result.body)) throw new Error("bad ack");
  const body = result.body;
  if (body["status"] !== "ok" && body["status"] !== "error") throw new Error("bad ack");
  const error = isObject(body["error"]) ? body["error"] : null;
  return {
    v: 1,
    actor: typeof body["actor"] === "string" ? body["actor"] : "",
    epoch: 0,
    eventSeq: 0,
    status: body["status"],
    outputs: [],
    error:
      error === null
        ? null
        : { code: String(error["code"]), message: String(error["message"] ?? "") },
    result: body["result"],
  };
}

// journal の applied の行から、再生で Container に送る要求を作る（Host だけの種類は null）。
function replayRequest(row: JournalRow): Envelope | null {
  const env = JSON.parse(row.payload) as Envelope;
  if (CONTAINER_KINDS.has(row.kind)) return env;
  if (row.kind === "transfer.activate" || row.kind === "transfer.gc") {
    const response = JSON.parse(row.response ?? "{}") as { status?: unknown };
    if (response.status !== "ok") return null;
    const then = (env.payload as { then?: { kind: string; payload: JsonObject } }).then;
    if (then === undefined) return null;
    return { ...env, kind: then.kind, payload: then.payload };
  }
  return null;
}
