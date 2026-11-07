// GameSession の設定（T-308・DesignDoc §12.5）。
//
// - CONTAINER_START_TIMEOUT_MS（Env。値は T-601 が cloudflare.config.ts に入れる）
// - gameTime.maxMinutes・gameTime.maxAlarms（POST /init のときに CostGuard の view() から 1 回だけ読む）

import type { CostGuardClient } from "../admin/client.ts";
import type { Env } from "../routes/env.ts";
import { DEFAULT_MAX_ALARMS, DEFAULT_MAX_MINUTES } from "./limits.ts";

// 仮の値（#15 の結果で見直す）。ADR-0016 の「起動は多くの場合 1〜3 秒」とスパイクの既定に合わせた。
export const DEFAULT_CONTAINER_START_TIMEOUT_MS = 30000;
const MIN_CONTAINER_START_TIMEOUT_MS = 1000;
const MAX_CONTAINER_START_TIMEOUT_MS = 120000;
const POSITIVE_INTEGER = /^[1-9][0-9]*$/;

// コンテナを起動してから /healthz が 200 を返すまで待つ上限（ミリ秒）。なし・不正なら既定値。
export function containerStartTimeoutMsOf(env: Env): number {
  const raw = env["CONTAINER_START_TIMEOUT_MS"];
  if (typeof raw !== "string" || !POSITIVE_INTEGER.test(raw)) {
    return DEFAULT_CONTAINER_START_TIMEOUT_MS;
  }
  const value = Number(raw);
  return value >= MIN_CONTAINER_START_TIMEOUT_MS && value <= MAX_CONTAINER_START_TIMEOUT_MS
    ? value
    : DEFAULT_CONTAINER_START_TIMEOUT_MS;
}

export interface GameTimeLimits {
  readonly maxMinutes: number;
  readonly maxAlarms: number;
}

const DEFAULT_LIMITS: GameTimeLimits = {
  maxMinutes: DEFAULT_MAX_MINUTES,
  maxAlarms: DEFAULT_MAX_ALARMS,
};

const isPositiveInt = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 1;

// costGuard がない・view() が例外・"guards_invalid" なら既定値（30 分・20 回）。
export async function readGameTimeLimits(
  costGuard: CostGuardClient | undefined,
): Promise<GameTimeLimits> {
  if (costGuard === undefined) return DEFAULT_LIMITS;
  let view: Awaited<ReturnType<CostGuardClient["view"]>>;
  try {
    view = await costGuard.view();
  } catch {
    return DEFAULT_LIMITS;
  }
  if (view === "guards_invalid") return DEFAULT_LIMITS;
  const { maxMinutes, maxAlarms } = view.guards.gameTime;
  if (!isPositiveInt(maxMinutes) || !isPositiveInt(maxAlarms)) return DEFAULT_LIMITS;
  return { maxMinutes, maxAlarms };
}
