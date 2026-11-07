// スパイク（T-007a / #134）の測定のスクリプト。人が本番の workers.dev に対して実行する。ローカル（cf dev）にも使う。
//
//   node spike/scripts/measure.ts --base <URL> [--token-file <path>] [--instance lite|standard-1]
//        [--only start,ws,ping,bench,loop,sql,plain,egress,crash,cpu,stop,alarm,inactivity]
//        [--runs 10] [--concurrency 3] [--first <n>] [--internet-control] [--idle 120] [--out <path>]
//
// - トークンは環境変数 SPIKE_TOKEN か --token-file。出力・ログに出さない
// - Durable Object の番号は --first から順に使う（同じ番号は 3 回までしか起動できない）。使った番号は結果に書く
// - 各項目は「起動 → 測定 → stop」。失敗・例外・SIGINT でも、起動したものに必ず stop を呼んでから終える
// - 結果は JSON（--out、既定は標準出力）と ADR-0016 に貼る Markdown の表（--out のときは標準出力、なければ標準エラー）
// - 終了コード: 成功 0、どれかの項目の失敗・stop の失敗 1、引数・トークンの誤り 2
// 追加の依存を使わない（Node 22.23 以上の fetch・WebSocket・型の除去）。
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  SessionPool,
  USAGE,
  isLocalBase,
  paddedMessage,
  parseArgs,
  renderTable,
  sessionsNeeded,
  summarize,
  type Item,
  type ItemResult,
  type MeasureResult,
  type Options,
} from "./lib.ts";

type Json = Record<string, unknown>;

interface ApiResult {
  status: number;
  body: Json;
  clientMs: number;
}

const here = dirname(fileURLToPath(import.meta.url));

