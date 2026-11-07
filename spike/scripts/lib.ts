// 測定のスクリプト（measure.ts）の純粋な部分: 引数の解釈・パーセンタイル・ADR-0016 に貼る表の組み立て。
// Node の型の除去で動くよう、erasableSyntaxOnly の書き方にする。
import { percentile, summarize, type Summary } from "../worker/stats.ts";

export { percentile, summarize, type Summary };

export const ITEMS = [
  "start",
  "ws",
  "ping",
  "bench",
  "loop",
  "sql",
  "plain",
  "egress",
  "crash",
  "cpu",
  "stop",
  "alarm",
  "inactivity",
] as const;
export type Item = (typeof ITEMS)[number];
export type Instance = "lite" | "standard-1";

export interface Options {
  base: string;
  tokenFile: string | null;
  instance: Instance;
  only: Item[];
  runs: number;
  concurrency: number;
  first: number;
  internetControl: boolean;
  idle: number;
  out: string | null;
}

export type ParseArgsResult = { ok: true; options: Options } | { ok: false; message: string };

export const USAGE = `使い方:
  node spike/scripts/measure.ts --base <URL> [--token-file <path>] [--instance lite|standard-1]
       [--only ${ITEMS.join(",")}]
       [--runs 10] [--concurrency 3] [--first <n>] [--internet-control] [--idle 120] [--out <path>]
トークンは環境変数 SPIKE_TOKEN か --token-file で渡す（出力には出さない）。`;

const INT_RE = /^(0|[1-9][0-9]*)$/;

function intArg(name: string, raw: string | undefined, min: number, max: number): number | string {
  if (raw === undefined || !INT_RE.test(raw)) return `${name} は整数`;
  const n = Number(raw);
  if (n < min || n > max) return `${name} は ${min}〜${max}`;
  return n;
}

/** --base は https:// か、http://localhost・http://127.0.0.1 だけ（トークンを平文で外に送らない） */
export function checkBase(raw: string): string | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return `--base が URL でない: ${raw}`;
  }
  if (u.username !== "" || u.password !== "") return "--base に認証情報を書かない";
  if (u.search !== "" || u.hash !== "") return "--base にクエリ・フラグメントを書かない";
  if (u.pathname !== "/" && u.pathname !== "") return "--base はオリジンだけ（パスを書かない）";
  if (u.protocol === "https:") return null;
  if (u.protocol === "http:" && (u.hostname === "localhost" || u.hostname === "127.0.0.1")) return null;
  return "--base は https:// か http://localhost・http://127.0.0.1 だけ";
}

export function isLocalBase(base: string): boolean {
  const u = new URL(base);
  return u.hostname === "localhost" || u.hostname === "127.0.0.1";
}

export function parseArgs(argv: readonly string[]): ParseArgsResult {
  const o: Options = {
    base: "",
    tokenFile: null,
    instance: "lite",
    only: [...ITEMS],
    runs: 10,
    concurrency: 3,
    first: 1,
    internetControl: false,
    idle: 120,
    out: null,
  };
  const seen = new Set<string>();
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i] ?? "";
    if (seen.has(flag)) return { ok: false, message: `${flag} が 2 回ある` };
    seen.add(flag);
    if (flag === "--internet-control") {
      o.internetControl = true;
      continue;
    }
    const value = argv[i + 1];
    const takesValue = [
      "--base",
      "--token-file",
      "--instance",
      "--only",
      "--runs",
      "--concurrency",
      "--first",
      "--idle",
      "--out",
    ];
    if (!takesValue.includes(flag)) return { ok: false, message: `知らない引数: ${flag}` };
    if (value === undefined || value.startsWith("--")) return { ok: false, message: `${flag} に値がない` };
    i++;
    let n: number | string;
    switch (flag) {
      case "--base": {
        const err = checkBase(value);
        if (err !== null) return { ok: false, message: err };
        o.base = new URL(value).origin;
        break;
      }
      case "--token-file":
        o.tokenFile = value;
        break;
      case "--instance":
        if (value !== "lite" && value !== "standard-1") {
          return { ok: false, message: "--instance は lite か standard-1" };
        }
        o.instance = value;
        break;
      case "--only": {
        const names = value.split(",");
        const bad = names.filter((x) => !(ITEMS as readonly string[]).includes(x));
        if (bad.length > 0 || names.length === 0) return { ok: false, message: `--only の知らない名前: ${bad.join(",")}` };
        o.only = ITEMS.filter((x) => names.includes(x));
        break;
      }
      case "--runs":
        n = intArg("--runs", value, 1, 50);
        if (typeof n === "string") return { ok: false, message: n };
        o.runs = n;
        break;
      case "--concurrency":
        n = intArg("--concurrency", value, 1, 5);
        if (typeof n === "string") return { ok: false, message: n };
        o.concurrency = n;
        break;
      case "--first":
        n = intArg("--first", value, 1, 50);
        if (typeof n === "string") return { ok: false, message: n };
        o.first = n;
        break;
      case "--idle":
        n = intArg("--idle", value, 0, 600);
        if (typeof n === "string") return { ok: false, message: n };
        o.idle = n;
        break;
      case "--out":
        o.out = value;
        break;
    }
  }
  if (o.base === "") return { ok: false, message: "--base が要る" };
  return { ok: true, options: o };
}

