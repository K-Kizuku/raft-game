import { describe, expect, it } from "vitest";

import { checkSessionRateLimit, rateLimitKey } from "./rate-limit.ts";

describe("L-1 rateLimitKey", () => {
  it.each([
    ["203.0.113.5", "ip4:203.0.113.5"],
    ["0.0.0.0", "ip4:0.0.0.0"],
    ["255.255.255.255", "ip4:255.255.255.255"],
    ["2001:DB8:0:0:1::1", "ip6:2001:db8:0:0"],
    ["2001:db8::1", "ip6:2001:db8:0:0"],
    ["2001:0db8:0000:0000:ffff:ffff:ffff:ffff", "ip6:2001:db8:0:0"],
    ["2001:db8:1:2:3:4:5:6", "ip6:2001:db8:1:2"],
    ["::1", "ip6:0:0:0:0"],
    ["::", "ip6:0:0:0:0"],
    ["fe80::", "ip6:fe80:0:0:0"],
    ["::ffff:198.51.100.7", "ip4:198.51.100.7"],
    ["::FFFF:198.51.100.7", "ip4:198.51.100.7"],
    ["0:0:0:0:0:ffff:198.51.100.7", "ip4:198.51.100.7"],
    [null, "unknown"],
    ["", "unknown"],
    ["256.1.1.1", "unknown"],
    ["01.2.3.4", "unknown"],
    ["1.2.3", "unknown"],
    ["fe80::1%eth0", "unknown"],
    ["1:2:3:4:5:6:7:8:9", "unknown"],
    ["1:2:3:4:5:6:7", "unknown"],
    ["1:2:3:4::5:6:7:8", "unknown"],
    ["::1::", "unknown"],
    [":1:2:3:4:5:6:7", "unknown"],
    ["12345::1", "unknown"],
    ["::1.2.3.4", "unknown"],
    ["64:ff9b::198.51.100.7", "unknown"],
    ["::ffff:256.1.1.1", "unknown"],
    ["x", "unknown"],
  ])("%j → %s", (ip, key) => {
    expect(rateLimitKey(ip)).toBe(key);
  });
});

interface FakeLimiter {
  readonly calls: unknown[];
  limit(options: unknown): Promise<unknown>;
}

// 10 回まで success: true、11 回目から false。
function countingLimiter(max = 10): FakeLimiter {
  const calls: unknown[] = [];
  return {
    calls,
    async limit(options) {
      calls.push(options);
      return { success: calls.length <= max };
    },
  };
}

describe("L-2 束縛", () => {
  it("11 回目が rate_limited で、キーは ip4:203.0.113.5", async () => {
    const limiter = countingLimiter();
    const env = { SESSION_RATE_LIMIT: limiter };
    const results: string[] = [];
    for (let i = 0; i < 11; i++) {
      results.push(await checkSessionRateLimit({ enabled: true, env, clientIp: "203.0.113.5" }));
    }
    expect(results.slice(0, 10)).toEqual(Array<string>(10).fill("ok"));
    expect(results[10]).toBe("rate_limited");
    expect(limiter.calls).toHaveLength(11);
    expect(
      limiter.calls.every((call) => JSON.stringify(call) === '{"key":"ip4:203.0.113.5"}'),
    ).toBe(true);
  });

  it("limit は束縛のオブジェクトを this にして呼ぶ", async () => {
    const binding = {
      seen: [] as unknown[],
      limit(this: { seen: unknown[] }, options: unknown) {
        this.seen.push(options);
        return Promise.resolve({ success: true });
      },
    };
    const result = await checkSessionRateLimit({
      enabled: true,
      env: { SESSION_RATE_LIMIT: binding },
      clientIp: "2001:db8::1",
    });
    expect(result).toBe("ok");
    expect(binding.seen).toEqual([{ key: "ip6:2001:db8:0:0" }]);
  });
});

describe("L-3 拒否する側", () => {
  it.each([
    ["束縛がない", undefined],
    ["{}", {}],
    ["{ limit: 1 }", { limit: 1 }],
    [
      "limit が例外",
      {
        limit: () => {
          throw new Error("boom");
        },
      },
    ],
    ["limit が reject", { limit: () => Promise.reject(new Error("boom")) }],
    ["結果が {}", { limit: async () => ({}) }],
    ['結果が {success:"true"}', { limit: async () => ({ success: "true" }) }],
    ["結果が undefined", { limit: async () => undefined }],
    ["結果が true", { limit: async () => true }],
  ])("%s → rate_limited", async (_name, binding) => {
    const env = binding === undefined ? {} : { SESSION_RATE_LIMIT: binding };
    await expect(
      checkSessionRateLimit({ enabled: true, env, clientIp: "203.0.113.5" }),
    ).resolves.toBe("rate_limited");
  });
});

describe("L-4 無効", () => {
  it("success: false を返す束縛でも、束縛がなくても ok で、limit を呼ばない", async () => {
    const limiter = countingLimiter(0);
    expect(
      await checkSessionRateLimit({
        enabled: false,
        env: { SESSION_RATE_LIMIT: limiter },
        clientIp: "203.0.113.5",
      }),
    ).toBe("ok");
    expect(await checkSessionRateLimit({ enabled: false, env: {}, clientIp: null })).toBe("ok");
    expect(limiter.calls).toHaveLength(0);
  });
});