function log(msg: string): void {
  process.stderr.write(`[measure] ${msg}\n`);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

class ItemError extends Error {}

function readToken(o: Options): string | null {
  if (o.tokenFile !== null) {
    try {
      return readFileSync(o.tokenFile, "utf8").replace(/\r?\n$/, "");
    } catch (e) {
      log(`--token-file を読めない: ${e instanceof Error ? e.message : String(e)}`);
      return null;
    }
  }
  const t = process.env["SPIKE_TOKEN"];
  return t === undefined || t === "" ? null : t;
}

function cfVersion(): string {
  try {
    const pkg = JSON.parse(readFileSync(join(here, "..", "package.json"), "utf8")) as {
      devDependencies?: Record<string, string>;
    };
    return pkg.devDependencies?.["cf"] ?? "?";
  } catch {
    return "?";
  }
}

class Runner {
  private readonly o: Options;
  private readonly token: string;
  private readonly pool: SessionPool;
  readonly started = new Set<number>();
  readonly stopFailures: MeasureResult["stopFailures"] = [];

  constructor(o: Options, token: string) {
    this.o = o;
    this.token = token;
    this.pool = new SessionPool(o.first);
  }

  /** 使った番号（--first から順） */
  get used(): number[] {
    return this.pool.used;
  }

  /** starts 回の起動に使う番号（同じ番号は START_CAP 回まで） */
  acquire(starts: number): number {
    return this.pool.acquire(starts);
  }

  acquireDistinct(k: number): number[] {
    return this.pool.acquireDistinct(k, 1);
  }

  async api(method: "GET" | "POST", path: string, query: Record<string, string | number> = {}): Promise<ApiResult> {
    const url = new URL(path, this.o.base);
    for (const [k, v] of Object.entries(query)) url.searchParams.set(k, String(v));
    const t = performance.now();
    try {
      const res = await fetch(url, {
        method,
        headers: { authorization: `Bearer ${this.token}` },
        signal: AbortSignal.timeout(180_000),
      });
      const text = await res.text();
      let body: Json;
      try {
        const parsed: unknown = JSON.parse(text);
        body = typeof parsed === "object" && parsed !== null ? (parsed as Json) : { value: parsed };
      } catch {
        body = { text: text.slice(0, 500) };
      }
      return { status: res.status, body, clientMs: performance.now() - t };
    } catch (e) {
      return { status: 0, body: { error: "network", message: e instanceof Error ? e.message : String(e) }, clientMs: performance.now() - t };
    }
  }

  async start(n: number, query: Record<string, string | number> = {}): Promise<ApiResult> {
    this.started.add(n); // 期限切れでも起動しているかもしれないので、先に stop の対象にする
    const r = await this.api("POST", `/spike/s/${n}/start`, { instance: this.o.instance, ...query });
    log(`start s-${n}: ${r.status}${typeof r.body["startMs"] === "number" ? ` startMs=${r.body["startMs"]}` : ""}`);
    return r;
  }

  async stop(n: number): Promise<ApiResult> {
    const r = await this.api("POST", `/spike/s/${n}/stop`);
    if (r.status === 200) {
      this.started.delete(n);
    } else {
      this.stopFailures.push({ session: n, status: r.status, error: JSON.stringify(r.body) });
      log(`stop s-${n} が失敗: ${r.status}（README の「手で止める」を参照）`);
    }
    return r;
  }

  async stopAll(): Promise<void> {
    for (const n of [...this.started]) await this.stop(n);
  }

  async status(n: number): Promise<ApiResult> {
    return this.api("GET", `/spike/s/${n}/status`);
  }

  /** 起動 → fn → stop。起動に失敗したら ItemError。starts は fn の中の起動し直しを含む回数 */
  async withSession<T>(
    query: Record<string, string | number>,
    fn: (n: number, start: ApiResult) => Promise<T>,
    starts = 1,
  ): Promise<T> {
    const n = this.acquire(starts);
    try {
      const s = await this.start(n, query);
      if (s.status !== 200) throw new ItemError(`start s-${n}: ${s.status} ${JSON.stringify(s.body)}`);
      return await fn(n, s);
    } finally {
      await this.stop(n);
    }
  }

  wsUrl(n: number): string {
    const u = new URL(`/spike/s/${n}/ws`, this.o.base.replace(/^http/, "ws"));
    u.searchParams.set("token", this.token);
    return u.toString();
  }
}

/** Node の WebSocket を、届いた順に読めるようにする */
class WsClient {
  private readonly ws: WebSocket;
  private readonly queue: string[] = [];
  private waiter: (() => void) | null = null;
  closed: { code: number; reason: string } | null = null;
  private closeWaiters: (() => void)[] = [];

  private constructor(ws: WebSocket) {
    this.ws = ws;
    ws.addEventListener("message", (ev) => {
      this.queue.push(typeof ev.data === "string" ? ev.data : "<binary>");
      this.waiter?.();
    });
    ws.addEventListener("close", (ev) => {
      this.closed = { code: ev.code, reason: ev.reason };
      this.waiter?.();
      for (const w of this.closeWaiters.splice(0)) w();
    });
  }

  static open(url: string, timeoutMs = 15_000): Promise<WsClient> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      const client = new WsClient(ws);
      const timer = setTimeout(() => reject(new Error("ws open timeout")), timeoutMs);
      ws.addEventListener("open", () => {
        clearTimeout(timer);
        resolve(client);
      });
      ws.addEventListener("error", () => {
        clearTimeout(timer);
        reject(new Error("ws error before open"));
      });
    });
  }

  send(s: string): void {
    this.ws.send(s);
  }

  /** 次の電文。timeoutMs までに来なければ・閉じたら null */
  async next(timeoutMs: number): Promise<string | null> {
    const deadline = performance.now() + timeoutMs;
    for (;;) {
      const m = this.queue.shift();
      if (m !== undefined) return m;
      if (this.closed !== null) return null;
      const left = deadline - performance.now();
      if (left <= 0) return null;
      await new Promise<void>((resolve) => {
        const timer = setTimeout(resolve, left);
        this.waiter = () => {
          clearTimeout(timer);
          resolve();
        };
      });
      this.waiter = null;
    }
  }

  async waitClose(timeoutMs: number): Promise<{ code: number; reason: string } | null> {
    if (this.closed !== null) return this.closed;
    await Promise.race([new Promise<void>((r) => this.closeWaiters.push(r)), sleep(timeoutMs)]);
    return this.closed;
  }

  close(code = 1000): void {
    this.ws.close(code, "measure done");
  }
}

