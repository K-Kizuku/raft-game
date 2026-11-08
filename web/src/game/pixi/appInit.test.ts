import { describe, expect, it } from "vitest";

import { clampResolution, initAttempts } from "./appInit.ts";

describe("initAttempts", () => {
  it("AI-1 1 回目は webgl・devicePixelRatio・antialias、2 回目は webgpu、3 回目は resolution 1・antialias なし", () => {
    expect(initAttempts(2)).toEqual([
      { preference: "webgl", resolution: 2, antialias: true },
      { preference: "webgpu", resolution: 2, antialias: true },
      { preference: "webgl", resolution: 1, antialias: false },
    ]);
  });

  it("AI-2 devicePixelRatio が不正（0・NaN）なら 1 にする", () => {
    expect(initAttempts(0)[0]?.resolution).toBe(1);
    expect(initAttempts(Number.NaN)[1]?.resolution).toBe(1);
  });
});

describe("clampResolution", () => {
  const limits = { maxTextureSize: 4096, maxRenderbufferSize: 8192 };

  it("CR-1 上限に収まるなら want のまま", () => {
    expect(clampResolution({ width: 1470, height: 860 }, 2, limits)).toBe(2);
  });

  it("CR-2 長い辺 × want が上限（小さいほう）を超えるなら、収まるまで抑える", () => {
    const r = clampResolution({ width: 3000, height: 1000 }, 2, limits);
    expect(r).toBeLessThan(2);
    expect(Math.round(3000 * r)).toBeLessThanOrEqual(4096);
    expect(r).toBeGreaterThan(1.3);
  });

  it("CR-3 MAX_RENDERBUFFER_SIZE のほうが小さければ、そちらに合わせる", () => {
    const r = clampResolution({ width: 1000, height: 2500 }, 2, {
      maxTextureSize: 16384,
      maxRenderbufferSize: 4096,
    });
    expect(Math.round(2500 * r)).toBeLessThanOrEqual(4096);
  });

  it("CR-4 上限が分からない・大きさが 0 なら want のまま", () => {
    expect(clampResolution({ width: 9000, height: 9000 }, 2, null)).toBe(2);
    expect(clampResolution({ width: 0, height: 0 }, 2, limits)).toBe(2);
  });

  it("CR-5 resolution 1 でも上限を超える大きさなら 1 より小さくする", () => {
    const r = clampResolution({ width: 5000, height: 100 }, 1, limits);
    expect(r).toBeGreaterThan(0);
    expect(Math.round(5000 * r)).toBeLessThanOrEqual(4096);
  });
});
