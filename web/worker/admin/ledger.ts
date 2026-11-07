// CostGuard の中身（T-309・ADR-0018 G1・G6・DesignDoc §12.3.1）。
//
// 費用の帳簿（予約・取り消し・精算・1 日と 1 か月の合計）と、設定の上書きと記録。
// SQLite（ctx.storage.sql）の上の同期の処理だけで、await を挟まない（Durable Object の中で
// 直列になるので、上限の確認と行の書き込みの間に、ほかの要求が割り込まない）。
// 時刻は引数の now() で受ける。金額はすべて µUSD の整数。1 日・1 か月は UTC の暦で区切る。

import type { SqlStorageLike, SqlValue } from "./cf-types.ts";
import {
  buildGuardsView,
  overlayGuards,
  parseFlatRecord,
  parseReason,
  toStoredValue,
  validateFlatChange,
  type ContainerInstance,
  type EffectiveGuards,
  type FlatGuardsChange,
  type GuardsChangeRecord,
  type GuardsConfig,
  type GuardsView,
} from "./guards.ts";

export type ReserveResult = "ok" | "budget_exhausted" | "sessions_disabled";

export type CorePatchResult =
  | { readonly ok: true; readonly view: GuardsView }
  | { readonly ok: false; readonly code: "bad_request" | "locked" | "guards_invalid" };

export type TransactionSync = <T>(fn: () => T) => T;

export const MAX_RUN_SECONDS = 86_400;
export const MAX_AUDIT_ROWS = 1000;
export const RECENT_CHANGES = 10;
const MAX_BY_LENGTH = 64;
const MINUTE_MS = 60_000;

// sql.exec には 1 回に 1 つの文だけを渡す。
const SCHEMA: readonly string[] = [
  `CREATE TABLE IF NOT EXISTS ledger (
    session_id     TEXT PRIMARY KEY,
    instance       TEXT NOT NULL,
    rate_micro     INTEGER NOT NULL,
    reserved_micro INTEGER NOT NULL,
    settled_micro  INTEGER,
    status         TEXT NOT NULL,
    started_at     INTEGER NOT NULL,
    ended_at       INTEGER
  )`,
  "CREATE INDEX IF NOT EXISTS ledger_started_at ON ledger (started_at)",
  "CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
  `CREATE TABLE IF NOT EXISTS audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    at INTEGER NOT NULL,
    by TEXT NOT NULL,
    change TEXT NOT NULL,
    reason TEXT NOT NULL
  )`,
];

export function ensureSchema(sql: SqlStorageLike): void {
  for (const statement of SCHEMA) sql.exec(statement);
}

function utcDayStart(now: number): number {
  const d = new Date(now);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
}

function utcMonthStart(now: number, monthOffset = 0): number {
  const d = new Date(now);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + monthOffset, 1);
}

function asNumber(value: SqlValue | undefined): number {
  if (typeof value !== "number") throw new Error("CostGuard: unexpected SQL value");
  return value;
}

// 予約額 = ceil(30 分あたりの単価 × maxMinutes / 30)
export function reservationMicro(rateMicro: number, maxMinutes: number): number {
  return Math.ceil((rateMicro * maxMinutes) / 30);
}

// 精算額 = ceil(30 分あたりの単価 × runSeconds / 1800)（予約額を超えても切らない）
export function settlementMicro(rateMicro: number, runSeconds: number): number {
  return Math.ceil((rateMicro * runSeconds) / 1800);
}

export function isValidRunSeconds(value: unknown): value is number {
  return (
    typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= MAX_RUN_SECONDS
  );
}

export function isValidBy(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const length = [...value].length;
  return length >= 1 && length <= MAX_BY_LENGTH;
}

export class CostGuardCore {
  private readonly sql: SqlStorageLike;
  private readonly transactionSync: TransactionSync;
  private readonly config: GuardsConfig | null;
  private readonly now: () => number;

  constructor(
    sql: SqlStorageLike,
    transactionSync: TransactionSync,
    config: GuardsConfig | null,
    now: () => number,
  ) {
    this.sql = sql;
    this.transactionSync = transactionSync;
    this.config = config;
    this.now = now;
  }

  // 設定が不正（GUARDS が null）なら null。変更は次の要求からすぐに効く（キャッシュしない）。
  private effective(): EffectiveGuards | null {
    if (this.config === null) return null;
    const stored = new Map<string, unknown>();
    for (const row of this.sql.exec("SELECT key, value FROM settings").toArray()) {
      const key = row["key"];
      const value = row["value"];
      if (typeof key !== "string" || typeof value !== "string") continue;
      try {
        stored.set(key, JSON.parse(value) as unknown);
      } catch {
        // 壊れた上書きは無視して既定値を使う
      }
    }
    return overlayGuards(this.config, stored);
  }

  // started_at が since 以降の行の合計（cancelled は 0、settled は精算額、reserved は予約額）。
  private totalSince(since: number): number {
    const [row] = this.sql
      .exec(
        `SELECT COALESCE(SUM(CASE status
           WHEN 'cancelled' THEN 0
           WHEN 'settled' THEN settled_micro
           ELSE reserved_micro END), 0) AS total
         FROM ledger WHERE started_at >= ?`,
        since,
      )
      .toArray();
    return asNumber(row?.["total"]);
  }