function seqOf(msg: string | null): number | null {
  if (msg === null) return null;
  try {
    const v = (JSON.parse(msg) as { seq?: unknown }).seq;
    return typeof v === "number" ? v : null;
  } catch {
    return null;
  }
}

// ---- 項目 ----

type ItemFn = (r: Runner, o: Options) => Promise<ItemResult>;

const itemStart: ItemFn = async (r, o) => {
  const runs: Json[] = [];
  let info: unknown = null;
  let colo: unknown = null;
  for (let i = 0; i < o.runs; i++) {
    const n = r.acquire(1);
    const s = await r.start(n);
    runs.push({
      session: n,
      status: s.status,
      cold: i === 0,
      startMs: s.body["startMs"] ?? null,
      polls: s.body["polls"] ?? null,
      inactivityTimeout: s.body["inactivityTimeout"] ?? null, // setInactivityTimeout が効いたか（"ok" か失敗の理由）
      clientMs: s.clientMs,
      ...(s.status !== 200 ? { error: s.body } : {}),
    });
    if (info === null && s.status === 200) {
      info = s.body["info"] ?? null;
      colo = s.body["colo"] ?? null;
    }
    await r.stop(n);
  }
  const ns = r.acquireDistinct(o.concurrency);
  const results = await Promise.all(ns.map((n) => r.start(n)));
  const concurrent = results.map((s, i) => ({
    session: ns[i],
    status: s.status,
    startMs: s.body["startMs"] ?? null,
    clientMs: s.clientMs,
  }));
  await Promise.all(ns.map((n) => r.stop(n)));
  const ok = runs.every((x) => x["status"] === 200) && concurrent.every((x) => x.status === 200);
  return { ok, data: { runs, concurrent, info, colo } };
};

const itemWs: ItemFn = async (r, o) =>
  r.withSession({}, async (n) => {
    const ws = await WsClient.open(r.wsUrl(n));
    const rtt: number[] = [];
    let lost1k = 0;
    for (let i = 0; i < 200; i++) {
      const t = performance.now();
      ws.send(paddedMessage(i, "rtt", 1024));
      if (seqOf(await ws.next(5000)) === i) rtt.push(performance.now() - t);
      else lost1k++;
    }
    let ok16k = 0;
    for (let i = 0; i < 20; i++) {
      ws.send(paddedMessage(1000 + i, "16k", 16384));
      if (seqOf(await ws.next(5000)) === 1000 + i) ok16k++;
    }
    ws.send(paddedMessage(2000, "over", 16385));
    const over = await ws.next(2000);
    const oversizeDropped = over === null && ws.closed === null;
    const burstSeqs: number[] = [];
    for (let i = 0; i < 500; i++) ws.send(paddedMessage(3000 + i, "burst", 64));
    for (;;) {
      const s = seqOf(await ws.next(10_000));
      if (s === null) break;
      burstSeqs.push(s);
      if (burstSeqs.length >= 500) break;
    }
    const inOrder = burstSeqs.every((s, i) => i === 0 || s > (burstSeqs[i - 1] ?? -1));
    let idle: Json | null = null;
    if (o.idle > 0) {
      log(`ws: ${o.idle} 秒待つ（無通信）`);
      await sleep(o.idle * 1000);
      ws.send(paddedMessage(4000, "idle", 128));
      idle = { seconds: o.idle, ok: seqOf(await ws.next(5000)) === 4000 };
    }
    ws.close(1000);
    const clientClose = await ws.waitClose(5000);
    await sleep(1000);
    const relayAfterClose = (await r.status(n)).body["relay"] ?? null;

    // コンテナ → ブラウザの切断: もう 1 つつなぎ、コンテナを止める
    let serverClose: Json | null = null;
    try {
      const ws2 = await WsClient.open(r.wsUrl(n));
      await r.stop(n);
      r.started.add(n); // withSession の finally でもう一度 stop を呼ぶ（動いていなければ 200）
      const c = await ws2.waitClose(15_000);
      serverClose = c === null ? { code: null } : { code: c.code, reason: c.reason };
    } catch (e) {
      serverClose = { error: e instanceof Error ? e.message : String(e) };
    }
    const rttSummary = summarize(rtt);
    const ok =
      lost1k === 0 && ok16k === 20 && oversizeDropped && burstSeqs.length === 500 && inOrder && (idle?.["ok"] ?? true) === true;
    return {
      ok,
      data: {
        rtt1k: rttSummary,
        lost1k,
        ok16k,
        sent16k: 20,
        oversizeDropped,
        burst: { sent: 500, received: burstSeqs.length, inOrder },
        idle,
        clientClose,
        relayAfterClose,
        serverClose,
      },
    };
  });

