import { describe, expect, it } from "vitest";

import { MAP_STYLE } from "./style.ts";
import { fitIslands, toScreen } from "./viewport.ts";

describe("fitIslands", () => {
  it("VP-1 島 3 つを 800×600 に収め、外接矩形の中心が画面の中心に来る", () => {
    const points = [
      { x: 0, y: 0 },
      { x: 400, y: 0 },
      { x: 200, y: 300 },
    ];
    const t = fitIslands(points, { width: 800, height: 600 });
    const expected = Math.min(
      MAP_STYLE.maxScale,
      Math.min((800 - 2 * MAP_STYLE.padding) / 400, (600 - 2 * MAP_STYLE.padding) / 300),
    );
    expect(t.scale).toBeCloseTo(expected);
    const center = toScreen(t, { x: 200, y: 150 });
    expect(center.x).toBeCloseTo(400);
    expect(center.y).toBeCloseTo(300);
    // すべての島が余白の内側に入る。
    for (const p of points) {
      const s = toScreen(t, p);
      expect(s.x).toBeGreaterThanOrEqual(MAP_STYLE.padding - 1e-9);
      expect(s.x).toBeLessThanOrEqual(800 - MAP_STYLE.padding + 1e-9);
      expect(s.y).toBeGreaterThanOrEqual(MAP_STYLE.padding - 1e-9);
      expect(s.y).toBeLessThanOrEqual(600 - MAP_STYLE.padding + 1e-9);
    }
  });

  it("VP-1 大きな画面では maxScale で頭打ちにする", () => {
    const t = fitIslands(
      [
        { x: 0, y: 0 },
        { x: 10, y: 10 },
      ],
      { width: 800, height: 600 },
    );
    expect(t.scale).toBe(MAP_STYLE.maxScale);
  });

  it("VP-2 空・島 1 つ・画面が余白より小さい", () => {
    expect(fitIslands([], { width: 800, height: 600 })).toEqual({
      scale: 1,
      offsetX: 0,
      offsetY: 0,
    });

    const one = fitIslands([{ x: 123, y: -45 }], { width: 800, height: 600 });
    const p = toScreen(one, { x: 123, y: -45 });
    expect(p.x).toBeCloseTo(400);
    expect(p.y).toBeCloseTo(300);

    const tiny = fitIslands(
      [
        { x: 0, y: 0 },
        { x: 400, y: 300 },
      ],
      { width: 50, height: 50 },
    );
    expect(tiny.scale).toBe(MAP_STYLE.minScale);
    for (const v of Object.values(tiny)) expect(Number.isFinite(v)).toBe(true);
  });
});

describe("toScreen", () => {
  it("scale と offset で写す", () => {
    expect(toScreen({ scale: 2, offsetX: 10, offsetY: -5 }, { x: 3, y: 4 })).toEqual({
      x: 16,
      y: 3,
    });
  });
});
