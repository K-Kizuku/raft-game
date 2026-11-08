// コストガードの設定（T-309・ADR-0018・DesignDoc §12.3.1・プロトコル §2）。
//
// - GUARDS（cloudflare.config.ts の JSON のバインディング）の読み込みと検証
// - PATCH /api/admin/guards の本文の検証と、平らな形（"budget.enabled" など）への変換
// - 保存された上書きを GUARDS に重ねた「今の設定」と、表示用の形 GuardsView
//
// G3（signature）は設定の対象にしない。GUARDS に signature があれば設定ごと不正とし、
// PATCH に signature があれば locked にする。

import { MICRO_PER_USD, microToUsd, usdToMicro } from "./money.ts";

export type ContainerInstance = "lite" | "standard-1";

type JsonObject = Readonly<Record<string, unknown>>;

export function isPlainObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(obj: JsonObject, keys: readonly string[]): boolean {
  const actual = Object.keys(obj);
  return actual.length === keys.length && keys.every((key) => Object.hasOwn(obj, key));
}

function hasKeys(obj: JsonObject, keys: readonly string[]): boolean {
  return keys.every((key) => Object.hasOwn(obj, key));
}

function isIntIn(value: unknown, min: number, max: number): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= min && value <= max;
}

function isFiniteIn(value: unknown, min: number, max: number): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= min && value <= max;
}

const isBool = (value: unknown): value is boolean => typeof value === "boolean";
const isBudgetUsd = (value: unknown): value is number => isFiniteIn(value, 0, 1000);
const isMaxMinutes = (value: unknown): value is number => isIntIn(value, 1, 60);
const isMaxAlarms = (value: unknown): value is number => isIntIn(value, 1, 100);
const isPerIpPerMinute = (value: unknown): value is number => isIntIn(value, 1, 1000);
const isCpuMs = (value: unknown): value is number => isIntIn(value, 1, 300_000);
const isUnitCostUsd = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && value > 0 && value <= 10;

// ---------------------------------------------------------------------------
// GUARDS

export interface GuardsConfig {
  readonly budget: {
    readonly enabled: boolean;
    readonly monthlyUsd: number;
    readonly dailyUsd: number;
  };
  readonly gameTime: { readonly maxMinutes: number; readonly maxAlarms: number };
  readonly turnstile: { readonly enabled: boolean };
  readonly rateLimit: { readonly enabled: boolean; readonly perIpPerMinute: number };
  readonly cpuMs: number;
  readonly acceptGames: boolean;
  readonly unitCostUsd: UnitCostUsd;
}

// lite・standard-1 は「1 ゲーム = コンテナ 1 つ＋ Durable Object 1 つ」の 30 分の単価（今の {sessionId, instance} の予約）。
// container.<instance>・do は内訳つきの予約（{sessionId, units}。DTN-12・docs/dtn/environment.md §3）の種類ごとの単価。
// 内訳の単価は書かなくてよい。書いていない種類を内訳で予約すると、CostGuard は拒否する（sessions_disabled）。
export interface UnitCostUsd {
  readonly lite: number;
  readonly "standard-1": number;
  readonly "container.lite"?: number;
  readonly "container.standard-1"?: number;
  readonly do?: number;
}

export const UNIT_PART_KEYS = ["container.lite", "container.standard-1", "do"] as const;
export type UnitPartKey = (typeof UNIT_PART_KEYS)[number];

const CONFIG_KEYS = [
  "budget",
  "gameTime",
  "turnstile",
  "rateLimit",
  "cpuMs",
  "acceptGames",
  "unitCostUsd",
] as const;

function exactObject(value: unknown, keys: readonly string[]): JsonObject | null {
  return isPlainObject(value) && hasExactKeys(value, keys) ? value : null;
}

