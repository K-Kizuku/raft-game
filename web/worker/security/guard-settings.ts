// CostGuard の設定のうち、Worker の入り口が使う値の読み込み（T-310・ADR-0018・プロトコル §2）。
//
// turnstile.enabled・rateLimit.enabled・gameTime.maxMinutes だけを読む。CostGuard がない・
// 設定が不正（guards_invalid）なら STRICT_GUARDS（すべて有効・maxMinutes は範囲の上限）に倒す。
// view() の例外はそのまま投げ、キャッシュしない（ルーターが 500 にする）。
//
// 1 つの isolate の中で、最後に読んだ結果を ttlMs の間だけ使い回す。管理用 API での変更は、
// 各 isolate で最大 ttlMs 遅れて効く。sessionId の検証はこの設定を読まない。

import { getCostGuardClient } from "../admin/client.ts";
import type { Env } from "../routes/env.ts";

export interface SecurityGuards {
  readonly turnstileEnabled: boolean;
  readonly rateLimitEnabled: boolean;
  readonly maxMinutes: number; // 1〜60 の整数
}

// maxMinutes を設定の範囲の上限にするのは、Worker の期限が GameSession の打ち切りより先に
// 来ないようにするため（実際の打ち切りは GameSession のアラーム）。
export const STRICT_GUARDS: SecurityGuards = {
  turnstileEnabled: true,
  rateLimitEnabled: true,
  maxMinutes: 60,
};

export interface GuardSettingsSource {
  load(env: Env): Promise<SecurityGuards>;
}

async function readGuards(env: Env): Promise<SecurityGuards> {
  const client = getCostGuardClient(env);
  if (client === undefined) return STRICT_GUARDS;
  const view = await client.view();
  if (view === "guards_invalid") return STRICT_GUARDS;
  const { guards } = view;
  return {
    turnstileEnabled: guards.turnstile.enabled,
    rateLimitEnabled: guards.rateLimit.enabled,
    maxMinutes: guards.gameTime.maxMinutes,
  };
}

export function createGuardSettingsSource(options: {
  readonly ttlMs: number;
  readonly now: () => number;
}): GuardSettingsSource {
  let cached: { readonly at: number; readonly value: SecurityGuards } | undefined;
  return {
    async load(env) {
      const now = options.now();
      if (cached !== undefined && now - cached.at < options.ttlMs) return cached.value;
      const value = await readGuards(env);
      cached = { at: now, value };
      return value;
    },
  };
}
