// GameSession の SQLite の表と読み書き（T-308・DesignDoc §12.3）。
//
// session は 1 行だけ（id = 1）。stage_hash・build・last_clock_t と input_log は T-308b の入力ログと復元が
// 使う。列の追加・削除は、T-601 で初めてデプロイする前なら CREATE TABLE を直すだけでよい（移行を書かない）。

import type { SqlStorageLike, SqlValue } from "../admin/cf-types.ts";

const SCHEMA = [
  `CREATE TABLE IF NOT EXISTS session (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    session_id           TEXT    NOT NULL,
    stage_id             TEXT    NOT NULL,
    seed                 INTEGER NOT NULL,
    demo                 INTEGER NOT NULL,
    token_hash           TEXT    NOT NULL,
    state                TEXT    NOT NULL,
    created_at           INTEGER NOT NULL,
    deadline_at          INTEGER NOT NULL,
    max_alarms           INTEGER NOT NULL,
    alarm_count          INTEGER NOT NULL DEFAULT 0,
    alarm_at             INTEGER,
    idle_since           INTEGER,
    container_starts     INTEGER NOT NULL DEFAULT 0,
    container_started_at INTEGER,
    ready_at             INTEGER,
    run_ms               INTEGER NOT NULL DEFAULT 0,
    stage_hash           TEXT,
    build                TEXT,
    last_clock_t         INTEGER,
    end_reason           TEXT,
    ended_at             INTEGER,
    settled              INTEGER NOT NULL DEFAULT 0,
    result_json          TEXT,
    result_at            INTEGER
  )`,
  `CREATE TABLE IF NOT EXISTS input_log (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    t   INTEGER NOT NULL,
    cmd TEXT    NOT NULL
  )`,
] as const;

export function ensureSessionSchema(sql: SqlStorageLike): void {
  for (const statement of SCHEMA) sql.exec(statement);
}

export type SessionState = "starting" | "running" | "ended";

export interface SessionRow {
  readonly sessionId: string;
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly tokenHash: string; // SHA-256 の 16 進の小文字（トークンそのものは保存しない）
  readonly state: SessionState;
  readonly createdAt: number;
  readonly deadlineAt: number;
  readonly maxAlarms: number;
  readonly alarmCount: number;
  readonly alarmAt: number | null;
  readonly idleSince: number | null;
  readonly containerStarts: number;
  readonly containerStartedAt: number | null;
  readonly readyAt: number | null;
  readonly runMs: number;
  readonly stageHash: string | null;
  readonly build: string | null;
  readonly lastClockT: number | null;
  readonly endReason: string | null;
  readonly endedAt: number | null;
  readonly settled: boolean;
  readonly resultJson: string | null;
  readonly resultAt: number | null;
}

// 列の名前（キーは固定。SQL に入れるのはこの表の値だけ）。
const COLUMNS = {
  sessionId: "session_id",
  stageId: "stage_id",
  seed: "seed",
  demo: "demo",
  tokenHash: "token_hash",
  state: "state",
  createdAt: "created_at",
  deadlineAt: "deadline_at",
  maxAlarms: "max_alarms",
  alarmCount: "alarm_count",
  alarmAt: "alarm_at",
  idleSince: "idle_since",
  containerStarts: "container_starts",
  containerStartedAt: "container_started_at",
  readyAt: "ready_at",
  runMs: "run_ms",
  stageHash: "stage_hash",
  build: "build",
  lastClockT: "last_clock_t",
  endReason: "end_reason",
  endedAt: "ended_at",
  settled: "settled",
  resultJson: "result_json",
  resultAt: "result_at",
} as const satisfies Record<keyof SessionRow, string>;

type Column = keyof SessionRow;

const COLUMN_NAMES: ReadonlyMap<string, string> = new Map(Object.entries(COLUMNS));

function toSql(value: unknown): SqlValue {
  if (typeof value === "boolean") return value ? 1 : 0;
  if (value === null || typeof value === "string" || typeof value === "number") return value;
  throw new TypeError("session: unsupported value");
}

function num(value: SqlValue | undefined): number {
  return typeof value === "number" ? value : Number(value ?? 0);
}

function numOrNull(value: SqlValue | undefined): number | null {
  return value === null || value === undefined ? null : num(value);
}

function strOrNull(value: SqlValue | undefined): string | null {
  return value === null || value === undefined ? null : String(value);
}