// keys をすべて持ち、ほかのキーは optional のどれかであるオブジェクト。
function objectWith(
  value: unknown,
  keys: readonly string[],
  optional: readonly string[],
): JsonObject | null {
  if (!isPlainObject(value) || !hasKeys(value, keys)) return null;
  return Object.keys(value).every((key) => keys.includes(key) || optional.includes(key))
    ? value
    : null;
}

// 不正（未設定・JSON でない・規則に合わない・未知のキーや signature がある）なら null。
export function parseGuardsConfig(raw: unknown): GuardsConfig | null {
  let value = raw;
  if (typeof raw === "string") {
    try {
      value = JSON.parse(raw) as unknown;
    } catch {
      return null;
    }
  }
  const top = exactObject(value, CONFIG_KEYS);
  if (top === null) return null;

  const budget = exactObject(top["budget"], ["enabled", "monthlyUsd", "dailyUsd"]);
  const gameTime = exactObject(top["gameTime"], ["maxMinutes", "maxAlarms"]);
  const turnstile = exactObject(top["turnstile"], ["enabled"]);
  const rateLimit = exactObject(top["rateLimit"], ["enabled", "perIpPerMinute"]);
  const unitCostUsd = objectWith(top["unitCostUsd"], ["lite", "standard-1"], UNIT_PART_KEYS);
  if (
    budget === null ||
    gameTime === null ||
    turnstile === null ||
    rateLimit === null ||
    unitCostUsd === null
  ) {
    return null;
  }

  const budgetEnabled = budget["enabled"];
  const monthlyUsd = budget["monthlyUsd"];
  const dailyUsd = budget["dailyUsd"];
  const maxMinutes = gameTime["maxMinutes"];
  const maxAlarms = gameTime["maxAlarms"];
  const turnstileEnabled = turnstile["enabled"];
  const rateLimitEnabled = rateLimit["enabled"];
  const perIpPerMinute = rateLimit["perIpPerMinute"];
  const cpuMs = top["cpuMs"];
  const acceptGames = top["acceptGames"];
  const lite = unitCostUsd["lite"];
  const standard1 = unitCostUsd["standard-1"];
  if (
    !isBool(budgetEnabled) ||
    !isBudgetUsd(monthlyUsd) ||
    !isBudgetUsd(dailyUsd) ||
    !isMaxMinutes(maxMinutes) ||
    !isMaxAlarms(maxAlarms) ||
    !isBool(turnstileEnabled) ||
    !isBool(rateLimitEnabled) ||
    !isPerIpPerMinute(perIpPerMinute) ||
    !isCpuMs(cpuMs) ||
    !isBool(acceptGames) ||
    !isUnitCostUsd(lite) ||
    !isUnitCostUsd(standard1)
  ) {
    return null;
  }
  const parts: Partial<Record<UnitPartKey, number>> = {};
  for (const key of UNIT_PART_KEYS) {
    if (!Object.hasOwn(unitCostUsd, key)) continue;
    const value = unitCostUsd[key];
    if (!isUnitCostUsd(value)) return null;
    parts[key] = value;
  }

  return {
    budget: { enabled: budgetEnabled, monthlyUsd, dailyUsd },
    gameTime: { maxMinutes, maxAlarms },
    turnstile: { enabled: turnstileEnabled },
    rateLimit: { enabled: rateLimitEnabled, perIpPerMinute },
    cpuMs,
    acceptGames,
    unitCostUsd: { lite, "standard-1": standard1, ...parts },
  };
}

// ---------------------------------------------------------------------------
// PATCH の本文

export type FlatGuardsChange = Readonly<Record<string, boolean | number | null>>;

export type GuardsPatchError = "bad_request" | "locked";

export type FlatChangeCheck =
  | { readonly ok: true; readonly change: FlatGuardsChange }
  | { readonly ok: false; readonly code: GuardsPatchError };

export type GuardsPatchCheck =
  | { readonly ok: true; readonly change: FlatGuardsChange; readonly reason: string }
  | { readonly ok: false; readonly code: GuardsPatchError };