const itemPing: ItemFn = async (r) =>
  r.withSession({}, async (n) => {
    const p = await r.api("POST", `/spike/s/${n}/ping`, { count: 200 });
    return { ok: p.status === 200, data: { status: p.status, ...p.body, clientMs: p.clientMs } };
  });

const itemBench: ItemFn = async (r) =>
  r.withSession({}, async (n) => {
    const b = await r.api("POST", `/spike/s/${n}/bench/raft`, { nodes: 7, minutes: 30, proposalsPerSec: 2 });
    return { ok: b.status === 200, data: { status: b.status, result: b.body, clientMs: b.clientMs } };
  });

const itemLoop: ItemFn = async (r) =>
  r.withSession({}, async (n) => {
    const speeds: Json[] = [];
    for (const speed of ["1", "4", "8"]) {
      const b = await r.api("POST", `/spike/s/${n}/bench/loop`, { nodes: 7, speed, seconds: 20 });
      log(`loop ×${speed}: ${b.status}`);
      speeds.push({ speed, status: b.status, result: b.body });
    }
    return { ok: speeds.every((s) => s["status"] === 200), data: { speeds } };
  });

const itemSql: ItemFn = async (r) => {
  const n = r.acquire(2); // 同じ番号で起動し直して、行が残るかを見る
  const data: Json = { session: n };
  try {
    const s1 = await r.start(n);
    if (s1.status !== 200) throw new ItemError(`start: ${s1.status}`);
    const a = await r.api("POST", `/spike/s/${n}/sql`, { rows: 100 });
    const b = await r.api("POST", `/spike/s/${n}/sql`, { rows: 1000 });
    data["rows100ClientMs"] = a.clientMs;
    data["rows1000ClientMs"] = b.clientMs;
    const total = typeof b.body["total"] === "number" ? b.body["total"] : null;
    data["totalBeforeRestart"] = total;
    await r.stop(n);
    const s2 = await r.start(n);
    data["restartStatus"] = s2.status;
    const st = await r.status(n);
    const rows = typeof st.body["inputLogRows"] === "number" ? st.body["inputLogRows"] : null;
    data["rowsAfterRestart"] = rows;
    data["keptAfterRestart"] = total !== null && rows !== null && rows >= total;
    const ok = a.status === 200 && b.status === 200 && data["keptAfterRestart"] === true;
    return { ok, data };
  } finally {
    await r.stop(n);
  }
};

