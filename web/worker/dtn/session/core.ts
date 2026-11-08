// DtnSession（Session Front）の中身（DTN-12・docs/dtn/environment.md §4・actor-protocol.md §1・§3.3・
// persistence-and-recovery.md §4.2・handoff-and-contact.md §2）。
//
// 1 ゲームに 1 つ（idFromName(sessionId)）。Coordinator の Container（ROLE=coordinator）を起動して /ws に
// つなぎ、Coordinator の dispatch を各 ActorHost の POST /host に並行に配る（ADR-0023 の fetch）。
//
//   POST /init            {sessionId, stageId, seed, demo, maxMinutes, maxAlarms, probe?} → 201・200・400・409・500
//   POST /restore-permit  {actor, starts}  → 200 {}・409 {code}（1 ゲームの合計 6 回・1 Actor の起動 4 回まで）
//   GET  /probe           計測の結果（管理者の試運転の口。environment.md・open-questions.md の Q-*）
//   alarm()               起動の上限（start_timeout）・時間の上限（time_limit）・片付けの再試行
//
// 終わり（どの理由でも）: 全 ActorHost に /shutdown → Coordinator を destroy() → 精算（1 回だけ）。
// 止められなかったものは、アラームで 30 秒ごとに再試行する（maxAlarms の中）。actors の表から何度でもやり直せる（D-8）。

import type { CostGuardClient } from "../../admin/client.ts";
import type { ContainerInstance } from "../../admin/guards.ts";
import type { SqlValue } from "../../admin/cf-types.ts";
import type { DtnStageInfo } from "../../routes/stage-catalog.ts";
import type { CallResult, DtnStateLike, WebSocketLike } from "../cf-types.ts";
import { digestOf, utf8Bytes } from "../codec.ts";
import {
  connectSocket,
  errorName,
  safeClose,
  safeSend,
  startContainer,
  waitHealthy,
  type LinkDeps,
} from "../container-link.ts";
import { createContactSigner, parseContact, type ContactSigner } from "../contact.ts";
import type { HostCaller } from "../namespaces.ts";
import {
  CLEANUP_RETRY_MS,
  CONTAINER_PORT,
  exceedsMessageLimit,
  isObject,
  isSafeNonNegative,
  MAX_CONTAINER_STARTS,
  MAX_COORD_JOURNAL_BYTES,
  MAX_COORD_JOURNAL_ROWS,
  MAX_COORDINATOR_RESTARTS,
  MAX_MESSAGE_BYTES,
  MAX_RESTORES_PER_GAME,
  PROTOCOL_VERSION,
  SESSION_ID_PATTERN,
  type JsonObject,
} from "../protocol.ts";

export const DEFAULT_DTN_START_TIMEOUT_MS = 90_000; // 全体の起動の上限（Q-COLD で見直す）
export const DEFAULT_COORDINATOR_START_TIMEOUT_MS = 60_000;
export const HOST_CALL_ATTEMPTS = 3; // ActorHost の応答が失われたときの送り直し（同じ eventSeq）
export const HOST_DEADLINE_GRACE_MS = 60_000; // ActorHost の自分の期限 = ゲームの期限 + これ
const MAX_STATS_ROWS = 10_000;
// restore.chunk の 1 つの電文の目安（1 MiB の上限から枠の分を引く）
const RESTORE_CHUNK_BYTES = MAX_MESSAGE_BYTES - 16 * 1024;

export type DtnEndReason =
  | "round_ended"
  | "time_limit"
  | "idle"
  | "alarm_cap"
  | "start_timeout"
  | "dtn_actor_lost"
  | "dtn_restore_failed"
  | "dtn_limit"
  | "dtn_internal";

// Coordinator が end で伝えてよい理由（ほかは dtn_internal にする）
const COORDINATOR_END_REASONS: ReadonlySet<string> = new Set([
  "round_ended",
  "dtn_actor_lost",
  "dtn_restore_failed",
  "dtn_limit",
  "dtn_internal",
]);

export interface DtnSessionDeps extends LinkDeps {
  readonly log: (line: string) => void;
  readonly instance: ContainerInstance;
  readonly contactKey: string | undefined;
  readonly startTimeoutMs: number;
  readonly coordinatorStartTimeoutMs: number;
  readonly callHost: HostCaller;
  readonly costGuard: CostGuardClient | undefined;
  readonly catalog: ReadonlyMap<string, DtnStageInfo>;
}

type SessionPhase = "starting" | "ready" | "ending" | "ended";

interface SessionRow {
  sessionId: string;
  stageId: string;
  seed: number;
  demo: boolean;
  probe: { bot: string; speed: number } | null;
  createdAt: number;
  deadlineAt: number;
  startDeadlineAt: number;
  maxAlarms: number;
  alarmCount: number;
  alarmAt: number | null;
  phase: SessionPhase;
  endReason: DtnEndReason | null;
  endedAt: number | null;
  coordEpoch: number;
  coordStarts: number;
  coordUp: boolean;
  restoreCount: number;
  settled: boolean;
  readyAt: number | null;
  stopFailed: boolean;
  result: unknown;
  botStats: unknown;
}