interface PatchField {
  readonly key: string; // 平らな形のキー（settings の key と同じ）
  readonly valid: (value: unknown) => boolean;
  readonly money: boolean; // true なら µUSD の整数で保存する
}

// 変えられる項目。この順序で平らな形を作る。
const PATCH_FIELDS: readonly PatchField[] = [
  { key: "budget.enabled", valid: isBool, money: false },
  { key: "budget.monthlyUsd", valid: isBudgetUsd, money: true },
  { key: "budget.dailyUsd", valid: isBudgetUsd, money: true },
  { key: "gameTime.maxMinutes", valid: isMaxMinutes, money: false },
  { key: "turnstile.enabled", valid: isBool, money: false },
  { key: "rateLimit.enabled", valid: isBool, money: false },
  { key: "rateLimit.perIpPerMinute", valid: isPerIpPerMinute, money: false },
  { key: "acceptGames", valid: isBool, money: false },
];

const PATCH_FIELD_KEYS: ReadonlySet<string> = new Set(PATCH_FIELDS.map((field) => field.key));
const PATCH_GROUPS: ReadonlySet<string> = new Set(["budget", "gameTime", "turnstile", "rateLimit"]);
const MAX_REASON_LENGTH = 200;

const BAD_REQUEST = { ok: false, code: "bad_request" } as const;
const LOCKED = { ok: false, code: "locked" } as const;

// 変えられない項目（G3 の signature、再デプロイが要る cpuMs、G2 の gameTime.enabled・maxAlarms）。
function isLockedFlatKey(key: string): boolean {
  return (
    key === "signature" ||
    key.startsWith("signature.") ||
    key === "cpuMs" ||
    key.startsWith("cpuMs.") ||
    key === "gameTime.enabled" ||
    key === "gameTime.maxAlarms"
  );
}

// 平らな形の変更を確かめる（CostGuard の /patch が、Worker を通らない呼び出しに備えて使う）。
export function validateFlatChange(raw: unknown): FlatChangeCheck {
  if (!isPlainObject(raw)) return BAD_REQUEST;
  const keys = Object.keys(raw);
  if (keys.some(isLockedFlatKey)) return LOCKED;
  if (keys.some((key) => !PATCH_FIELD_KEYS.has(key))) return BAD_REQUEST;

  const change: Record<string, boolean | number | null> = {};
  for (const field of PATCH_FIELDS) {
    if (!Object.hasOwn(raw, field.key)) continue;
    const value = raw[field.key];
    if (value === null) {
      change[field.key] = null;
    } else if ((typeof value === "boolean" || typeof value === "number") && field.valid(value)) {
      change[field.key] = value;
    } else {
      return BAD_REQUEST;
    }
  }
  if (Object.keys(change).length === 0) return BAD_REQUEST;
  return { ok: true, change };
}

// 前後の空白を除いて 1〜200 文字の文字列。合わなければ null。
export function parseReason(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  const reason = raw.trim();
  const length = [...reason].length;
  return length >= 1 && length <= MAX_REASON_LENGTH ? reason : null;
}

export function parseGuardsPatch(body: unknown): GuardsPatchCheck {
  // 1. JSON のオブジェクトでない
  if (!isPlainObject(body)) return BAD_REQUEST;

  // 2. 変えられない項目（ほかの誤りより先）
  if (Object.hasOwn(body, "signature") || Object.hasOwn(body, "cpuMs")) return LOCKED;
  const gameTime = body["gameTime"];
  if (
    isPlainObject(gameTime) &&
    (Object.hasOwn(gameTime, "enabled") || Object.hasOwn(gameTime, "maxAlarms"))
  ) {
    return LOCKED;
  }

  // 3. 未知のキー・型や範囲の違い
  const flat: Record<string, unknown> = {};
  for (const key of Object.keys(body)) {
    if (key === "reason") continue;
    const value = body[key];
    if (key === "acceptGames") {
      flat[key] = value;
      continue;
    }
    if (!PATCH_GROUPS.has(key) || !isPlainObject(value)) return BAD_REQUEST;
    for (const name of Object.keys(value)) {
      flat[`${key}.${name}`] = value[name];
    }
  }
  const hasChange = Object.keys(flat).length > 0;
  const checked = hasChange ? validateFlatChange(flat) : null;
  if (checked !== null && !checked.ok) return checked;

  // 4. reason
  const reason = parseReason(body["reason"]);
  if (reason === null) return BAD_REQUEST;

  // 5. 変える項目がない
  if (checked === null) return BAD_REQUEST;

  return { ok: true, change: checked.change, reason };
}

