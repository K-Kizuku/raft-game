import { describe, expect, it } from "vitest";

import { dashSegments } from "./dash.ts";

describe("dashSegments", () => {
  it("DS-1 長さ 100 の水平線を dash 10・gap 8 で分ける", () => {
    const segs = dashSegments({ x: 0, y: 5 }, { x: 100, y: 5 }, 10, 8);
    expect(segs).toHaveLength(6);
    const xs = segs.map(([p, q]) => [p.x, q.x]);
    expect(xs[0]?.[0]).toBeCloseTo(0);
    expect(xs[0]?.[1]).toBeCloseTo(10);
    expect(xs[5]?.[0]).toBeCloseTo(90);
    expect(xs[5]?.[1]).toBeCloseTo(100);
    for (const [p, q] of segs) {
      expect(p.y).toBeCloseTo(5);
      expect(q.y).toBeCloseTo(5);
    }
  });

  it("DS-1 最後の線分は b で切る（斜めの線）", () => {
    const b = { x: 30, y: 40 }; // 長さ 50
    const segs = dashSegments({ x: 0, y: 0 }, b, 20, 20);
    expect(segs).toHaveLength(2);
    const last = segs[1];
    expect(last?.[1]).toEqual(b);
    expect(last?.[0].x).toBeCloseTo(24);
    expect(last?.[0].y).toBeCloseTo(32);
  });

  it.each([
    { name: "同じ点", a: { x: 3, y: 3 }, b: { x: 3, y: 3 }, dash: 10 },
    { name: "dash が 0", a: { x: 0, y: 0 }, b: { x: 100, y: 0 }, dash: 0 },
    { name: "dash が負", a: { x: 0, y: 0 }, b: { x: 100, y: 0 }, dash: -1 },
  ])("DS-1 $name → []", ({ a, b, dash }) => {
    expect(dashSegments(a, b, dash, 8)).toEqual([]);
  });
});