function stateOf(value: SqlValue | undefined): SessionState {
  return value === "starting" || value === "running" ? value : "ended";
}

export function readSession(sql: SqlStorageLike): SessionRow | null {
  const [row] = sql.exec("SELECT * FROM session WHERE id = 1").toArray();
  if (row === undefined) return null;
  return {
    sessionId: String(row["session_id"]),
    stageId: String(row["stage_id"]),
    seed: num(row["seed"]),
    demo: num(row["demo"]) === 1,
    tokenHash: String(row["token_hash"]),
    state: stateOf(row["state"]),
    createdAt: num(row["created_at"]),
    deadlineAt: num(row["deadline_at"]),
    maxAlarms: num(row["max_alarms"]),
    alarmCount: num(row["alarm_count"]),
    alarmAt: numOrNull(row["alarm_at"]),
    idleSince: numOrNull(row["idle_since"]),
    containerStarts: num(row["container_starts"]),
    containerStartedAt: numOrNull(row["container_started_at"]),
    readyAt: numOrNull(row["ready_at"]),
    runMs: num(row["run_ms"]),
    stageHash: strOrNull(row["stage_hash"]),
    build: strOrNull(row["build"]),
    lastClockT: numOrNull(row["last_clock_t"]),
    endReason: strOrNull(row["end_reason"]),
    endedAt: numOrNull(row["ended_at"]),
    settled: num(row["settled"]) === 1,
    resultJson: strOrNull(row["result_json"]),
    resultAt: numOrNull(row["result_at"]),
  };
}

export interface NewSession {
  readonly sessionId: string;
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly tokenHash: string;
  readonly createdAt: number;
  readonly deadlineAt: number;
  readonly maxAlarms: number;
  readonly idleSince: number;
}

export function insertSession(sql: SqlStorageLike, init: NewSession): void {
  sql.exec(
    `INSERT INTO session
       (id, session_id, stage_id, seed, demo, token_hash, state, created_at, deadline_at, max_alarms, idle_since)
     VALUES (1, ?, ?, ?, ?, ?, 'starting', ?, ?, ?, ?)`,
    init.sessionId,
    init.stageId,
    init.seed,
    init.demo ? 1 : 0,
    init.tokenHash,
    init.createdAt,
    init.deadlineAt,
    init.maxAlarms,
    init.idleSince,
  );
}

export type SessionPatch = { readonly [K in Column]?: SessionRow[K] };

export function updateSession(sql: SqlStorageLike, patch: SessionPatch): void {
  const sets: string[] = [];
  const values: SqlValue[] = [];
  for (const [key, value] of Object.entries(patch)) {
    const column = COLUMN_NAMES.get(key);
    if (column === undefined || value === undefined) continue;
    sets.push(`${column} = ?`);
    values.push(toSql(value));
  }
  if (sets.length === 0) return;
  sql.exec(`UPDATE session SET ${sets.join(", ")} WHERE id = 1`, ...values);
}

export function countInputLog(sql: SqlStorageLike): number {
  const [row] = sql.exec("SELECT COUNT(*) AS n FROM input_log").toArray();
  return num(row?.["n"]);
}

// 入力ログに 1 行足す（cmd は {"type":…,"payload":…} の JSON）。
export function appendInputLog(sql: SqlStorageLike, t: number, cmd: string): void {
  sql.exec("INSERT INTO input_log (t, cmd) VALUES (?, ?)", t, cmd);
}

// seq の順。
export function readInputLog(sql: SqlStorageLike): { readonly t: number; readonly cmd: string }[] {
  return sql
    .exec("SELECT t, cmd FROM input_log ORDER BY seq")
    .toArray()
    .map((row) => ({ t: num(row["t"]), cmd: String(row["cmd"]) }));
}

export interface InputLogStats {
  readonly rows: number;
  readonly bytes: number; // cmd の UTF-8 のバイト数の合計
  readonly lastT: number | null; // 最後の行の t
}

export function inputLogStats(sql: SqlStorageLike): InputLogStats {
  const [totals] = sql
    .exec("SELECT COUNT(*) AS n, COALESCE(SUM(LENGTH(CAST(cmd AS BLOB))), 0) AS b FROM input_log")
    .toArray();
  const [last] = sql.exec("SELECT t FROM input_log ORDER BY seq DESC LIMIT 1").toArray();
  return {
    rows: num(totals?.["n"]),
    bytes: num(totals?.["b"]),
    lastT: numOrNull(last?.["t"]),
  };
}