// 平らな変更の値を settings に保存する形にする（金額は µUSD の整数）。
export function toStoredValue(key: string, value: boolean | number): boolean | number {
  const field = PATCH_FIELDS.find((candidate) => candidate.key === key);
  if (field?.money === true && typeof value === "number") return usdToMicro(value);
  return value;
}

// ---------------------------------------------------------------------------
// 今の設定

export interface EffectiveGuards {
  readonly budget: {
    readonly enabled: boolean;
    readonly monthlyMicro: number;
    readonly dailyMicro: number;
  };
  readonly gameTime: { readonly maxMinutes: number; readonly maxAlarms: number };
  readonly turnstile: { readonly enabled: boolean };
  readonly rateLimit: { readonly enabled: boolean; readonly perIpPerMinute: number };
  readonly cpuMs: number;
  readonly acceptGames: boolean;
  readonly unitMicro: Readonly<Record<ContainerInstance, number>>;
  readonly unitPartMicro: Readonly<Partial<Record<UnitPartKey, number>>>; // 内訳の単価（書いたものだけ）
}

const MAX_BUDGET_MICRO = 1000 * MICRO_PER_USD;

// GUARDS に保存された上書き（settings の key → JSON.parse した値）を重ねる。
// 保存された値が今の規則で不正なら、その上書きだけを無視して既定値を使う。
export function overlayGuards(
  config: GuardsConfig,
  stored: ReadonlyMap<string, unknown>,
): EffectiveGuards {
  const bool = (key: string, fallback: boolean): boolean => {
    const value = stored.get(key);
    return isBool(value) ? value : fallback;
  };
  const int = (key: string, min: number, max: number, fallback: number): number => {
    const value = stored.get(key);
    return isIntIn(value, min, max) ? value : fallback;
  };
  return {
    budget: {
      enabled: bool("budget.enabled", config.budget.enabled),
      monthlyMicro: int(
        "budget.monthlyUsd",
        0,
        MAX_BUDGET_MICRO,
        usdToMicro(config.budget.monthlyUsd),
      ),
      dailyMicro: int("budget.dailyUsd", 0, MAX_BUDGET_MICRO, usdToMicro(config.budget.dailyUsd)),
    },
    gameTime: {
      maxMinutes: int("gameTime.maxMinutes", 1, 60, config.gameTime.maxMinutes),
      maxAlarms: config.gameTime.maxAlarms,
    },
    turnstile: { enabled: bool("turnstile.enabled", config.turnstile.enabled) },
    rateLimit: {
      enabled: bool("rateLimit.enabled", config.rateLimit.enabled),
      perIpPerMinute: int("rateLimit.perIpPerMinute", 1, 1000, config.rateLimit.perIpPerMinute),
    },
    cpuMs: config.cpuMs,
    acceptGames: bool("acceptGames", config.acceptGames),
    unitMicro: {
      lite: usdToMicro(config.unitCostUsd.lite),
      "standard-1": usdToMicro(config.unitCostUsd["standard-1"]),
    },
    unitPartMicro: partMicro(config.unitCostUsd),
  };
}

function partMicro(unitCostUsd: UnitCostUsd): Partial<Record<UnitPartKey, number>> {
  const out: Partial<Record<UnitPartKey, number>> = {};
  for (const key of UNIT_PART_KEYS) {
    const value = unitCostUsd[key];
    if (value !== undefined) out[key] = usdToMicro(value);
  }
  return out;
}

