// スパイク（T-007a / #134）の上限の値。Worker（router.ts）と Durable Object（session.ts）が同じ検査をする。
// コンテナ（spike/container/loadmodel/bench.go）も bench の引数を同じ値で検査する。

export const MAX_SESSIONS_DEFAULT = 30;
export const MAX_SESSIONS_MIN = 1;
export const MAX_SESSIONS_MAX = 50;
/** `plain`・`control` の番号の上限 */
export const MAX_STORES = 5;
/** 1 つの Durable Object で起動（＝アラームの設定）できる回数 */
export const START_CAP = 3;
/** WS の 1 通の上限（DesignDoc §12.5） */
export const MAX_WS_MESSAGE_BYTES = 16384;
/** トークンの最小の長さ */
export const MIN_TOKEN_LENGTH = 32;
/** crash で monitor() が終わるのを待つ上限 */
export const CRASH_DETECT_TIMEOUT_MS = 30000;
/** 準備完了を待つ間隔 */
export const READY_POLL_INTERVAL_MS = 100;
/** コンテナの待ち受けのポート（コンテナの中だけ。外には出さない） */
export const CONTAINER_PORT = 8080;

export type IntSpec = { kind: "int"; min: number; max: number; default?: number; required?: boolean };
export type EnumSpec = { kind: "enum"; values: readonly string[]; default?: string; required?: boolean };
export type ParamSpec = IntSpec | EnumSpec;
export type ParamSpecs = Readonly<Record<string, ParamSpec>>;

export const START_PARAMS = {
  instance: { kind: "enum", values: ["lite", "standard-1"], default: "lite" },
  internet: { kind: "int", min: 0, max: 1, default: 0 },
  maxRunMs: { kind: "int", min: 10000, max: 300000, default: 300000 },
  inactivityMs: { kind: "int", min: 10000, max: 300000, default: 120000 },
  timeoutMs: { kind: "int", min: 1000, max: 60000, default: 30000 },
} as const satisfies ParamSpecs;

export const PING_PARAMS = {
  count: { kind: "int", min: 1, max: 200, default: 50 },
} as const satisfies ParamSpecs;

export const SQL_PARAMS = {
  rows: { kind: "int", min: 1, max: 10000, default: 100 },
} as const satisfies ParamSpecs;

export const CPU_PARAMS = {
  iters: { kind: "int", min: 1, max: 1_000_000_000, required: true },
} as const satisfies ParamSpecs;

export const CRASH_PARAMS = {
  restart: { kind: "int", min: 0, max: 1, default: 0 },
} as const satisfies ParamSpecs;

export const BENCH_RAFT_PARAMS = {
  nodes: { kind: "int", min: 1, max: 9 },
  minutes: { kind: "int", min: 1, max: 60 },
  proposalsPerSec: { kind: "int", min: 0, max: 20 },
} as const satisfies ParamSpecs;

export const BENCH_LOOP_PARAMS = {
  nodes: { kind: "int", min: 1, max: 9 },
  speed: { kind: "enum", values: ["0.5", "1", "2", "4", "8"] },
  seconds: { kind: "int", min: 1, max: 60 },
  proposalsPerSec: { kind: "int", min: 0, max: 20 },
  eventsPerFlush: { kind: "int", min: 0, max: 1000 },
} as const satisfies ParamSpecs;

export const NO_PARAMS = {} as const satisfies ParamSpecs;

export type SessionAction =
  | "start"
  | "stop"
  | "status"
  | "ping"
  | "bench/raft"
  | "bench/loop"
  | "egress"
  | "crash"
  | "sql"
  | "cpu"
  | "ws";

export interface ActionSpec {
  method: "GET" | "POST";
  params: ParamSpecs;
}

/** `/spike/s/{n}/<action>` の一覧。Worker と Durable Object が同じ表で検査する */
export const SESSION_ACTIONS: Readonly<Record<SessionAction, ActionSpec>> = {
  start: { method: "POST", params: START_PARAMS },
  stop: { method: "POST", params: NO_PARAMS },
  status: { method: "GET", params: NO_PARAMS },
  ping: { method: "POST", params: PING_PARAMS },
  "bench/raft": { method: "POST", params: BENCH_RAFT_PARAMS },
  "bench/loop": { method: "POST", params: BENCH_LOOP_PARAMS },
  egress: { method: "POST", params: NO_PARAMS },
  crash: { method: "POST", params: CRASH_PARAMS },
  sql: { method: "POST", params: SQL_PARAMS },
  cpu: { method: "POST", params: CPU_PARAMS },
  ws: { method: "GET", params: NO_PARAMS },
};

export function isSessionAction(s: string): s is SessionAction {
  return Object.hasOwn(SESSION_ACTIONS, s);
}

export type ParamValues = Record<string, number | string>;
export type ParseResult = { ok: true; values: ParamValues } | { ok: false; message: string };

const INT_RE = /^(0|[1-9][0-9]*)$/;

/**
 * 引数を検査する。知らない名前・重複・整数でない値・範囲外はエラー。`ignore` の名前は見ない。
 * 既定値のある引数は、省略されたら既定値を入れる（既定値のない引数は省略されたら入れない）。
 */
export function parseParams(
  params: URLSearchParams,
  specs: ParamSpecs,
  ignore: readonly string[] = [],
): ParseResult {
  const seen = new Set<string>();
  const values: ParamValues = {};
  for (const [name, raw] of params) {
    if (ignore.includes(name)) continue;
    const spec = Object.hasOwn(specs, name) ? specs[name] : undefined;
    if (spec === undefined) return { ok: false, message: `unknown parameter: ${name}` };
    if (seen.has(name)) return { ok: false, message: `duplicate parameter: ${name}` };
    seen.add(name);
    if (spec.kind === "int") {
      if (!INT_RE.test(raw) || raw.length > 10) return { ok: false, message: `${name} must be an integer` };
      const n = Number(raw);
      if (n < spec.min || n > spec.max) {
        return { ok: false, message: `${name} must be in ${spec.min}..${spec.max}` };
      }
      values[name] = n;
    } else {
      if (!spec.values.includes(raw)) {
        return { ok: false, message: `${name} must be one of ${spec.values.join(", ")}` };
      }
      values[name] = raw;
    }
  }
  for (const [name, spec] of Object.entries(specs)) {
    if (seen.has(name)) continue;
    if (spec.required === true) return { ok: false, message: `missing parameter: ${name}` };
    if (spec.default !== undefined) values[name] = spec.default;
  }
  return { ok: true, values };
}

/** SPIKE_MAX_SESSIONS を解釈する。不正なら null（すべての /spike/s/* を 404 にする） */
export function parseMaxSessions(raw: string | undefined): number | null {
  const v = raw ?? String(MAX_SESSIONS_DEFAULT);
  if (!INT_RE.test(v)) return null;
  const n = Number(v);
  return n >= MAX_SESSIONS_MIN && n <= MAX_SESSIONS_MAX ? n : null;
}

/** 番号（`^[1-9][0-9]*$`）が 1〜max か。違えば null */
export function parseIndex(raw: string, max: number): number | null {
  if (!/^[1-9][0-9]*$/.test(raw)) return null;
  const n = Number(raw);
  return n <= max ? n : null;
}
