// Durable Object SpikeSession（SQLite・コンテナ付き）。DurableObject を継承しない（ADR-0023 と同じ fetch のハンドラーの形。M-8）。
//
// 処理の本体は SessionCore（ctx と差し替え口を受け取るクラス）に置く。本番で継承しない形が動かなければ、
// SpikeSession だけを `extends DurableObject` に直せば済む（Issue #134 の H-8）。
//
// 費用の上限: 1 つの Durable Object で起動は START_CAP 回まで。起動ごとに maxRunMs 後のアラームを設定し、
// alarm() ではコンテナを destroy() するだけで、アラームを設定し直さない。setInactivityTimeout も設定する。
import { COLO_HEADER, errorResponse, json, JSON_HEADERS, messageOf } from "./http.ts";
import {
  CONTAINER_PORT,
  CRASH_DETECT_TIMEOUT_MS,
  READY_POLL_INTERVAL_MS,
  SESSION_ACTIONS,
  START_CAP,
  START_PARAMS,
  isSessionAction,
  parseParams,
  type ParamValues,
} from "./limits.ts";
import { newRelayStats, startRelay, type RelayStats } from "./relay.ts";
import { summarize } from "./stats.ts";
import { burn } from "./cpu.ts";
import type { ContainerLike, DurableObjectStateLike, SqlStorageLike, WebSocketLike } from "./types.ts";

/** 差し替え口（テストで時刻・待ち・ログ・WebSocketPair を注入する） */
export interface SessionDeps {
  now(): number;
  sleep(ms: number): Promise<void>;
  log(line: string): void;
  /** [ブラウザに返す側, Durable Object の側] */
  createPair(): [WebSocketLike, WebSocketLike];
  upgradeResponse(client: WebSocketLike): Response;
  /** fetch に渡す期限の signal（テストでは undefined を返してよい） */
  timeoutSignal(ms: number): AbortSignal | undefined;
}

type WebSocketPairCtor = new () => { 0: WebSocketLike; 1: WebSocketLike };

export const defaultSessionDeps: SessionDeps = {
  now: () => Date.now(),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  log: (line) => console.log(line),
  createPair: () => {
    const Pair = (globalThis as unknown as { WebSocketPair: WebSocketPairCtor }).WebSocketPair;
    const pair = new Pair();
    return [pair[0], pair[1]];
  },
  upgradeResponse: (client) => new Response(null, { status: 101, webSocket: client } as ResponseInit),
  timeoutSignal: (ms) => AbortSignal.timeout(Math.max(1, Math.ceil(ms))),
};

export type RunKind = "start" | "ready" | "stop" | "alarm" | "exit" | "crash";

interface StartValues {
  instance: "lite" | "standard-1";
  internet: number;
  maxRunMs: number;
  inactivityMs: number;
  timeoutMs: number;
}

interface ExitWatch {
  done: boolean;
  result: { ok: boolean; error?: string } | null;
  promise: Promise<void>;
}

type StartOutcome = { status: number; body: Record<string, unknown> };

const CONTAINER_ORIGIN = "http://container";

function toStartValues(v: ParamValues): StartValues {
  return {
    instance: v["instance"] === "standard-1" ? "standard-1" : "lite",
    internet: Number(v["internet"]),
    maxRunMs: Number(v["maxRunMs"]),
    inactivityMs: Number(v["inactivityMs"]),
    timeoutMs: Number(v["timeoutMs"]),
  };
}

function outcome(o: StartOutcome): Response {
  return json(o.status, o.body);
}

/** SpikeSession の処理の本体（継承に依存しない） */
export class SessionCore {
  private readonly ctx: DurableObjectStateLike;
  private readonly sql: SqlStorageLike;
  private readonly deps: SessionDeps;
  private exit: ExitWatch | null = null;
  private relay: RelayStats | null = null;