// ---------------------------------------------------------------------------
// 表示用の形（GET・PATCH の応答）

export const CPU_MS_NOTE = "変更には再デプロイが必要";

export interface GuardsChangeRecord {
  readonly at: string; // ISO 8601（UTC）
  readonly change: FlatGuardsChange;
  readonly reason: string;
}

export interface GuardsView {
  readonly guards: {
    readonly budget: {
      readonly enabled: boolean;
      readonly monthlyUsd: number;
      readonly dailyUsd: number;
    };
    readonly gameTime: {
      readonly enabled: true;
      readonly maxMinutes: number;
      readonly maxAlarms: number;
    };
    readonly signature: { readonly enabled: true; readonly locked: true };
    readonly turnstile: { readonly enabled: boolean };
    readonly rateLimit: { readonly enabled: boolean; readonly perIpPerMinute: number };
    readonly cpuMs: { readonly value: number; readonly note: string };
    readonly acceptGames: boolean;
  };
  readonly usage: {
    readonly todayUsd: number;
    readonly monthUsd: number;
    readonly activeGames: number;
  };
  readonly unitCostUsd: { readonly lite: number; readonly "standard-1": number };
  readonly recentChanges: readonly GuardsChangeRecord[];
}

export interface GuardsUsage {
  readonly todayMicro: number;
  readonly monthMicro: number;
  readonly activeGames: number;
}

// キーの順序はプロトコル §2 の例のとおり。
export function buildGuardsView(
  guards: EffectiveGuards,
  usage: GuardsUsage,
  recentChanges: readonly GuardsChangeRecord[],
): GuardsView {
  return {
    guards: {
      budget: {
        enabled: guards.budget.enabled,
        monthlyUsd: microToUsd(guards.budget.monthlyMicro),
        dailyUsd: microToUsd(guards.budget.dailyMicro),
      },
      gameTime: {
        enabled: true,
        maxMinutes: guards.gameTime.maxMinutes,
        maxAlarms: guards.gameTime.maxAlarms,
      },
      signature: { enabled: true, locked: true },
      turnstile: { enabled: guards.turnstile.enabled },
      rateLimit: {
        enabled: guards.rateLimit.enabled,
        perIpPerMinute: guards.rateLimit.perIpPerMinute,
      },
      cpuMs: { value: guards.cpuMs, note: CPU_MS_NOTE },
      acceptGames: guards.acceptGames,
    },
    usage: {
      todayUsd: microToUsd(usage.todayMicro),
      monthUsd: microToUsd(usage.monthMicro),
      activeGames: usage.activeGames,
    },
    unitCostUsd: {
      lite: microToUsd(guards.unitMicro.lite),
      "standard-1": microToUsd(guards.unitMicro["standard-1"]),
    },
    recentChanges,
  };
}

// 値が真偽値・数・null だけのオブジェクトなら、平らな変更として返す。
export function parseFlatRecord(raw: unknown): FlatGuardsChange | null {
  if (!isPlainObject(raw)) return null;
  const change: Record<string, boolean | number | null> = {};
  for (const key of Object.keys(raw)) {
    const value = raw[key];
    if (value !== null && typeof value !== "boolean" && typeof value !== "number") return null;
    change[key] = value;
  }
  return change;
}

function field(value: unknown, keys: readonly string[]): JsonObject | null {
  return isPlainObject(value) && hasKeys(value, keys) ? value : null;
}

const isNumber = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value);

function parseChangeRecord(raw: unknown): GuardsChangeRecord | null {
  const record = field(raw, ["at", "change", "reason"]);
  if (record === null) return null;
  const at = record["at"];
  const reason = record["reason"];
  const change = parseFlatRecord(record["change"]);
  if (typeof at !== "string" || typeof reason !== "string" || change === null) return null;
  return { at, change, reason };
}

