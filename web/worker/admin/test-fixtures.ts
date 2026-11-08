// テスト用の共通の値（本番のコードからは import しない）。

import { isPlainObject } from "./guards.ts";

// ADR-0018 の値（GUARDS の例）。
export const GUARDS_EXAMPLE = {
  budget: { enabled: true, monthlyUsd: 30, dailyUsd: 1 },
  gameTime: { maxMinutes: 30, maxAlarms: 20 },
  turnstile: { enabled: true },
  rateLimit: { enabled: true, perIpPerMinute: 10 },
  cpuMs: 50,
  acceptGames: true,
  unitCostUsd: { lite: 0.0065, "standard-1": 0.04 },
} as const;

export const DELETE = Symbol("delete");

// value を複製し、path の値を置き換える（DELETE なら消す）。
export function withValue(value: unknown, path: readonly string[], next: unknown): unknown {
  const copy: unknown = structuredClone(value);
  let cursor: unknown = copy;
  path.forEach((key, index) => {
    if (!isPlainObject(cursor)) throw new Error("withValue: not an object");
    const target: Record<string, unknown> = cursor;
    if (index === path.length - 1) {
      if (next === DELETE) delete target[key];
      else target[key] = next;
    } else {
      cursor = target[key];
    }
  });
  return copy;
}

// GUARDS_EXAMPLE の一部を変えたもの。
export function guardsWith(changes: Readonly<Record<string, unknown>>): unknown {
  let value: unknown = GUARDS_EXAMPLE;
  for (const [path, next] of Object.entries(changes))
    value = withValue(value, path.split("."), next);
  return value;
}
