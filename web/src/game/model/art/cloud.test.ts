import { describe, expect, it } from "vitest";

import { cloudEllipses, cloudOutline } from "./cloud.ts";
import { MAP_ART_STYLE } from "./style.ts";

const inside = (p: { x: number; y: number }, scale: number): boolean =>
  cloudEllipses().some((e) => ((p.x - e.x) / e.rx) ** 2 + ((p.y - e.y) / e.ry) ** 2 <= scale);

describe("cloudOutline", () => {
  it("頂点の数が指定どおり（省略すると MAP_ART_STYLE.cloudPoints）", () => {
    expect(cloudOutline(24)).toHaveLength(24);
    expect(cloudOutline()).toHaveLength(MAP_ART_STYLE.cloudPoints);
  });

  it("どの頂点も、いずれかの楕円の縁の上にある（合わせた形の外周）", () => {
    for (const p of cloudOutline()) {
      expect(inside(p, 1 + 1e-9)).toBe(true);
      // 少し外へ出すと、どの楕円にも入らない
      expect(inside({ x: p.x * 1.02, y: p.y * 1.02 }, 1)).toBe(false);
    }
  });

  it("雲の幅は楕円の横のずらしと半径の和", () => {
    const xs = cloudOutline(72).map((p) => p.x);
    const half = MAP_ART_STYLE.cloudSpread + MAP_ART_STYLE.cloudRadiusX;
    expect(Math.max(...xs)).toBeCloseTo(half, 0);
    expect(Math.min(...xs)).toBeCloseTo(-half, 0);
  });
});
