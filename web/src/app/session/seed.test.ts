import { describe, expect, it } from "vitest";

import { randomSeed } from "./seed.ts";

describe("randomSeed", () => {
  it.each([0, 4294967295])("S-1 差し替えた getRandomValues の値 %i をそのまま返す", (value) => {
    const seed = randomSeed((array) => {
      expect(array).toBeInstanceOf(Uint32Array);
      expect(array).toHaveLength(1);
      array[0] = value;
      return array;
    });
    expect(seed).toBe(value);
  });

  it("S-1 既定の実装の値は 0 以上 2^32 未満の整数", () => {
    for (let i = 0; i < 20; i++) {
      const seed = randomSeed();
      expect(Number.isInteger(seed)).toBe(true);
      expect(seed).toBeGreaterThanOrEqual(0);
      expect(seed).toBeLessThan(2 ** 32);
    }
  });
});