/** 1 つの Durable Object で起動できる回数（spike/worker/limits.ts の START_CAP と同じ） */
export const START_CAP = 3;

/**
 * Durable Object の番号の割り当て。--first から順に使い、1 つの番号の起動の回数が START_CAP を超えないよう、
 * 残りのある一番小さい番号を選ぶ（ない・除くときは次の番号）。1 回の実行で使う番号を少なくし、
 * 2 回目の実行（--first をずらす）と重ならないようにする。サーバーの側の数は見ない（この実行の中で数える）。
 */
export class SessionPool {
  private readonly remaining = new Map<number, number>();
  private next: number;
  readonly used: number[] = [];

  constructor(first: number) {
    this.next = first;
  }

  /** starts 回の起動に使う番号（0 なら起動しない用途） */
  acquire(starts: number, exclude: ReadonlySet<number> = new Set()): number {
    for (const n of this.used) {
      const left = this.remaining.get(n) ?? 0;
      if (left >= starts && !exclude.has(n)) {
        this.remaining.set(n, left - starts);
        return n;
      }
    }
    const n = this.next++;
    this.used.push(n);
    this.remaining.set(n, START_CAP - starts);
    return n;
  }

  /** 同時に使う k 個の、別々の番号 */
  acquireDistinct(k: number, starts: number): number[] {
    const picked = new Set<number>();
    for (let i = 0; i < k; i++) picked.add(this.acquire(starts, picked));
    return [...picked];
  }
}

/** 各項目が使う起動の回数（measure.ts と同じ順。sql・crash は同じ番号で 2 回起動する） */
export function planSessions(
  pool: SessionPool,
  o: Pick<Options, "only" | "runs" | "concurrency" | "internetControl">,
): void {
  for (const item of o.only) {
    switch (item) {
      case "plain":
        break; // p-1 を使う
      case "start":
        for (let i = 0; i < o.runs; i++) pool.acquire(1);
        pool.acquireDistinct(o.concurrency, 1);
        break;
      case "sql":
      case "crash":
        pool.acquire(2);
        break;
      case "egress":
        pool.acquire(1);
        if (o.internetControl) pool.acquire(1);
        break;
      case "cpu":
        pool.acquire(0);
        break;
      default:
        pool.acquire(1);
    }
  }
}

/** --only の項目が使う Durable Object の番号の数 */
export function sessionsNeeded(o: Pick<Options, "only" | "runs" | "concurrency" | "internetControl">): number {
  const pool = new SessionPool(1);
  planSessions(pool, o);
  return pool.used.length;
}

// ---- 結果 ----

export interface ItemResult {
  ok: boolean;
  error?: string;
  data: Record<string, unknown>;
}

export interface MeasureResult {
  startedAt: string;
  finishedAt: string;
  base: string;
  target: "local" | Instance;
  instance: Instance;
  cfVersion: string;
  options: Omit<Options, "base" | "tokenFile" | "out">;
  sessions: number[];
  items: Partial<Record<Item, ItemResult>>;
  stopFailures: { session: number; status: number; error: string }[];
  ok: boolean;
}

// ---- 表 ----

type Obj = Record<string, unknown>;

function obj(v: unknown): Obj {
  return typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Obj) : {};
}

function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

