import { describe, expect, it } from "vitest";

import { bobOffset, rainOffset, seedOf, waveOffset } from "./motion.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;
const TIMES = Array.from({ length: 60 }, (_, i) => i * 137.5);

describe("waveOffset", () => {
  it("動きを減らす設定では 0", () => {
    for (const t of TIMES) expect(waveOffset(t, true)).toBe(0);
  });

  it("時刻で変わり、振幅の範囲に収まる", () => {
    const values = TIMES.map((t) => waveOffset(t, false));
    expect(new Set(values).size).toBeGreaterThan(1);
    for (const v of values) expect(Math.abs(v)).toBeLessThanOrEqual(A.waveAmplitude);
  });
});

describe("rainOffset", () => {
  it("動きを減らす設定では 0", () => {
    for (const t of TIMES) expect(rainOffset(t, A.rainPeriod, true)).toBe(0);
  });

  it("時刻で変わり、0 以上 period 未満", () => {
    const values = [...TIMES, -1234.5, 1e12 + 0.25].map((t) => rainOffset(t, A.rainPeriod, false));
    expect(new Set(values).size).toBeGreaterThan(1);
    for (const v of values) {
      expect(v).toBeGreaterThanOrEqual(0);
      expect(v).toBeLessThan(A.rainPeriod);
    }
  });

  it("period が 0 以下なら 0", () => {
    expect(rainOffset(1000, 0, false)).toBe(0);
  });
});

describe("bobOffset", () => {
  it("動きを減らす設定では揺れない", () => {
    for (const t of TIMES) expect(bobOffset(t, 42, true)).toEqual({ y: 0, rotation: 0 });
  });

  it("時刻で変わり、振幅の範囲に収まる", () => {
    const values = TIMES.map((t) => bobOffset(t, 42, false));
    expect(new Set(values.map((v) => v.y)).size).toBeGreaterThan(1);
    for (const v of values) {
      expect(Math.abs(v.y)).toBeLessThanOrEqual(A.bobAmplitude);
      expect(Math.abs(v.rotation)).toBeLessThanOrEqual(A.bobRotation);
    }
  });

  it("種が違えば揺れの位相がずれる", () => {
    expect(bobOffset(500, seedOf("f1"), false)).not.toEqual(bobOffset(500, seedOf("f2"), false));
  });
});

describe("seedOf", () => {
  it("同じ文字列なら同じ種", () => {
    expect(seedOf("ferry-1")).toBe(seedOf("ferry-1"));
    expect(seedOf("ferry-1")).not.toBe(seedOf("ferry-2"));
  });
});
