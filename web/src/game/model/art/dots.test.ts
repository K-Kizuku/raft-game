import { describe, expect, it } from "vitest";

import { dotPositions, insetSegment } from "./dots.ts";

const dist = (p: { x: number; y: number }, q: { x: number; y: number }): number =>
  Math.hypot(p.x - q.x, p.y - q.y);

describe("dotPositions", () => {
  const a = { x: 10, y: 20 };
  const b = { x: 130, y: 70 }; // 長さ 130

  it("両端から inset 以上空ける", () => {
    const dots = dotPositions(a, b, 11, 30);
    expect(dots.length).toBeGreaterThan(0);
    for (const p of dots) {
      expect(dist(p, a)).toBeGreaterThanOrEqual(30 - 1e-9);
      expect(dist(p, b)).toBeGreaterThanOrEqual(30 - 1e-9);
    }
    // 残りの真ん中にそろえる（両端の空きが同じ）
    expect(dist(dots[0]!, a)).toBeCloseTo(dist(dots[dots.length - 1]!, b));
  });

  it("点の間隔が spacing で、点は航路の上にある", () => {
    const dots = dotPositions(a, b, 11, 30);
    for (let i = 1; i < dots.length; i++) expect(dist(dots[i - 1]!, dots[i]!)).toBeCloseTo(11);
    for (const p of dots) expect(dist(a, p) + dist(p, b)).toBeCloseTo(dist(a, b));
    expect(dots).toHaveLength(Math.floor((130 - 60) / 11) + 1);
  });

  it("長さが 2 × inset より短い航路では空の配列", () => {
    expect(dotPositions({ x: 0, y: 0 }, { x: 50, y: 0 }, 10, 30)).toEqual([]);
  });

  it("長さ 0・間隔が 0 以下では空の配列", () => {
    expect(dotPositions(a, a, 10, 0)).toEqual([]);
    expect(dotPositions(a, b, 0, 10)).toEqual([]);
  });
});

describe("insetSegment", () => {
  it("両端を inset ずつ縮める", () => {
    const seg = insetSegment({ x: 0, y: 0 }, { x: 100, y: 0 }, 30);
    expect(seg).toEqual([
      { x: 30, y: 0 },
      { x: 70, y: 0 },
    ]);
  });

  it("短すぎるときは null", () => {
    expect(insetSegment({ x: 0, y: 0 }, { x: 50, y: 0 }, 30)).toBeNull();
  });
});