  reserve(sessionId: string, instance: ContainerInstance): ReserveResult {
    return this.transactionSync(() => {
      // 1. 設定が不正なら拒否する側に倒す
      const guards = this.effective();
      if (guards === null) return "sessions_disabled";

      // 2. 同じ sessionId の行がすでにある（冪等）
      const existing = this.sql
        .exec("SELECT session_id FROM ledger WHERE session_id = ?", sessionId)
        .toArray();
      if (existing.length > 0) return "ok";

      // 3. 受付の一括停止（G6。予算より先に判定）
      if (!guards.acceptGames) return "sessions_disabled";

      // 4. 予算（G1）。ちょうど上限に等しいのは通す
      const now = this.now();
      const rateMicro = guards.unitMicro[instance];
      const reservedMicro = reservationMicro(rateMicro, guards.gameTime.maxMinutes);
      if (guards.budget.enabled) {
        const today = this.totalSince(utcDayStart(now));
        const month = this.totalSince(utcMonthStart(now));
        if (
          today + reservedMicro > guards.budget.dailyMicro ||
          month + reservedMicro > guards.budget.monthlyMicro
        ) {
          return "budget_exhausted";
        }
      }

      // 5. 行を足し、先月の 1 日（UTC）より前の行を片付ける
      this.sql.exec(
        `INSERT INTO ledger (session_id, instance, rate_micro, reserved_micro, settled_micro,
           status, started_at, ended_at)
         VALUES (?, ?, ?, ?, NULL, 'reserved', ?, NULL)`,
        sessionId,
        instance,
        rateMicro,
        reservedMicro,
        now,
      );
      this.sql.exec("DELETE FROM ledger WHERE started_at < ?", utcMonthStart(now, -1));
      return "ok";
    });
  }

  // 未精算の予約だけを取り消す（精算済みの実際の費用は消さない）。
  cancel(sessionId: string): void {
    this.sql.exec(
      `UPDATE ledger SET status = 'cancelled', ended_at = ?
       WHERE session_id = ? AND status = 'reserved'`,
      this.now(),
      sessionId,
    );
  }

  // 未精算の予約だけを、予約したときの単価と実際に動いた秒数で精算する（冪等）。
  settle(sessionId: string, runSeconds: number): void {
    if (!isValidRunSeconds(runSeconds)) throw new RangeError("runSeconds is out of range");
    const [row] = this.sql
      .exec("SELECT rate_micro FROM ledger WHERE session_id = ? AND status = 'reserved'", sessionId)
      .toArray();
    if (row === undefined) return;
    this.sql.exec(
      `UPDATE ledger SET status = 'settled', settled_micro = ?, ended_at = ?
       WHERE session_id = ? AND status = 'reserved'`,
      settlementMicro(asNumber(row["rate_micro"]), runSeconds),
      this.now(),
      sessionId,
    );
  }

  view(): GuardsView | "guards_invalid" {
    const guards = this.effective();
    if (guards === null) return "guards_invalid";
    return this.buildView(guards);
  }

  patch(change: unknown, reason: unknown, by: unknown): CorePatchResult {
    const checked = validateFlatChange(change);
    if (!checked.ok) return checked;
    const trimmedReason = parseReason(reason);
    if (trimmedReason === null || !isValidBy(by)) return { ok: false, code: "bad_request" };
    if (this.config === null) return { ok: false, code: "guards_invalid" };

    return this.transactionSync(() => {
      this.writeSettings(checked.change);
      this.sql.exec(
        "INSERT INTO audit (at, by, change, reason) VALUES (?, ?, ?, ?)",
        this.now(),
        by,
        JSON.stringify(checked.change),
        trimmedReason,
      );
      this.sql.exec(
        "DELETE FROM audit WHERE id NOT IN (SELECT id FROM audit ORDER BY id DESC LIMIT ?)",
        MAX_AUDIT_ROWS,
      );
      const guards = this.effective();
      if (guards === null) throw new Error("CostGuard: guards disappeared");
      return { ok: true, view: this.buildView(guards) } as const;
    });
  }

  private writeSettings(change: FlatGuardsChange): void {
    for (const key of Object.keys(change)) {
      const value = change[key];
      if (value === undefined) continue;
      if (value === null) {
        this.sql.exec("DELETE FROM settings WHERE key = ?", key);
      } else {
        this.sql.exec(
          "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
          key,
          JSON.stringify(toStoredValue(key, value)),
        );
      }
    }
  }

  private buildView(guards: EffectiveGuards): GuardsView {
    const now = this.now();
    const [active] = this.sql
      .exec(
        "SELECT COUNT(*) AS n FROM ledger WHERE status = 'reserved' AND started_at >= ?",
        now - guards.gameTime.maxMinutes * MINUTE_MS,
      )
      .toArray();
    return buildGuardsView(
      guards,
      {
        todayMicro: this.totalSince(utcDayStart(now)),
        monthMicro: this.totalSince(utcMonthStart(now)),
        activeGames: asNumber(active?.["n"]),
      },
      this.recentChanges(),
    );
  }

  private recentChanges(): GuardsChangeRecord[] {
    const rows = this.sql
      .exec("SELECT at, change, reason FROM audit ORDER BY id DESC LIMIT ?", RECENT_CHANGES)
      .toArray();
    const records: GuardsChangeRecord[] = [];
    for (const row of rows) {
      const at = row["at"];
      const change = row["change"];
      const reason = row["reason"];
      if (typeof at !== "number" || typeof change !== "string" || typeof reason !== "string") {
        continue;
      }
      let parsed: FlatGuardsChange | null;
      try {
        parsed = parseFlatRecord(JSON.parse(change) as unknown);
      } catch {
        parsed = null;
      }
      records.push({ at: new Date(at).toISOString(), change: parsed ?? {}, reason });
    }
    return records;
  }
}