type ActorState = "pending" | "starting" | "started" | "start_failed" | "stopped" | "stop_failed";

interface ActorRow {
  readonly name: string;
  readonly state: ActorState;
  readonly epoch: number;
  readonly startMs: number | null;
}

const SCHEMA: readonly string[] = [
  "CREATE TABLE IF NOT EXISTS session (id INTEGER PRIMARY KEY CHECK (id = 1), body TEXT NOT NULL)",
  `CREATE TABLE IF NOT EXISTS actors (
    name     TEXT PRIMARY KEY,
    ord      INTEGER NOT NULL,
    state    TEXT NOT NULL,
    epoch    INTEGER NOT NULL,
    start_ms INTEGER
  )`,
  // Coordinator の journal（persistence-and-recovery.md §4.2）。entry は restore.* でそのまま送る
  `CREATE TABLE IF NOT EXISTS coord_journal (
    seq   INTEGER PRIMARY KEY AUTOINCREMENT,
    entry TEXT NOT NULL,
    bytes INTEGER NOT NULL
  )`,
  "CREATE TABLE IF NOT EXISTS stats (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, ms INTEGER NOT NULL)",
];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

function percentile(sorted: readonly number[], p: number): number | null {
  if (sorted.length === 0) return null;
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[index] ?? null;
}

interface Call {
  readonly actor: string;
  readonly envelope: Record<string, unknown>;
}

export class DtnSessionCore {
  readonly #ctx: DtnStateLike;
  readonly #deps: DtnSessionDeps;
  readonly #signer: ContactSigner | null;
  #socket: WebSocketLike | null = null;
  #coordGen = 0;
  #cleaning: Promise<void> | null = null;
  readonly #tasks = new Set<Promise<void>>();