// CostGuard の応答を確かめて GuardsView に組み直す（クライアントが使う）。形が違えば null。
export function parseGuardsView(raw: unknown): GuardsView | null {
  const top = field(raw, ["guards", "usage", "unitCostUsd", "recentChanges"]);
  if (top === null) return null;
  const guards = field(top["guards"], [
    "budget",
    "gameTime",
    "signature",
    "turnstile",
    "rateLimit",
    "cpuMs",
    "acceptGames",
  ]);
  const usage = field(top["usage"], ["todayUsd", "monthUsd", "activeGames"]);
  const unitCostUsd = field(top["unitCostUsd"], ["lite", "standard-1"]);
  const recentRaw = top["recentChanges"];
  if (guards === null || usage === null || unitCostUsd === null || !Array.isArray(recentRaw)) {
    return null;
  }
  const budget = field(guards["budget"], ["enabled", "monthlyUsd", "dailyUsd"]);
  const gameTime = field(guards["gameTime"], ["enabled", "maxMinutes", "maxAlarms"]);
  const signature = field(guards["signature"], ["enabled", "locked"]);
  const turnstile = field(guards["turnstile"], ["enabled"]);
  const rateLimit = field(guards["rateLimit"], ["enabled", "perIpPerMinute"]);
  const cpuMs = field(guards["cpuMs"], ["value", "note"]);
  if (
    budget === null ||
    gameTime === null ||
    signature === null ||
    turnstile === null ||
    rateLimit === null ||
    cpuMs === null
  ) {
    return null;
  }

  const budgetEnabled = budget["enabled"];
  const monthlyUsd = budget["monthlyUsd"];
  const dailyUsd = budget["dailyUsd"];
  const maxMinutes = gameTime["maxMinutes"];
  const maxAlarms = gameTime["maxAlarms"];
  const turnstileEnabled = turnstile["enabled"];
  const rateLimitEnabled = rateLimit["enabled"];
  const perIpPerMinute = rateLimit["perIpPerMinute"];
  const cpuMsValue = cpuMs["value"];
  const cpuMsNote = cpuMs["note"];
  const acceptGames = guards["acceptGames"];
  const todayUsd = usage["todayUsd"];
  const monthUsd = usage["monthUsd"];
  const activeGames = usage["activeGames"];
  const lite = unitCostUsd["lite"];
  const standard1 = unitCostUsd["standard-1"];
  if (
    !isBool(budgetEnabled) ||
    !isNumber(monthlyUsd) ||
    !isNumber(dailyUsd) ||
    gameTime["enabled"] !== true ||
    !isNumber(maxMinutes) ||
    !isNumber(maxAlarms) ||
    signature["enabled"] !== true ||
    signature["locked"] !== true ||
    !isBool(turnstileEnabled) ||
    !isBool(rateLimitEnabled) ||
    !isNumber(perIpPerMinute) ||
    !isNumber(cpuMsValue) ||
    typeof cpuMsNote !== "string" ||
    !isBool(acceptGames) ||
    !isNumber(todayUsd) ||
    !isNumber(monthUsd) ||
    !isNumber(activeGames) ||
    !isNumber(lite) ||
    !isNumber(standard1)
  ) {
    return null;
  }

  const recentChanges: GuardsChangeRecord[] = [];
  for (const item of recentRaw) {
    const record = parseChangeRecord(item);
    if (record === null) return null;
    recentChanges.push(record);
  }

  return {
    guards: {
      budget: { enabled: budgetEnabled, monthlyUsd, dailyUsd },
      gameTime: { enabled: true, maxMinutes, maxAlarms },
      signature: { enabled: true, locked: true },
      turnstile: { enabled: turnstileEnabled },
      rateLimit: { enabled: rateLimitEnabled, perIpPerMinute },
      cpuMs: { value: cpuMsValue, note: cpuMsNote },
      acceptGames,
    },
    usage: { todayUsd, monthUsd, activeGames },
    unitCostUsd: { lite, "standard-1": standard1 },
    recentChanges,
  };
}