function num(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

/** 数を短く書く（null は ?） */
export function fmt(v: unknown): string {
  const n = num(v);
  if (n === null) return "?";
  const a = Math.abs(n);
  if (Number.isInteger(n) || a >= 100) return String(Math.round(n));
  if (a >= 1) return String(Number(n.toFixed(1)));
  return String(Number(n.toFixed(3)));
}

function okNg(v: unknown): string {
  return v === true ? "ok" : v === false ? "ng" : "?";
}

function short(reason: unknown): string {
  if (reason === "ok") return "ok";
  if (typeof reason !== "string") return "?";
  return `失敗（${reason.length > 40 ? `${reason.slice(0, 40)}…` : reason}）`;
}

function failed(item: ItemResult | undefined): string | null {
  if (item === undefined) return null;
  if (!item.ok && Object.keys(item.data).length === 0) return `失敗: ${item.error ?? "?"}`;
  return null;
}

function cellStart(r: MeasureResult): string {
  const it = r.items.start;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const runs = arr(it.data["runs"]).map(obj);
  const okRuns = runs.filter((x) => x["status"] === 200);
  const s = summarize(okRuns.map((x) => num(x["startMs"])).filter((x): x is number => x !== null));
  const c = summarize(okRuns.map((x) => num(x["clientMs"])).filter((x): x is number => x !== null));
  const cold = runs.find((x) => x["cold"] === true);
  const conc = arr(it.data["concurrent"]).map(obj);
  const concMax = summarize(conc.map((x) => num(x["startMs"])).filter((x): x is number => x !== null)).max;
  const concOk = conc.filter((x) => x["status"] === 200).length;
  return (
    `p50 ${fmt(s.p50)} / p95 ${fmt(s.p95)} / 最大 ${fmt(s.max)} ms（DO の中、${okRuns.length}/${runs.length} 回）` +
    `・クライアント p50 ${fmt(c.p50)} ms・初回 ${fmt(cold?.["startMs"])} ms` +
    `・同時 ${conc.length}: ${concOk} 成功・最大 ${fmt(concMax)} ms`
  );
}

function cellBench(r: MeasureResult): string {
  const it = r.items.bench;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const res = obj(it.data["result"]);
  return `${fmt(res["wallMs"])} ms（${fmt(res["virtualMinutesPerSecond"])} 分/秒、CPU ${fmt(res["cpuUserMs"])} ms）`;
}

function cellLoop(r: MeasureResult): string {
  const it = r.items.loop;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  return arr(it.data["speeds"])
    .map(obj)
    .map((x) => {
      const res = obj(x["result"]);
      if (x["status"] !== 200) return `×${String(x["speed"])}: 失敗（${String(x["status"])}）`;
      return (
        `×${String(x["speed"])}: step p99 ${fmt(obj(res["stepMs"])["p99"])} ms` +
        `・遅れ p99 ${fmt(obj(res["lateMs"])["p99"])} ms・CPU ${fmt(res["cpuUtil"])}`
      );
    })
    .join("；");
}

function exp(n: number): string {
  return n.toExponential(0).replace("e+", "e");
}

function cpuSide(rows: unknown[]): string {
  const list = rows.map(obj);
  const okIters = list.filter((x) => x["status"] === 200).map((x) => num(x["iters"]) ?? 0);
  const firstFail = list.find((x) => x["status"] !== 200);
  const maxOk = okIters.length > 0 ? exp(Math.max(...okIters)) : "なし";
  if (firstFail === undefined) return `成功の最大 ${maxOk}・失敗なし`;
  return (
    `成功の最大 ${maxOk}・最初の失敗 ${exp(num(firstFail["iters"]) ?? 0)}` +
    `（${String(firstFail["status"])} ${String(firstFail["error"] ?? "")}）`
  );
}

function cellCpu(r: MeasureResult): string {
  const it = r.items.cpu;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  return `Worker: ${cpuSide(arr(it.data["worker"]))}／DO: ${cpuSide(arr(it.data["durableObject"]))}`;
}

function cellWs(r: MeasureResult): string {
  const it = r.items.ws;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const d = it.data;
  const rtt = obj(d["rtt1k"]);
  const burst = obj(d["burst"]);
  const idle = d["idle"] === null || d["idle"] === undefined ? null : obj(d["idle"]);
  const relay = obj(d["relayAfterClose"]);
  const serverClose = obj(d["serverClose"]);
  return (
    `1 KiB p50 ${fmt(rtt["p50"])} / p99 ${fmt(rtt["p99"])} ms（${fmt(rtt["n"])} 回）` +
    `・16 KiB ${fmt(d["ok16k"])}/${fmt(d["sent16k"])}` +
    `・超過 ${d["oversizeDropped"] === true ? "破棄" : d["oversizeDropped"] === false ? "返った" : "?"}` +
    `・500 件 ${fmt(burst["received"])}/${fmt(burst["sent"])}（順序 ${burst["inOrder"] === true ? "ok" : "乱れ"}）` +
    `・無通信 ${idle === null ? "—" : `${fmt(idle["seconds"])} 秒 ${okNg(idle["ok"])}`}` +
    `・切断 ブラウザ→コンテナ ${relay["closedBy"] === "browser" ? "ok" : "?"}` +
    `／コンテナ→ブラウザ ${fmt(serverClose["code"])}`
  );
}

function cellPing(r: MeasureResult): string {
  const it = r.items.ping;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const d = it.data;
  return `p50 ${fmt(d["p50"])} / p99 ${fmt(d["p99"])} ms（${fmt(d["ok"])}/${fmt(d["count"])} 回）`;
}

function cellSql(r: MeasureResult): string {
  const it = r.items.sql;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const d = it.data;
  const kept = d["keptAfterRestart"];
  return (
    `100 行 ${fmt(d["rows100ClientMs"])} ms・1,000 行 ${fmt(d["rows1000ClientMs"])} ms（クライアント）` +
    `・再起動の後 ${kept === true ? "残る" : kept === false ? "消えた" : "?"}（${fmt(d["rowsAfterRestart"])} 行）`
  );
}

function cellPlain(r: MeasureResult): string {
  const it = r.items.plain;
  const parts: string[] = [];
  if (it !== undefined) {
    const f = failed(it);
    if (f !== null) parts.push(f);
    else {
      parts.push(`PlainStore（継承しない）${okNg(it.data["plainOk"])}`);
      parts.push(`ControlStore（継承する）${okNg(it.data["controlOk"])}`);
    }
  }
  if (r.items.start !== undefined) parts.push(`SpikeSession の container ${okNg(r.items.start.ok)}`);
  if (r.items.alarm !== undefined) parts.push(`alarm ${okNg(r.items.alarm.ok)}`);
  return parts.join("・");
}

function egressSide(v: unknown): string {
  const e = obj(v);
  return `DNS ${short(e["dns"])} / TCP ${short(e["tcp"])} / HTTPS ${short(e["https"])}`;
}

function cellEgress(r: MeasureResult): string {
  const it = r.items.egress;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const parts = [`false: ${egressSide(it.data["internet0"])}`];
  if (it.data["internet1"] !== undefined) parts.push(`true（対照）: ${egressSide(it.data["internet1"])}`);
  return parts.join("；");
}

function cellStop(r: MeasureResult): string {
  const parts: string[] = [];
  const stop = r.items.stop;
  if (stop !== undefined) {
    parts.push(
      failed(stop) ??
        `destroy ${fmt(stop.data["stopMs"])} ms で running=${String(stop.data["runningAfter"] ?? "?")}`,
    );
  }
  const alarm = r.items.alarm;
  if (alarm !== undefined) {
    parts.push(
      failed(alarm) ??
        `アラーム（${fmt(alarm.data["maxRunMs"])} ms）${alarm.data["stopped"] === true ? "で止まった" : "で止まらない"}`,
    );
  }
  const ina = r.items.inactivity;
  if (ina !== undefined) {
    parts.push(
      failed(ina) ??
        `inactivity（${fmt(ina.data["inactivityMs"])} ms）${ina.data["stopped"] === true ? "で止まった" : "で止まらない"}`,
    );
  }
  return parts.join("・");
}

function cellCrash(r: MeasureResult): string {
  const it = r.items.crash;
  if (it === undefined) return "";
  const f = failed(it);
  if (f !== null) return f;
  const d = it.data;
  if (d["timeout"] === true) return "検知できない（30 秒で打ち切り）";
  return `検知 ${fmt(d["detectMs"])} ms・再起動 ${fmt(d["restartMs"])} ms`;
}

function cellLocal(r: MeasureResult): string {
  const names: Item[] = ["start", "ws", "sql", "plain", "stop", "egress", "crash", "cpu"];
  return names
    .filter((n) => r.items[n] !== undefined)
    .map((n) => `${n} ${okNg(r.items[n]?.ok)}`)
    .join("・");
}

function cellInfo(r: MeasureResult): string {
  const it = r.items.start;
  if (it === undefined) return "";
  const info = obj(it.data["info"]);
  if (Object.keys(info).length === 0) return failed(it) ?? "";
  const cg = obj(info["cgroup"]);
  const env = obj(info["env"]);
  return (
    `CPU ${fmt(info["numCPU"])}・GOMAXPROCS ${fmt(info["gomaxprocs"])}` +
    `・cpu.max ${String(cg["cpuMax"] ?? "null")}・memory.max ${String(cg["memoryMax"] ?? "null")}` +
    `・env ${String(env["MODE"] ?? "null")}/${String(env["PORT"] ?? "null")}・colo ${String(it.data["colo"] ?? "null")}`
  );
}

const ROWS: readonly { id: string; label: string; cell: ((r: MeasureResult) => string) | null }[] = [
  { id: "M-1", label: "コンテナの起動時間（p50 / p95 / 最大、初回、同時 3 つ）", cell: cellStart },
  { id: "M-2", label: "復元の速さ（7 島・30 分の再計算。手元の PC との比）", cell: cellBench },
  { id: "M-3", label: "8 倍速（1 ステップの p99・遅れの p99・CPU の使用率）", cell: cellLoop },
  { id: "M-4", label: "limits.cpuMs（止まる反復回数・DO に効くか・ふつうの要求の CPU 時間）", cell: cellCpu },
  { id: "M-5", label: "WS の中継（往復の p50 / p99、16 KiB、500 件、無通信 120 秒、切断）", cell: cellWs },
  { id: "M-6", label: "DO とコンテナの HTTP の往復（p50 / p99）", cell: cellPing },
  { id: "M-7", label: "DO の SQLite（100 行・1,000 行、再起動の後）", cell: cellSql },
  { id: "M-8", label: "DurableObject を継承しないクラス（sql・container・alarm）", cell: cellPlain },
  { id: "M-9", label: "enableInternet: false（DNS・TCP・HTTPS。対照）", cell: cellEgress },
  { id: "M-10", label: "停止（destroy・アラーム・setInactivityTimeout）", cell: cellStop },
  { id: "M-11", label: "落ちたときの検知と再起動", cell: cellCrash },
  { id: "M-12", label: "cf dev での動作", cell: null },
  { id: "M-13", label: "デプロイ（時間・イメージの大きさ・ログ）", cell: null },
  { id: "M-14", label: "コンテナから見た資源（CPU・メモリ・env・colo）", cell: cellInfo },
];

function escapeCell(s: string): string {
  return s.replaceAll("|", "\\|").replaceAll("\n", " ");
}

/** ADR-0016 に貼る表（spike/README.md の雛形と同じ列）。測った列だけを埋める */
export function renderTable(r: MeasureResult): string {
  const date = r.startedAt.slice(0, 10);
  const lines = [
    `## T-007 の測定結果（${date}、cf@${r.cfVersion}）`,
    "",
    "| ID | 項目 | lite | standard-1 | ローカル（cf dev） | 判断 |",
    "|---|---|---|---|---|---|",
  ];
  for (const row of ROWS) {
    const cols = { lite: "", "standard-1": "", local: "" };
    if (row.id === "M-12") {
      cols.lite = "—";
      cols["standard-1"] = "—";
      if (r.target === "local") cols.local = cellLocal(r);
    } else {
      if (row.id === "M-13") cols.local = "—";
      if (row.cell !== null) cols[r.target] = row.cell(r);
    }
    const cells = [row.id, row.label, cols.lite, cols["standard-1"], cols.local, ""].map(escapeCell);
    lines.push(`|${cells.map((c) => (c === "" ? " " : ` ${c} `)).join("|")}|`);
  }
  return `${lines.join("\n")}\n`;
}

/** 決まった大きさ（バイト）の ASCII の JSON の電文を作る */
export function paddedMessage(seq: number, kind: string, bytes: number): string {
  const base = JSON.stringify({ seq, kind, pad: "" });
  const padLen = bytes - base.length;
  if (padLen < 0) throw new RangeError(`message too small: ${bytes}`);
  return JSON.stringify({ seq, kind, pad: "x".repeat(padLen) });
}
