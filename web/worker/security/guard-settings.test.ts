import { describe, expect, it } from "vitest";

import type { DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import { guardsWith } from "../admin/test-fixtures.ts";
import { createTestState } from "../admin/test-sqlite.ts";
import { CostGuard } from "../cost-guard.ts";
import type { Env } from "../routes/env.ts";
import { createGuardSettingsSource, STRICT_GUARDS } from "./guard-settings.ts";

// 本物の CostGuard（":memory:" の DB）に、/view の呼び出しを数える偽の名前空間をかぶせる。
async function costGuardEnv(
  guards: unknown,
  options: { readonly fail?: () => boolean } = {},
): Promise<{ env: Env; paths: string[] }> {
  const guard = new CostGuard(await createTestState(), { GUARDS: guards });
  const paths: string[] = [];
  const namespace: DurableObjectNamespaceLike = {
    idFromName: (name) => ({ name }),
    get: () => ({
      fetch: async (request) => {
        paths.push(new URL(request.url).pathname);
        if (options.fail?.() === true) throw new Error("CostGuard unreachable");
        return guard.fetch(request);
      },
    }),
  };
  return { env: { COST_GUARD: namespace }, paths };
}

const source = (now: () => number = () => 0) => createGuardSettingsSource({ ttlMs: 5000, now });

describe("G-1 読み替え", () => {
  it("turnstile・rateLimit を無効、maxMinutes 15", async () => {
    const { env } = await costGuardEnv(
      guardsWith({
        "turnstile.enabled": false,
        "rateLimit.enabled": false,
        "gameTime.maxMinutes": 15,
      }),
    );
    expect(await source().load(env)).toEqual({
      turnstileEnabled: false,
      rateLimitEnabled: false,
      maxMinutes: 15,
    });
  });

  it("既定（GUARDS の例）はすべて有効、maxMinutes 30", async () => {
    const { env } = await costGuardEnv(guardsWith({}));
    expect(await source().load(env)).toEqual({
      turnstileEnabled: true,
      rateLimitEnabled: true,
      maxMinutes: 30,
    });
  });
});

describe("G-2 拒否する側", () => {
  it("STRICT_GUARDS はすべて有効、maxMinutes 60", () => {
    expect(STRICT_GUARDS).toEqual({
      turnstileEnabled: true,
      rateLimitEnabled: true,
      maxMinutes: 60,
    });
  });

  it("COST_GUARD がない → STRICT_GUARDS", async () => {
    expect(await source().load({})).toEqual(STRICT_GUARDS);
    expect(await source().load({ COST_GUARD: {} })).toEqual(STRICT_GUARDS);
  });

  it("view が 503 guards_invalid → STRICT_GUARDS", async () => {
    const { env, paths } = await costGuardEnv(
      guardsWith({ "turnstile.enabled": false, signature: { enabled: false } }),
    );
    expect(await source().load(env)).toEqual(STRICT_GUARDS);
    expect(paths).toEqual(["/view"]);
  });
});

describe("G-3 例外", () => {
  it("fetch が例外 → rejects で、次の load で再び呼ぶ", async () => {
    let fail = true;
    const { env, paths } = await costGuardEnv(guardsWith({ "turnstile.enabled": false }), {
      fail: () => fail,
    });
    const s = source();
    await expect(s.load(env)).rejects.toThrow();
    await expect(s.load(env)).rejects.toThrow();
    fail = false;
    expect(await s.load(env)).toMatchObject({ turnstileEnabled: false });
    expect(paths).toEqual(["/view", "/view", "/view"]);
  });
});

describe("G-4 キャッシュ", () => {
  it("now が 0・4999 では view を 1 回だけ、5000 で 2 回目", async () => {
    const { env, paths } = await costGuardEnv(guardsWith({}));
    let now = 0;
    const s = source(() => now);
    await s.load(env);
    now = 4999;
    await s.load(env);
    expect(paths).toEqual(["/view"]);
    now = 5000;
    await s.load(env);
    expect(paths).toEqual(["/view", "/view"]);
  });
});
