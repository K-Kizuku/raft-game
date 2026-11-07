import { describe, expect, it } from "vitest";

import { MAP_STYLE } from "../style.ts";
import { labelScale } from "./labelScale.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

describe("labelScale", () => {
  it("1280 以下の幅では最小の倍率", () => {
    for (const w of [1, 320, 1024, 1279, 1280]) expect(labelScale(w)).toBe(A.labelScaleMin);
  });

  it("1920 以上の幅では最大の倍率", () => {
    for (const w of [1920, 2560, 3840, Number.POSITIVE_INFINITY]) {
      expect(labelScale(w)).toBe(A.labelScaleMax);
    }
  });

  it("0・負・数でない幅では最小の倍率", () => {
    for (const w of [0, -1, -1920, Number.NaN, Number.NEGATIVE_INFINITY]) {
      expect(labelScale(w)).toBe(A.labelScaleMin);
    }
  });

  it("その間は単調に増え、両端の倍率の間に収まる", () => {
    let previous = labelScale(A.labelScaleMinWidth);
    for (let w = A.labelScaleMinWidth + 10; w <= A.labelScaleMaxWidth; w += 10) {
      const k = labelScale(w);
      expect(k).toBeGreaterThan(previous);
      expect(k).toBeGreaterThanOrEqual(A.labelScaleMin);
      expect(k).toBeLessThanOrEqual(A.labelScaleMax);
      previous = k;
    }
    expect(labelScale(1600)).toBeCloseTo((A.labelScaleMin + A.labelScaleMax) / 2, 10);
  });

  it("最小の倍率は 1（小さな画面の文字を変えない）で、1920 の幅で島の名前の文字が 16px 以上", () => {
    expect(A.labelScaleMin).toBe(1);
    expect(A.labelScaleMinWidth).toBe(1280);
    expect(A.labelScaleMaxWidth).toBe(1920);
    expect(MAP_STYLE.draw.labelFontSize * labelScale(1920)).toBeGreaterThanOrEqual(16);
  });
});