  constructor(ctx: DurableObjectStateLike, deps: SessionDeps) {
    this.ctx = ctx;
    this.sql = ctx.storage.sql;
    this.deps = deps;
    this.sql.exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)");
    this.sql.exec("CREATE TABLE IF NOT EXISTS runs (id INTEGER PRIMARY KEY, kind TEXT, at INTEGER, detail TEXT)");
    this.sql.exec("CREATE TABLE IF NOT EXISTS input_log (seq INTEGER PRIMARY KEY, t INTEGER, cmd TEXT)");
  }

  // ---- SQLite ----

  private metaInt(key: string): number {
    const row = this.sql.exec("SELECT value FROM meta WHERE key = ?", key).toArray()[0];
    const n = Number(row?.["value"] ?? 0);
    return Number.isSafeInteger(n) ? n : 0;
  }

  private metaJson(key: string): unknown {
    const row = this.sql.exec("SELECT value FROM meta WHERE key = ?", key).toArray()[0];
    if (row === undefined) return null;
    try {
      return JSON.parse(String(row["value"]));
    } catch {
      return null;
    }
  }

  private setMeta(key: string, value: string): void {
    this.sql.exec(
      "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
      key,
      value,
    );
  }

  private record(kind: RunKind, detail: Record<string, unknown>): void {
    this.sql.exec("INSERT INTO runs (kind, at, detail) VALUES (?, ?, ?)", kind, this.deps.now(), JSON.stringify(detail));
  }

  private log(event: RunKind, detail: Record<string, unknown>): void {
    this.deps.log(JSON.stringify({ spike: "session", event, ...detail }));
  }

  private count(table: "input_log" | "runs"): number {
    const row = this.sql.exec(`SELECT COUNT(*) AS n FROM ${table}`).toArray()[0];
    return Number(row?.["n"] ?? 0);
  }

  // ---- 入口 ----

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const action = url.pathname.slice(1);
    if (!isSessionAction(action)) return errorResponse(404, "not_found", "not found");
    const spec = SESSION_ACTIONS[action];
    if (request.method !== spec.method) return errorResponse(405, "method_not_allowed", `use ${spec.method}`);
    const parsed = parseParams(url.searchParams, spec.params);
    if (!parsed.ok) return errorResponse(400, "bad_param", parsed.message);
    const v = parsed.values;
    switch (action) {
      case "start":
        return this.start(toStartValues(v), request.headers.get(COLO_HEADER));
      case "stop":
        return this.stop();
      case "status":
        return this.status();
      case "ping":
        return this.ping(Number(v["count"]));
      case "bench/raft":
      case "bench/loop":
      case "egress":
        return this.forward(action, v);
      case "crash":
        return this.crash(Number(v["restart"]) === 1, request.headers.get(COLO_HEADER));
      case "sql":
        return this.appendRows(Number(v["rows"]));
      case "cpu":
        return json(200, { where: "durable-object", ...burn(Number(v["iters"])) });
      case "ws":
        return this.ws(request);
    }
  }

  /** アラーム。動いていれば destroy() する。アラームを設定し直さない */
  async alarm(): Promise<void> {
    const c = this.ctx.container;
    const running = c?.running ?? false;
    let error: string | undefined;
    if (c !== undefined && running) {
      try {
        await c.destroy();
      } catch (e) {
        error = messageOf(e);
      }
    }
    const detail = { running, destroyed: running && error === undefined, ...(error !== undefined ? { error } : {}) };
    this.record("alarm", detail);
    this.log("alarm", detail);
  }

  // ---- 起動 ----

  /** 起動の回数を確かめ、数を増やしてアラームを設定する。上限なら null */
  private async reserveStart(maxRunMs: number): Promise<number | null> {
    const starts = this.metaInt("starts");
    if (starts >= START_CAP) return null;
    this.setMeta("starts", String(starts + 1));
    this.setMeta("alarms", String(this.metaInt("alarms") + 1));
    await this.ctx.storage.setAlarm(this.deps.now() + maxRunMs);
    return starts + 1;
  }

  private async start(v: StartValues, colo: string | null): Promise<Response> {
    const c = this.ctx.container;
    if (c === undefined) return errorResponse(500, "no_container", "ctx.container is not available");
    if (c.running) return errorResponse(409, "already_running", "container is already running");
    const run = await this.reserveStart(v.maxRunMs);
    if (run === null) return errorResponse(429, "start_cap", `a session can start at most ${START_CAP} times`);
    this.setMeta("lastStart", JSON.stringify(v));
    return outcome(await this.boot(c, v, colo, run));
  }

  private watch(c: ContainerLike): void {
    const w: ExitWatch = { done: false, result: null, promise: Promise.resolve() };
    let p: Promise<void>;
    try {
      p = c.monitor();
    } catch (e) {
      p = Promise.reject(e instanceof Error ? e : new Error(String(e)));
    }
    w.promise = p.then(
      () => ({ ok: true }),
      (e: unknown) => ({ ok: false, error: messageOf(e) }),
    ).then((result) => {
      w.done = true;
      w.result = result;
      this.record("exit", result);
      this.log("exit", result);
    });
    this.exit = w;
  }

  private async boot(c: ContainerLike, v: StartValues, colo: string | null, run: number): Promise<StartOutcome> {
    this.record("start", { run, ...v });
    this.log("start", { run, instance: v.instance, internet: v.internet });

    const image = c.images["server"];
    if (image === undefined) {
      this.record("start", { run, error: "start_failed", message: "image 'server' is not defined" });
      return { status: 500, body: { error: "start_failed", message: "image 'server' is not defined" } };
    }
    const t0 = this.deps.now();
    try {
      c.start({
        image,
        instance: v.instance,
        enableInternet: v.internet === 1,
        env: { MODE: "single", PORT: String(CONTAINER_PORT) },
      });
    } catch (e) {
      this.record("start", { run, error: "start_failed", message: messageOf(e) });
      return { status: 500, body: { error: "start_failed", message: messageOf(e) } };
    }
    this.watch(c);

    let inactivityTimeout = "ok";
    try {
      await c.setInactivityTimeout(v.inactivityMs);
    } catch (e) {
      inactivityTimeout = messageOf(e); // 効かない環境（ローカルなど）は結果に書いて続ける
    }

    const port = c.getTcpPort(CONTAINER_PORT);
    let polls = 0;
    for (;;) {
      polls++;
      const left = v.timeoutMs - (this.deps.now() - t0);
      let ok = false;
      try {
        const res = await port.fetch(`${CONTAINER_ORIGIN}/healthz`, { signal: this.deps.timeoutSignal(left) ?? null });
        await res.text();
        ok = res.status === 200;
      } catch {
        ok = false;
      }
      if (ok) break;
      if (this.deps.now() - t0 >= v.timeoutMs) {
        let destroyError: string | undefined;
        try {
          await c.destroy();
        } catch (e) {
          destroyError = messageOf(e);
        }
        const detail = { run, error: "start_timeout", polls, ...(destroyError !== undefined ? { destroyError } : {}) };
        this.record("stop", detail);
        this.log("stop", detail);
        return {
          status: 504,
          body: { error: "start_timeout", message: `not ready within ${v.timeoutMs} ms`, polls },
        };
      }
      await this.deps.sleep(READY_POLL_INTERVAL_MS);
    }
    const startMs = this.deps.now() - t0;

    let info: unknown = null;
    let infoError: string | undefined;
    try {
      const res = await port.fetch(`${CONTAINER_ORIGIN}/info`);
      info = await res.json();
    } catch (e) {
      infoError = messageOf(e);
    }
    this.record("ready", { run, startMs, polls });
    this.log("ready", { run, startMs, polls });
    return {
      status: 200,
      body: {
        instance: v.instance,
        internet: v.internet,
        startMs,
        polls,
        run,
        info,
        ...(infoError !== undefined ? { infoError } : {}),
        colo,
        inactivityTimeout,
      },
    };
  }

  // ---- 停止・状態 ----

  private async stop(): Promise<Response> {
    const c = this.ctx.container;
    if (c === undefined || !c.running) return json(200, { running: false });
    const t0 = this.deps.now();
    try {
      await c.destroy();
    } catch (e) {
      this.record("stop", { error: messageOf(e) });
      this.log("stop", { error: messageOf(e) });
      return errorResponse(500, "stop_failed", messageOf(e));
    }
    const stopMs = this.deps.now() - t0;
    this.record("stop", { stopMs });
    this.log("stop", { stopMs });
    return json(200, { running: c.running, destroyed: true, stopMs });
  }

  private status(): Response {
    const runs = this.sql
      .exec("SELECT id, kind, at, detail FROM runs ORDER BY id DESC LIMIT 100")
      .toArray()
      .reverse()
      .map((r) => {
        let detail: unknown = null;
        try {
          detail = JSON.parse(String(r["detail"]));
        } catch {
          detail = r["detail"];
        }
        return { id: Number(r["id"]), kind: String(r["kind"]), at: Number(r["at"]), detail };
      });
    return json(200, {
      running: this.ctx.container?.running ?? false,
      hasContainer: this.ctx.container !== undefined,
      starts: this.metaInt("starts"),
      alarms: this.metaInt("alarms"),
      inputLogRows: this.count("input_log"),
      runCount: this.count("runs"),
      runs,
      relay: this.relay,
    });
  }

  private running(): ContainerLike | null {
    const c = this.ctx.container;
    return c !== undefined && c.running ? c : null;
  }

  private notRunning(): Response {
    return errorResponse(409, "not_running", "container is not running");
  }

  // ---- コンテナへの要求 ----

  private async ping(count: number): Promise<Response> {
    const c = this.running();
    if (c === null) return this.notRunning();
    const port = c.getTcpPort(CONTAINER_PORT);
    const latencies: number[] = [];
    let ok = 0;
    for (let i = 0; i < count; i++) {
      const t = this.deps.now();
      try {
        const res = await port.fetch(`${CONTAINER_ORIGIN}/healthz`);
        await res.text();
        if (res.status === 200) ok++;
      } catch {
        // 失敗は ok に数えない
      }
      latencies.push(this.deps.now() - t);
    }
    const s = summarize(latencies);
    return json(200, { count, ok, p50: s.p50, p99: s.p99, max: s.max });
  }

  private async forward(action: "bench/raft" | "bench/loop" | "egress", v: ParamValues): Promise<Response> {
    const c = this.running();
    if (c === null) return this.notRunning();
    const q = new URLSearchParams();
    for (const [k, val] of Object.entries(v)) q.set(k, String(val));
    const qs = q.toString();
    try {
      const res = await c
        .getTcpPort(CONTAINER_PORT)
        .fetch(`${CONTAINER_ORIGIN}/${action}${qs === "" ? "" : `?${qs}`}`, { method: "POST" });
      return new Response(await res.text(), { status: res.status, headers: JSON_HEADERS });
    } catch (e) {
      return errorResponse(502, "container_error", messageOf(e));
    }
  }

  private async crash(restart: boolean, colo: string | null): Promise<Response> {
    const c = this.running();
    if (c === null) return this.notRunning();
    if (restart && this.metaInt("starts") >= START_CAP) {
      return errorResponse(429, "start_cap", `a session can start at most ${START_CAP} times`);
    }
    if (this.exit === null || this.exit.done) this.watch(c); // Durable Object が作り直された後など
    const watched = this.exit;
    if (watched === null) return errorResponse(500, "no_monitor", "monitor() is not available");

    const t0 = this.deps.now();
    let crashStatus: number | null = null;
    try {
      const res = await c.getTcpPort(CONTAINER_PORT).fetch(`${CONTAINER_ORIGIN}/crash`, { method: "POST" });
      await res.text();
      crashStatus = res.status;
    } catch {
      crashStatus = null;
    }
    const deadline = t0 + CRASH_DETECT_TIMEOUT_MS;
    while (!watched.done) {
      const left = deadline - this.deps.now();
      if (left <= 0) break;
      await Promise.race([watched.promise, this.deps.sleep(Math.min(left, 1000))]);
    }
    const timeout = !watched.done;
    const detectMs = timeout ? null : this.deps.now() - t0;
    const detail = { crashStatus, detectMs, timeout, exit: watched.result };
    this.record("crash", detail);
    this.log("crash", detail);

    let restartBody: Record<string, unknown> | null = null;
    let restartMs: number | null = null;
    let status = 200;
    if (restart && !timeout) {
      const v = toStartValues({ ...defaultStartValues(), ...(this.metaJson("lastStart") as ParamValues | null) });
      const run = await this.reserveStart(v.maxRunMs);
      if (run === null) {
        status = 429;
        restartBody = { error: "start_cap", message: `a session can start at most ${START_CAP} times` };
      } else {
        const o = await this.boot(c, v, colo, run);
        status = o.status;
        restartBody = o.body;
        restartMs = typeof o.body["startMs"] === "number" ? o.body["startMs"] : null;
      }
    }
    return json(status, { ...detail, restartMs, restart: restartBody });
  }

  private appendRows(rows: number): Response {
    const base = this.count("input_log");
    for (let i = 0; i < rows; i++) {
      this.sql.exec(
        "INSERT INTO input_log (t, cmd) VALUES (?, ?)",
        this.deps.now(),
        JSON.stringify({ type: "spike.cmd", i: base + i }),
      );
    }
    return json(200, { rows, total: this.count("input_log") });
  }

  // ---- WS ----

  private ws(request: Request): Response {
    if (request.headers.get("upgrade")?.toLowerCase() !== "websocket") {
      return errorResponse(426, "upgrade_required", "use a WebSocket upgrade");
    }
    const c = this.running();
    if (c === null) return this.notRunning(); // Upgrade の前に 409 を返す（README に書く）
    const [client, server] = this.deps.createPair();
    const stats = newRelayStats();
    this.relay = stats;
    void startRelay(
      server,
      async () => {
        const res = await c
          .getTcpPort(CONTAINER_PORT)
          .fetch(`${CONTAINER_ORIGIN}/ws`, { headers: { Upgrade: "websocket" } });
        return (res as unknown as { webSocket?: WebSocketLike | null }).webSocket ?? null;
      },
      stats,
    );
    return this.deps.upgradeResponse(client);
  }
}

function defaultStartValues(): ParamValues {
  const r = parseParams(new URLSearchParams(), START_PARAMS);
  return r.ok ? r.values : {};
}

/** Durable Object のクラス（DurableObject を継承しない。Object.getPrototypeOf(SpikeSession.prototype) === Object.prototype） */
export class SpikeSession {
  private readonly core: SessionCore;

  constructor(ctx: DurableObjectStateLike, _env: unknown, deps: Partial<SessionDeps> = {}) {
    this.core = new SessionCore(ctx, { ...defaultSessionDeps, ...deps });
  }

  fetch(request: Request): Promise<Response> {
    return this.core.fetch(request);
  }

  alarm(): Promise<void> {
    return this.core.alarm();
  }
}