  constructor(ctx: DtnStateLike, deps: DtnSessionDeps) {
    this.#ctx = ctx;
    this.#deps = deps;
    this.#signer = deps.contactKey === undefined ? null : createContactSigner(deps.contactKey);
    for (const statement of SCHEMA) ctx.storage.sql.exec(statement);
  }

  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);
    if (pathname === "/probe" && request.method === "GET") return this.#probe();
    if (request.method !== "POST") return json(405, { code: "method_not_allowed" });
    let body: unknown;
    try {
      body = JSON.parse(await request.text()) as unknown;
    } catch {
      return json(400, { code: "bad_request" });
    }
    if (!isObject(body)) return json(400, { code: "bad_request" });
    switch (pathname) {
      case "/init":
        return this.#init(body);
      case "/restore-permit":
        return this.#restorePermit(body);
      default:
        return json(404, { code: "not_found" });
    }
  }

  async alarm(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    this.#update({ alarmAt: null });
    if (row.phase === "ended") return;
    if (row.phase === "ending") {
      await this.#cleanup();
      return;
    }
    const now = this.#deps.now();
    if (row.phase === "starting" && now >= row.startDeadlineAt) {
      await this.#endSession("start_timeout");
      return;
    }
    if (now >= row.deadlineAt) {
      await this.#endSession("time_limit");
      return;
    }
    await this.#scheduleAlarm();
  }

  async drain(): Promise<void> {
    while (this.#tasks.size > 0) await Promise.all([...this.#tasks]);
    if (this.#cleaning !== null) await this.#cleaning;
  }

  // ---- 保存 ----

  #row(): SessionRow | null {
    const [row] = this.#ctx.storage.sql.exec("SELECT body FROM session WHERE id = 1").toArray();
    const body = row?.["body"];
    return typeof body === "string" ? (JSON.parse(body) as SessionRow) : null;
  }

  #update(patch: Partial<SessionRow>): void {
    const row = this.#row();
    if (row === null) return;
    this.#ctx.storage.sql.exec(
      "UPDATE session SET body = ? WHERE id = 1",
      JSON.stringify({ ...row, ...patch }),
    );
  }

  #actors(): ActorRow[] {
    return this.#ctx.storage.sql
      .exec("SELECT * FROM actors ORDER BY ord")
      .toArray()
      .map((row: Record<string, SqlValue>) => ({
        name: String(row["name"]),
        state: String(row["state"]) as ActorState,
        epoch: typeof row["epoch"] === "number" ? row["epoch"] : 0,
        startMs: typeof row["start_ms"] === "number" ? row["start_ms"] : null,
      }));
  }

  #setActor(name: string, patch: { state?: ActorState; epoch?: number; startMs?: number }): void {
    const sql = this.#ctx.storage.sql;
    if (patch.state !== undefined)
      sql.exec("UPDATE actors SET state = ? WHERE name = ?", patch.state, name);
    if (patch.epoch !== undefined)
      sql.exec("UPDATE actors SET epoch = ? WHERE name = ?", patch.epoch, name);
    if (patch.startMs !== undefined) {
      sql.exec("UPDATE actors SET start_ms = ? WHERE name = ?", patch.startMs, name);
    }
  }

  #log(
    level: "info" | "warn" | "error",
    event: string,
    fields: Record<string, unknown> = {},
  ): void {
    const row = this.#row();
    this.#deps.log(
      JSON.stringify({
        level,
        event,
        session: row?.sessionId ?? null,
        coordEpoch: row?.coordEpoch ?? null,
        ...fields,
      }),
    );
  }

  #track(promise: Promise<void>): void {
    const tracked = promise.catch((error: unknown) =>
      this.#log("error", "dtn.error", { op: "async", error: errorName(error) }),
    );
    this.#tasks.add(tracked);
    void tracked.finally(() => this.#tasks.delete(tracked));
  }

  #stat(kind: string, ms: number): void {
    const sql = this.#ctx.storage.sql;
    sql.exec("INSERT INTO stats (kind, ms) VALUES (?, ?)", kind, Math.max(0, Math.round(ms)));
    sql.exec(
      "DELETE FROM stats WHERE id NOT IN (SELECT id FROM stats ORDER BY id DESC LIMIT ?)",
      MAX_STATS_ROWS,
    );
  }

  // ---- /init ----

  async #init(body: JsonObject): Promise<Response> {
    const { sessionId, stageId, seed, demo, maxMinutes, maxAlarms, probe } = body;
    if (
      typeof sessionId !== "string" ||
      !SESSION_ID_PATTERN.test(sessionId) ||
      typeof stageId !== "string" ||
      !isSafeNonNegative(seed) ||
      typeof demo !== "boolean" ||
      !isSafeNonNegative(maxMinutes) ||
      maxMinutes < 1 ||
      maxMinutes > 60 ||
      !isSafeNonNegative(maxAlarms) ||
      maxAlarms < 1 ||
      maxAlarms > 100
    ) {
      return json(400, { code: "bad_request" });
    }
    let parsedProbe: SessionRow["probe"] = null;
    if (probe !== undefined && probe !== null) {
      if (!isObject(probe) || probe["bot"] !== "greedy" || !isSafeNonNegative(probe["speed"])) {
        return json(400, { code: "bad_request" });
      }
      parsedProbe = { bot: "greedy", speed: probe["speed"] };
    }
    // Actor の名前は、目録と署名済みの sessionId からだけ決める（environment.md §1）
    const info = this.#deps.catalog.get(stageId);
    if (info === undefined || info.unsupportedEvents.length > 0) {
      return json(400, { code: "dtn_unsupported_stage" });
    }
    const existing = this.#row();
    if (existing !== null) {
      const same =
        existing.sessionId === sessionId &&
        existing.stageId === stageId &&
        existing.seed === seed &&
        existing.demo === demo;
      return same ? json(200, {}) : json(409, { code: "conflict" });
    }

    const now = this.#deps.now();
    const row: SessionRow = {
      sessionId,
      stageId,
      seed,
      demo,
      probe: parsedProbe,
      createdAt: now,
      deadlineAt: now + maxMinutes * 60_000,
      startDeadlineAt: now + this.#deps.startTimeoutMs,
      maxAlarms,
      alarmCount: 0,
      alarmAt: null,
      phase: "starting",
      endReason: null,
      endedAt: null,
      coordEpoch: 0,
      coordStarts: 0,
      coordUp: false,
      restoreCount: 0,
      settled: false,
      readyAt: null,
      stopFailed: false,
      result: null,
      botStats: null,
    };
    this.#ctx.storage.transactionSync(() => {
      this.#ctx.storage.sql.exec(
        "INSERT INTO session (id, body) VALUES (1, ?)",
        JSON.stringify(row),
      );
      info.actors.forEach((name, ord) => {
        this.#ctx.storage.sql.exec(
          "INSERT INTO actors (name, ord, state, epoch, start_ms) VALUES (?, ?, 'pending', 0, NULL)",
          name,
          ord,
        );
      });
    });
    // アラームを設定してから起動する（今の GameSession と同じ。起動の途中で止まっても片付けが働く）
    if (!(await this.#scheduleAlarm())) {
      this.#update({ phase: "ended", endReason: "start_timeout", endedAt: now });
      return json(500, { code: "start_failed" });
    }
    this.#log("info", "dtn.init", {
      stage: stageId,
      actors: info.actors.length,
      probe: parsedProbe !== null,
    });
    this.#track(this.#startAll());
    return json(201, {});
  }

  // Coordinator と全 ActorHost を並行に起動する。1 つでも失敗したら start_timeout で全停止する。
  async #startAll(): Promise<void> {
    const actors = this.#actors();
    const results = await Promise.allSettled([
      this.#startCoordinator(),
      ...actors.map((actor) => this.#startHost(actor.name)),
    ]);
    const row = this.#row();
    if (row === null || row.phase !== "starting") return;
    if (results.some((result) => result.status === "rejected" || result.value !== true)) {
      await this.#endSession("start_timeout");
      return;
    }
    this.#sendCtl("init", {
      stage: row.stageId,
      stageHash: null, // Coordinator が自分の埋め込みのステージ定義から決める
      seed: row.seed,
      sessionId: row.sessionId,
      coordEpoch: row.coordEpoch,
      demo: row.demo,
      actors: this.#actors().map((actor) => ({ actor: actor.name, epoch: actor.epoch })),
    });
  }

  async #startHost(name: string): Promise<boolean> {
    const row = this.#row();
    if (row === null) return false;
    this.#setActor(name, { state: "starting" });
    const startedAt = this.#deps.now();
    let result: CallResult;
    try {
      result = await this.#deps.callHost(row.sessionId, name, "/start", {
        sessionId: row.sessionId,
        actor: name,
        deadlineAt: row.deadlineAt + HOST_DEADLINE_GRACE_MS,
        maxAlarms: row.maxAlarms,
      });
    } catch (error) {
      this.#log("error", "dtn.error", { op: "host_start", actor: name, error: errorName(error) });
      this.#setActor(name, { state: "start_failed" });
      return false;
    }
    const epoch = isObject(result.body) ? result.body["epoch"] : undefined;
    if (result.status !== 200 || !isSafeNonNegative(epoch)) {
      this.#log("error", "dtn.error", { op: "host_start", actor: name, status: result.status });
      this.#setActor(name, { state: "start_failed" });
      return false;
    }
    const startMs = this.#deps.now() - startedAt;
    this.#setActor(name, { state: "started", epoch, startMs });
    this.#stat("host_start", startMs);
    return true;
  }

  // Coordinator の Container を起動して /ws につなぐ（coordEpoch を 1 増やす）。
  async #startCoordinator(): Promise<boolean> {
    const row = this.#row();
    const container = this.#ctx.container;
    if (row === null || container === undefined) return false;
    if (row.coordStarts >= 1 + MAX_COORDINATOR_RESTARTS) return false;
    const coordEpoch = row.coordEpoch + 1;
    this.#update({ coordEpoch, coordStarts: row.coordStarts + 1, coordUp: true });
    const env: Record<string, string> = { ROLE: "coordinator", PORT: String(CONTAINER_PORT) };
    if (row.probe !== null) {
      // 計測の入口（管理者だけ）: Coordinator が bot で遊ぶ（ブラウザなし）
      env["BOT"] = row.probe.bot;
      env["SPEED"] = String(row.probe.speed);
    }
    if (!startContainer(container, this.#deps.instance, env)) return false;
    const startedAt = this.#deps.now();
    const gen = ++this.#coordGen;
    let exit: Promise<void>;
    try {
      exit = container.monitor();
    } catch {
      exit = Promise.resolve();
    }
    void exit.then(
      () => this.#onCoordinatorLost(gen, "monitor"),
      () => this.#onCoordinatorLost(gen, "monitor"),
    );
    const ready = await waitHealthy(
      container,
      startedAt + this.#deps.coordinatorStartTimeoutMs,
      this.#deps,
      () => gen !== this.#coordGen,
    );
    if (ready !== "ready") return false;
    const socket = await connectSocket(container);
    if (socket === null || gen !== this.#coordGen) return false;
    this.#socket = socket;
    socket.addEventListener("message", (event) => this.#onFrame(gen, event.data));
    socket.addEventListener("close", () => this.#onCoordinatorLost(gen, "socket_closed"));
    socket.addEventListener("error", () => this.#onCoordinatorLost(gen, "socket_error"));
    const startMs = this.#deps.now() - startedAt;
    this.#stat("coordinator_start", startMs);
    this.#log("info", "coordinator.ready", { startMs });
    return true;
  }

  #sendCtl(kind: string, payload: unknown): boolean {
    const socket = this.#socket;
    if (socket === null) return false;
    const text = JSON.stringify({ ch: "ctl", msg: { kind, payload } });
    if (exceedsMessageLimit(text)) {
      this.#track(this.#endSession("dtn_limit"));
      return false;
    }
    return safeSend(socket, text);
  }

  // ---- Coordinator からの電文 ----

  #onFrame(gen: number, data: unknown): void {
    if (gen !== this.#coordGen) return;
    if (typeof data !== "string" || exceedsMessageLimit(data)) {
      this.#track(this.#endSession("dtn_limit"));
      return;
    }
    let frame: unknown;
    try {
      frame = JSON.parse(data) as unknown;
    } catch {
      this.#track(this.#endSession("dtn_internal"));
      return;
    }
    if (!isObject(frame) || !isObject(frame["msg"])) return;
    if (frame["ch"] === "browser") return; // ブラウザの中継は DTN-13
    if (frame["ch"] !== "ctl") return;
    const msg = frame["msg"];
    const payload = isObject(msg["payload"]) ? msg["payload"] : {};
    switch (msg["kind"]) {
      case "dispatch":
        this.#track(this.#onDispatch(gen, payload));
        return;
      case "ready":
        this.#onReady();
        return;
      case "end": {
        const reason = payload["reason"];
        if (payload["result"] !== undefined) this.#update({ result: payload["result"] });
        const end: DtnEndReason =
          typeof reason === "string" && COORDINATOR_END_REASONS.has(reason)
            ? (reason as DtnEndReason)
            : "dtn_internal";
        this.#track(this.#endSession(end));
        return;
      }
      case "journal":
        if (!this.#appendJournal({ type: "note", entry: payload["entry"] ?? null })) {
          this.#track(this.#endSession("dtn_limit"));
        }
        return;
      case "stats":
        this.#update({ botStats: payload });
        return;
      default:
        this.#log("warn", "dtn.unknown_ctl", { kind: String(msg["kind"]) });
    }
  }

  #onReady(): void {
    const row = this.#row();
    if (row === null || row.phase !== "starting") return;
    const now = this.#deps.now();
    this.#update({ phase: "ready", readyAt: now });
    this.#stat("ready", now - row.createdAt);
    this.#log("info", "dtn.ready", { startMs: now - row.createdAt });
    this.#track(this.#scheduleAlarm().then(() => undefined));
  }

  // Coordinator の journal に 1 つ足す（50,000 行・16 MiB まで）。超えたら false。
  #appendJournal(entry: JsonObject): boolean {
    const text = JSON.stringify(entry);
    const bytes = utf8Bytes(text);
    const [stats] = this.#ctx.storage.sql
      .exec("SELECT COUNT(*) AS n, COALESCE(SUM(bytes), 0) AS b FROM coord_journal")
      .toArray();
    const rows = Number(stats?.["n"] ?? 0);
    const total = Number(stats?.["b"] ?? 0);
    if (rows >= MAX_COORD_JOURNAL_ROWS || total + bytes > MAX_COORD_JOURNAL_BYTES) return false;
    this.#ctx.storage.sql.exec(
      "INSERT INTO coord_journal (entry, bytes) VALUES (?, ?)",
      text,
      bytes,
    );
    return true;
  }

  // dispatch{id, calls}: 各 ActorHost の POST /host を並行に呼び、dispatch.result{id, results} を返す。
  // 同じ Actor への要求は eventSeq の順に 1 つずつ（Host の stale_event を起こさないため）。
  async #onDispatch(gen: number, payload: JsonObject): Promise<void> {
    const row = this.#row();
    if (row === null || (row.phase !== "starting" && row.phase !== "ready")) return;
    const id = payload["id"];
    const rawCalls = payload["calls"];
    if (!Array.isArray(rawCalls) || rawCalls.length < 1 || rawCalls.length > 256) {
      await this.#endSession("dtn_internal");
      return;
    }
    const startedAt = this.#deps.now();
    const known = new Set(this.#actors().map((actor) => actor.name));
    const calls: (Call | null)[] = (rawCalls as unknown[]).map((raw) =>
      isObject(raw) && typeof raw["actor"] === "string" && isObject(raw["envelope"])
        ? { actor: raw["actor"], envelope: { ...raw["envelope"] } }
        : null,
    );
    const results: unknown[] = new Array<unknown>(calls.length).fill(null);
    const prepared: { index: number; call: Call }[] = [];
    for (const [index, call] of calls.entries()) {
      const local = await this.#prepareCall(call, known, row);
      if ("error" in local) results[index] = local.error;
      else prepared.push({ index, call: local.call });
    }
    const digests = await Promise.all(
      prepared.map(async ({ call }) => ({
        actor: call.actor,
        eventSeq: call.envelope["eventSeq"],
        kind: call.envelope["kind"],
        reqDigest: await digestOf({
          v: call.envelope["v"],
          sessionId: call.envelope["sessionId"],
          actor: call.envelope["actor"],
          eventSeq: call.envelope["eventSeq"],
          kind: call.envelope["kind"],
          payload: call.envelope["payload"],
        }),
      })),
    );
    if (!this.#appendJournal({ type: "dispatch", id: id ?? null, calls: digests })) {
      await this.#endSession("dtn_limit");
      return;
    }
    // Actor ごとにまとめ、eventSeq の順に並べる
    const groups = new Map<string, { index: number; call: Call }[]>();
    for (const item of prepared) {
      const list = groups.get(item.call.actor) ?? [];
      list.push(item);
      groups.set(item.call.actor, list);
    }
    await Promise.all(
      [...groups.values()].map(async (list) => {
        list.sort(
          (a, b) => Number(a.call.envelope["eventSeq"]) - Number(b.call.envelope["eventSeq"]),
        );
        for (const { index, call } of list)
          results[index] = await this.#callHost(row.sessionId, call);
      }),
    );
    if (gen !== this.#coordGen) return; // 古い Coordinator への応答は送らない（新しい Coordinator が送り直す）
    if (!this.#appendJournal({ type: "dispatch.result", id: id ?? null, results })) {
      await this.#endSession("dtn_limit");
      return;
    }
    const ms = this.#deps.now() - startedAt;
    this.#stat("dispatch", ms);
    if (prepared.some(({ call }) => call.envelope["kind"] === "transfer.prepare"))
      this.#stat("handoff", ms);
    this.#sendCtl("dispatch.result", { id: id ?? null, results });
  }

  // 宛先・セッションの確認と Contact の署名。coordEpoch は Session Front が振った今の値にする。
  async #prepareCall(
    call: Call | null,
    known: ReadonlySet<string>,
    row: SessionRow,
  ): Promise<{ call: Call } | { error: unknown }> {
    const base = (code: string) => ({
      v: PROTOCOL_VERSION,
      actor: call?.actor ?? "",
      epoch: 0,
      eventSeq: isSafeNonNegative(call?.envelope["eventSeq"]) ? call.envelope["eventSeq"] : 0,
      status: "error",
      outputs: [],
      error: { code, message: code },
    });
    if (call === null) return { error: base("invalid") };
    if (!known.has(call.actor) || call.envelope["actor"] !== call.actor) {
      this.#log("warn", "dtn.unknown_actor", { actor: call.actor });
      return { error: base("unknown_actor") };
    }
    if (call.envelope["sessionId"] !== row.sessionId) return { error: base("session_mismatch") };
    const envelope: Record<string, unknown> = { ...call.envelope, coordEpoch: row.coordEpoch };
    const payload = envelope["payload"];
    if (isObject(payload) && payload["contact"] !== undefined) {
      const signed = await this.#signContact(payload["contact"], known, row);
      if (signed === null) return { error: base("contact_mismatch") };
      envelope["payload"] = { ...payload, contact: signed };
    }
    return { call: { actor: call.actor, envelope } };
  }

  // Contact に DTN_CONTACT_KEY で署名する（handoff-and-contact.md §2.1）。このセッションの島と船の組だけ。
  async #signContact(raw: unknown, known: ReadonlySet<string>, row: SessionRow): Promise<unknown> {
    if (this.#signer === null) return null;
    let contact;
    try {
      contact = parseContact(isObject(raw) ? { ...raw, sig: undefined } : raw);
    } catch {
      return null;
    }
    if (
      contact.sessionId !== row.sessionId ||
      !known.has(`island:${contact.island}`) ||
      !known.has(`ferry:${contact.ferry}`)
    ) {
      return null;
    }
    return this.#signer.sign(contact);
  }

  // POST /host。応答が失われたら同じ envelope（同じ eventSeq）で送り直す。
  async #callHost(sessionId: string, call: Call): Promise<unknown> {
    for (let attempt = 1; attempt <= HOST_CALL_ATTEMPTS; attempt += 1) {
      try {
        const result = await this.#deps.callHost(sessionId, call.actor, "/host", call.envelope);
        if (result.status === 200 && isObject(result.body)) {
          const epoch = result.body["epoch"];
          if (isSafeNonNegative(epoch) && epoch > 0) this.#setActor(call.actor, { epoch });
          return result.body;
        }
      } catch (error) {
        this.#log("warn", "dtn.host_call_failed", {
          actor: call.actor,
          attempt,
          error: errorName(error),
        });
      }
    }
    // Host に届かない。Coordinator は not_ready として仮想時間を止め、同じ eventSeq で送り直す
    return {
      v: PROTOCOL_VERSION,
      actor: call.actor,
      epoch: 0,
      eventSeq: call.envelope["eventSeq"],
      status: "error",
      outputs: [],
      error: { code: "not_ready", message: "host unreachable" },
    };
  }

  // ---- Coordinator の作り直し（persistence-and-recovery.md §4.2）----

  #onCoordinatorLost(gen: number, cause: string): void {
    if (gen !== this.#coordGen) return;
    this.#coordGen += 1; // 古い Container・ソケットからの電文を捨てる
    const socket = this.#socket;
    this.#socket = null;
    if (socket !== null) safeClose(socket, 1011, cause);
    const row = this.#row();
    if (row === null || row.phase === "ending" || row.phase === "ended") return;
    this.#log("warn", "coordinator.lost", { cause });
    if (row.phase === "starting") {
      // 起動の途中なら、作り直さずに全停止する（起動の待ちは #startAll が見ている）
      this.#track(this.#endSession("start_timeout"));
      return;
    }
    this.#track(this.#restartCoordinator());
  }

  async #restartCoordinator(): Promise<void> {
    const row = this.#row();
    if (row === null || row.phase !== "ready") return;
    if (row.coordStarts >= 1 + MAX_COORDINATOR_RESTARTS) {
      await this.#endSession("dtn_actor_lost");
      return;
    }
    const container = this.#ctx.container;
    if (container?.running === true) {
      try {
        await container.destroy();
      } catch (error) {
        this.#log("error", "dtn.error", { op: "coordinator_destroy", error: errorName(error) });
        await this.#endSession("dtn_actor_lost");
        return;
      }
    }
    if (!(await this.#startCoordinator())) {
      await this.#endSession("dtn_actor_lost");
      return;
    }
    const current = this.#row();
    if (current === null || current.phase !== "ready") return;
    this.#sendRestore(current);
  }

  // journal を 1 MiB に収まるように分けて送る: restore.begin → restore.chunk … → restore.end。
  #sendRestore(row: SessionRow): void {
    const entries = this.#ctx.storage.sql
      .exec("SELECT entry, bytes FROM coord_journal ORDER BY seq")
      .toArray();
    const chunks: unknown[][] = [[]];
    let size = 0;
    for (const item of entries) {
      const bytes = Number(item["bytes"] ?? 0);
      if (bytes > RESTORE_CHUNK_BYTES) {
        this.#track(this.#endSession("dtn_limit"));
        return;
      }
      if (size + bytes > RESTORE_CHUNK_BYTES) {
        chunks.push([]);
        size = 0;
      }
      chunks.at(-1)?.push(JSON.parse(String(item["entry"])) as unknown);
      size += bytes + 1;
    }
    const [first = [], ...rest] = chunks;
    this.#sendCtl("restore.begin", {
      stage: row.stageId,
      stageHash: null,
      seed: row.seed,
      sessionId: row.sessionId,
      coordEpoch: row.coordEpoch,
      actors: this.#actors().map((actor) => ({ actor: actor.name, epoch: actor.epoch })),
      entries: first,
    });
    rest.forEach((chunk, i) => this.#sendCtl("restore.chunk", { index: i + 1, entries: chunk }));
    this.#sendCtl("restore.end", {});
    this.#log("info", "coordinator.restore", { entries: entries.length, chunks: chunks.length });
  }

  // ---- 復元の許可 ----

  #restorePermit(body: JsonObject): Response {
    const row = this.#row();
    const actor = body["actor"];
    const starts = body["starts"];
    if (row === null || typeof actor !== "string" || !isSafeNonNegative(starts)) {
      return json(400, { code: "bad_request" });
    }
    if (!this.#actors().some((a) => a.name === actor)) return json(409, { code: "unknown_actor" });
    if (row.phase === "ending" || row.phase === "ended")
      return json(409, { code: "session_ended" });
    if (row.restoreCount + 1 > MAX_RESTORES_PER_GAME || starts > MAX_CONTAINER_STARTS) {
      this.#log("error", "dtn.restore_denied", { actor, restoreCount: row.restoreCount, starts });
      this.#track(this.#endSession("dtn_actor_lost"));
      return json(409, { code: "dtn_actor_lost" });
    }
    this.#update({ restoreCount: row.restoreCount + 1 });
    this.#log("info", "dtn.restore_permit", { actor, restoreCount: row.restoreCount + 1 });
    return json(200, {});
  }

  // ---- 計測（GET /probe）----

  async #probe(): Promise<Response> {
    const row = this.#row();
    if (row === null) return json(404, { code: "not_found" });
    const sql = this.#ctx.storage.sql;
    const series = (kind: string): number[] =>
      sql
        .exec("SELECT ms FROM stats WHERE kind = ? ORDER BY ms", kind)
        .toArray()
        .map((r) => Number(r["ms"] ?? 0));
    const summary = (kind: string) => {
      const values = series(kind);
      return { count: values.length, p50Ms: percentile(values, 50), p95Ms: percentile(values, 95) };
    };
    const [journal] = sql
      .exec("SELECT COUNT(*) AS n, COALESCE(SUM(bytes), 0) AS b FROM coord_journal")
      .toArray();
    const actors = this.#actors();
    const hosts = await Promise.all(
      actors.map(async (actor) => {
        if (actor.state === "pending" || actor.state === "stopped") {
          return { actor: actor.name, state: actor.state, startMs: actor.startMs };
        }
        try {
          const result = await this.#deps.callHost(row.sessionId, actor.name, "/host", {
            v: PROTOCOL_VERSION,
            sessionId: row.sessionId,
            actor: actor.name,
            epoch: 0,
            coordEpoch: row.coordEpoch,
            eventSeq: 0,
            kind: "host.status",
            payload: {},
          });
          const status = isObject(result.body) ? result.body["result"] : null;
          return { actor: actor.name, state: actor.state, startMs: actor.startMs, status };
        } catch {
          return { actor: actor.name, state: actor.state, startMs: actor.startMs, status: null };
        }
      }),
    );
    return json(200, {
      sessionId: row.sessionId,
      stageId: row.stageId,
      phase: row.phase,
      endReason: row.endReason,
      settled: row.settled,
      startup: {
        readyMs: row.readyAt === null ? null : row.readyAt - row.createdAt,
        coordinator: summary("coordinator_start"),
        hosts: summary("host_start"),
      },
      dispatch: summary("dispatch"),
      handoff: summary("handoff"),
      storage: {
        coordJournalRows: Number(journal?.["n"] ?? 0),
        coordJournalBytes: Number(journal?.["b"] ?? 0),
        hosts,
      },
      restores: row.restoreCount,
      coordinatorStarts: row.coordStarts,
      bot: row.botStats,
      result: row.result,
    });
  }

  // ---- アラーム ----

  #nextAlarmAt(row: SessionRow): number {
    return row.phase === "starting"
      ? Math.min(row.startDeadlineAt, row.deadlineAt)
      : row.deadlineAt;
  }

  // 早いほうにだけ動かす。回数の上限に達したら alarm_cap で終える。
  async #scheduleAlarm(): Promise<boolean> {
    const row = this.#row();
    if (row === null || row.phase === "ended" || row.phase === "ending") return false;
    const at = this.#nextAlarmAt(row);
    if (row.alarmAt !== null && at >= row.alarmAt) return true;
    if (row.alarmCount >= row.maxAlarms) {
      await this.#endSession("alarm_cap");
      return false;
    }
    return this.#setAlarmAt(at);
  }

  async #setAlarmAt(at: number): Promise<boolean> {
    const row = this.#row();
    if (row === null || row.alarmCount >= row.maxAlarms) return false;
    const previous = row.alarmAt;
    this.#update({ alarmCount: row.alarmCount + 1, alarmAt: at });
    try {
      await this.#ctx.storage.setAlarm(at);
      return true;
    } catch (error) {
      this.#log("error", "dtn.error", { op: "setAlarm", error: errorName(error) });
      this.#update({ alarmAt: previous });
      return previous !== null;
    }
  }

  // ---- 終わり（何度呼んでも 1 回だけ動く）----

  async #endSession(reason: DtnEndReason): Promise<void> {
    const row = this.#row();
    if (row === null || row.phase === "ending" || row.phase === "ended") return;
    this.#update({ phase: "ending", endReason: reason, endedAt: this.#deps.now() });
    this.#log("info", "dtn.end", { reason });
    this.#coordGen += 1;
    const socket = this.#socket;
    this.#socket = null;
    if (socket !== null) safeClose(socket, 1000, "session ended");
    await this.#cleanup();
  }

  #cleanup(): Promise<void> {
    this.#cleaning ??= this.#runCleanup().finally(() => {
      this.#cleaning = null;
    });
    return this.#cleaning;
  }

  // 全 ActorHost に /shutdown → Coordinator を destroy() → すべて止まったら精算してアラームを消す。
  async #runCleanup(): Promise<void> {
    const row = this.#row();
    if (row === null || row.phase !== "ending") return;
    const targets = this.#actors().filter(
      (actor) => actor.state !== "pending" && actor.state !== "stopped",
    );
    const hostResults = await Promise.all(
      targets.map(async (actor) => {
        try {
          const result = await this.#deps.callHost(row.sessionId, actor.name, "/shutdown", {
            sessionId: row.sessionId,
          });
          if (result.status === 200) {
            this.#setActor(actor.name, { state: "stopped" });
            return true;
          }
        } catch (error) {
          this.#log("error", "dtn.error", {
            op: "host_shutdown",
            actor: actor.name,
            error: errorName(error),
          });
        }
        this.#setActor(actor.name, { state: "stop_failed" });
        return false;
      }),
    );
    let coordinatorStopped = true;
    const container = this.#ctx.container;
    if (row.coordUp && container !== undefined) {
      try {
        await container.destroy();
        this.#update({ coordUp: false });
      } catch (error) {
        this.#log("error", "dtn.error", { op: "coordinator_destroy", error: errorName(error) });
        coordinatorStopped = false;
      }
    }
    if (!coordinatorStopped || hostResults.includes(false)) {
      this.#update({ stopFailed: true });
      await this.#scheduleCleanupRetry();
      return;
    }
    await this.#settleAndFinish();
  }

  async #scheduleCleanupRetry(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    const retryAt = this.#deps.now() + CLEANUP_RETRY_MS;
    if (row.alarmAt !== null && row.alarmAt <= retryAt) return; // そのアラームでやり直す
    if (await this.#setAlarmAt(retryAt)) return;
    // やり直す手段がない。帳簿は予約のまま（最悪の費用で数えられる）
    this.#log("error", "dtn.error", { op: "cleanup", error: "retry_exhausted" });
  }

  // 全停止を確かめた後でだけ呼ぶ。精算は 1 回だけ（失敗しても予約のまま。安全側）。
  async #settleAndFinish(): Promise<void> {
    const row = this.#row();
    if (row === null) return;
    const runSeconds = Math.min(
      Math.ceil(Math.max(0, this.#deps.now() - row.createdAt) / 1000),
      86_400,
    );
    let settled = row.settled;
    const costGuard = this.#deps.costGuard;
    if (!settled) {
      if (costGuard === undefined) {
        this.#log("error", "dtn.error", { op: "settle", error: "no_cost_guard" });
      } else {
        try {
          await costGuard.settle(row.sessionId, runSeconds);
          settled = true;
        } catch (error) {
          this.#log("error", "dtn.error", { op: "settle", error: errorName(error) });
        }
      }
    }
    try {
      await this.#ctx.storage.deleteAlarm();
    } catch (error) {
      this.#log("error", "dtn.error", { op: "deleteAlarm", error: errorName(error) });
    }
    this.#update({ phase: "ended", settled, alarmAt: null, stopFailed: false });
    this.#log("info", "dtn.ended", { reason: row.endReason, runSeconds, settled });
  }
}