const itemPlain: ItemFn = async (r) => {
  const one = async (kind: "plain" | "control") => {
    const a = await r.api("POST", `/spike/${kind}/1`);
    const b = await r.api("POST", `/spike/${kind}/1`);
    const okShape =
      a.status === 200 &&
      b.status === 200 &&
      typeof a.body["n"] === "number" &&
      b.body["n"] === a.body["n"] + 1 &&
      a.body["extendsDurableObject"] === (kind === "control");
    return { ok: okShape, first: a.body, second: b.body, status: [a.status, b.status] };
  };
  const plain = await one("plain");
  const control = await one("control");
  return { ok: plain.ok && control.ok, data: { plainOk: plain.ok, controlOk: control.ok, plain, control } };
};

const itemEgress: ItemFn = async (r, o) => {
  const run = (internet: number) =>
    r.withSession({ internet }, async (n) => {
      const e = await r.api("POST", `/spike/s/${n}/egress`);
      if (e.status !== 200) throw new ItemError(`egress: ${e.status} ${JSON.stringify(e.body)}`);
      return e.body;
    });
  const data: Json = { internet0: await run(0) };
  if (o.internetControl) data["internet1"] = await run(1);
  const e0 = data["internet0"] as Json;
  data["internet0AllFailed"] = e0["dns"] !== "ok" && e0["tcp"] !== "ok" && e0["https"] !== "ok";
  return { ok: true, data };
};

const itemCrash: ItemFn = async (r) =>
  r.withSession({}, async (n) => {
    const c = await r.api("POST", `/spike/s/${n}/crash`, { restart: 1 });
    return { ok: c.status === 200 && c.body["timeout"] !== true, data: { status: c.status, ...c.body } };
  }, 2);

const itemCpu: ItemFn = async (r) => {
  const n = r.acquire(0); // コンテナは起動しない
  const worker: Json[] = [];
  const durableObject: Json[] = [];
  for (const iters of [1e6, 1e7, 3e7, 1e8, 3e8, 1e9]) {
    const w = await r.api("POST", "/spike/cpu", { iters });
    worker.push({ iters, status: w.status, clientMs: w.clientMs, error: w.status === 200 ? null : w.body });
    const d = await r.api("POST", `/spike/s/${n}/cpu`, { iters });
    durableObject.push({ iters, status: d.status, clientMs: d.clientMs, error: d.status === 200 ? null : d.body });
    log(`cpu ${iters.toExponential(0).replace("e+", "e")}: worker ${w.status}・DO ${d.status}`);
  }
  return { ok: worker[0]?.["status"] === 200 && durableObject[0]?.["status"] === 200, data: { session: n, worker, durableObject } };
};

const itemStop: ItemFn = async (r) => {
  const n = r.acquire(1);
  const s = await r.start(n);
  if (s.status !== 200) {
    await r.stop(n);
    throw new ItemError(`start: ${s.status}`);
  }
  const st = await r.stop(n);
  const after = await r.status(n);
  const runningAfter = after.body["running"];
  return {
    ok: st.status === 200 && runningAfter === false,
    data: { session: n, stopMs: st.body["stopMs"] ?? null, clientMs: st.clientMs, runningAfter },
  };
};

const alarmCount = (st: ApiResult): number =>
  (Array.isArray(st.body["runs"]) ? (st.body["runs"] as Json[]) : []).filter((x) => x["kind"] === "alarm").length;

const itemAlarm: ItemFn = async (r) => {
  const n = r.acquire(1);
  try {
    const before = alarmCount(await r.status(n)); // 同じ番号を前の項目が使っていても、この項目の分だけを数える
    const s = await r.start(n, { maxRunMs: 60000 });
    if (s.status !== 200) throw new ItemError(`start: ${s.status}`);
    log("alarm: 75 秒待つ");
    await sleep(75_000);
    const st = await r.status(n);
    const alarmRecorded = alarmCount(st) > before;
    const stopped = st.body["running"] === false;
    return { ok: stopped && alarmRecorded, data: { session: n, maxRunMs: 60000, stopped, alarmRecorded } };
  } finally {
    await r.stop(n);
  }
};

