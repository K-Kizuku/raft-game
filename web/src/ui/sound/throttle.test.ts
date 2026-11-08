import { describe, expect, it } from "vitest";

import { DEFAULT_THROTTLE, createSoundThrottle } from "./throttle.ts";

describe("createSoundThrottle", () => {
  it("TH-1 同じ種類は minGapMs の間に 1 回だけ", () => {
    const throttle = createSoundThrottle({ minGapMs: 200, windowMs: 1000, maxPerWindow: 10 });
    expect(throttle.allow("deliver", 1000)).toBe(true);
    expect(throttle.allow("deliver", 1000)).toBe(false);
    expect(throttle.allow("deliver", 1199)).toBe(false);
    expect(throttle.allow("deliver", 1200)).toBe(true);
  });

  it("TH-2 違う種類は同じ時刻でも鳴らせる", () => {
    const throttle = createSoundThrottle({ minGapMs: 200, windowMs: 1000, maxPerWindow: 10 });
    expect(throttle.allow("depart", 1000)).toBe(true);
    expect(throttle.allow("arrive", 1000)).toBe(true);
    expect(throttle.allow("confirm", 1000)).toBe(true);
  });

  it("TH-3 windowMs の間に maxPerWindow 回まで（全種類で数える）", () => {
    const throttle = createSoundThrottle({ minGapMs: 0, windowMs: 1000, maxPerWindow: 3 });
    expect(throttle.allow("depart", 0)).toBe(true);
    expect(throttle.allow("arrive", 100)).toBe(true);
    expect(throttle.allow("deliver", 200)).toBe(true);
    expect(throttle.allow("confirm", 300)).toBe(false);
    expect(throttle.allow("storm", 999)).toBe(false);
    // 最初の音から windowMs たてば、また鳴らせる
    expect(throttle.allow("storm", 1000)).toBe(true);
  });

  it("TH-4 断った音は数えない", () => {
    const throttle = createSoundThrottle({ minGapMs: 200, windowMs: 1000, maxPerWindow: 2 });
    expect(throttle.allow("deliver", 0)).toBe(true);
    for (let t = 1; t < 200; t += 10) expect(throttle.allow("deliver", t)).toBe(false);
    expect(throttle.allow("arrive", 250)).toBe(true);
  });

  it("TH-5 たくさん同時に来ても、既定では 1 秒に数回まで", () => {
    const throttle = createSoundThrottle();
    let played = 0;
    for (let i = 0; i < 100; i++) {
      for (const kind of ["depart", "arrive", "deliver", "confirm", "storm"] as const) {
        if (throttle.allow(kind, i * 10)) played++;
      }
    }
    // 1 秒（0〜990ms）で上限まで
    expect(played).toBeLessThanOrEqual(DEFAULT_THROTTLE.maxPerWindow);
    expect(played).toBeGreaterThan(0);
  });

  it("TH-6 reset で数え直す", () => {
    const throttle = createSoundThrottle({ minGapMs: 500, windowMs: 1000, maxPerWindow: 1 });
    expect(throttle.allow("storm", 0)).toBe(true);
    expect(throttle.allow("storm", 10)).toBe(false);
    throttle.reset();
    expect(throttle.allow("storm", 20)).toBe(true);
  });
});