const itemInactivity: ItemFn = async (r) => {
  const n = r.acquire(1);
  try {
    const s = await r.start(n, { inactivityMs: 30000 });
    if (s.status !== 200) throw new ItemError(`start: ${s.status}`);
    log("inactivity: 60 秒何もせずに待つ");
    await sleep(60_000);
    const st = await r.status(n);
    return {
      ok: st.status === 200,
      data: {
        session: n,
        inactivityMs: 30000,
        stopped: st.body["running"] === false,
        note: "status の要求自体が Durable Object を起こす（コンテナへの要求はしない）",
      },
    };
  } finally {
    await r.stop(n);
  }
};

const ITEM_FNS: Record<Item, ItemFn> = {
  start: itemStart,
  ws: itemWs,
  ping: itemPing,
  bench: itemBench,
  loop: itemLoop,
  sql: itemSql,
  plain: itemPlain,
  egress: itemEgress,
  crash: itemCrash,
  cpu: itemCpu,
  stop: itemStop,
  alarm: itemAlarm,
  inactivity: itemInactivity,
};

async function main(): Promise<number> {
  const parsed = parseArgs(process.argv.slice(2));
  if (!parsed.ok) {
    process.stderr.write(`measure: ${parsed.message}\n${USAGE}\n`);
    return 2;
  }
  const o = parsed.options;
  const token = readToken(o);
  if (token === null) {
    process.stderr.write(`measure: トークンがない（SPIKE_TOKEN か --token-file）\n${USAGE}\n`);
    return 2;
  }
  log(`base=${o.base} instance=${o.instance} only=${o.only.join(",")} 番号 s-${o.first}〜s-${o.first + sessionsNeeded(o) - 1}`);

  const runner = new Runner(o, token);
  let interrupted = false;
  process.on("SIGINT", () => {
    if (interrupted) return;
    interrupted = true;
    log("SIGINT: 起動したものを止めてから終える");
    void runner.stopAll().finally(() => process.exit(130));
  });

  const result: MeasureResult = {
    startedAt: new Date().toISOString(),
    finishedAt: "",
    base: o.base,
    target: isLocalBase(o.base) ? "local" : o.instance,
    instance: o.instance,
    cfVersion: cfVersion(),
    options: {
      instance: o.instance,
      only: o.only,
      runs: o.runs,
      concurrency: o.concurrency,
      first: o.first,
      internetControl: o.internetControl,
      idle: o.idle,
    },
    sessions: runner.used,
    items: {},
    stopFailures: runner.stopFailures,
    ok: false,
  };
  for (const item of o.only) {
    if (interrupted) break;
    log(`== ${item}`);
    try {
      result.items[item] = await ITEM_FNS[item](runner, o);
    } catch (e) {
      result.items[item] = { ok: false, error: e instanceof Error ? e.message : String(e), data: {} };
    }
    log(`${item}: ${result.items[item]?.ok === true ? "ok" : "失敗"}`);
  }
  await runner.stopAll();
  result.finishedAt = new Date().toISOString();
  result.ok = Object.values(result.items).every((x) => x.ok) && runner.stopFailures.length === 0;

  const table = renderTable(result);
  const out = `${JSON.stringify({ ...result, table }, null, 2)}\n`;
  if (o.out !== null) {
    writeFileSync(o.out, out);
    process.stdout.write(table);
  } else {
    process.stdout.write(out);
    process.stderr.write(table);
  }
  if (runner.stopFailures.length > 0) {
    log(`stop に失敗した番号: ${runner.stopFailures.map((f) => f.session).join(", ")}（README の「手で止める」）`);
  }
  return result.ok ? 0 : 1;
}

process.exitCode = await main();
