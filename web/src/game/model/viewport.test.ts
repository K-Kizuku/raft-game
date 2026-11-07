import { describe, expect, it } from "vitest";

import { MAP_STYLE } from "./style.ts";
import { MAP_VIEW, fitIslands, layoutMap, mapInsets, markScaleFor, toScreen } from "./viewport.ts";
import type { Point, Size } from "./viewport.ts";

describe("fitIslands", () => {
  it("VP-1 島 3 つを 800×600 に収め、外接矩形の中心が画面の中心に来る", () => {
    const points = [
      { x: 0, y: 0 },
      { x: 400, y: 0 },
      { x: 200, y: 300 },
    ];
    const t = fitIslands(points, { width: 800, height: 600 });
    const expected = Math.min(
      MAP_VIEW.maxScale,
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
    expect(t.scale).toBe(MAP_VIEW.maxScale);
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

  it("VP-3 余白を指定できる（四辺ごと・負・NaN の余白でも壊れない）", () => {
    const points = [
      { x: 0, y: 0 },
      { x: 100, y: 50 },
    ];
    const t = fitIslands(points, { width: 400, height: 400 }, 100);
    expect(t.scale).toBeCloseTo(2);
    // 四辺ごと: 内側は x 50〜350・y 20〜320（幅 300・高さ 300）。中心は (200, 170)。
    const sides = fitIslands(
      points,
      { width: 400, height: 400 },
      { top: 20, right: 50, bottom: 80, left: 50 },
    );
    expect(sides.scale).toBeCloseTo(3);
    const c = toScreen(sides, { x: 50, y: 25 });
    expect(c.x).toBeCloseTo(200);
    expect(c.y).toBeCloseTo(170);
    for (const pad of [-10, Number.NaN]) {
      for (const v of Object.values(fitIslands(points, { width: 400, height: 400 }, pad))) {
        expect(Number.isFinite(v)).toBe(true);
      }
    }
  });
});

// Stage 1（data/stages/stage1.yaml）の島の配置。横に長く、縦に薄い。
const STAGE1: readonly Point[] = [
  { x: 680, y: 260 },
  { x: 430, y: 300 },
  { x: 200, y: 330 },
];

function screenBox(points: readonly Point[], size: Size) {
  const { transform } = layoutMap(points, size);
  const xs = points.map((p) => toScreen(transform, p).x);
  const ys = points.map((p) => toScreen(transform, p).y);
  return {
    minX: Math.min(...xs),
    maxX: Math.max(...xs),
    minY: Math.min(...ys),
    maxY: Math.max(...ys),
  };
}

describe("markScaleFor", () => {
  it("VP-4 描画領域の縦と横の比の相乗平均で拡大し、上限と下限で止める", () => {
    expect(markScaleFor({ width: MAP_VIEW.referenceWidth, height: MAP_VIEW.referenceHeight })).toBe(
      1,
    );
    expect(markScaleFor({ width: 1200, height: 540 })).toBeCloseTo(
      Math.sqrt((1200 / MAP_VIEW.referenceWidth) * (540 / MAP_VIEW.referenceHeight)),
    );
    // 横に長く縦に低いマップでも、縦の比（0.9）だけで決めない。
    expect(markScaleFor({ width: 1200, height: 324 })).toBeGreaterThan(
      324 / MAP_VIEW.referenceHeight,
    );
    expect(markScaleFor({ width: 10000, height: 10000 })).toBe(MAP_VIEW.maxMarkScale);
    expect(markScaleFor({ width: 10, height: 10 })).toBe(MAP_VIEW.minMarkScale);
    expect(markScaleFor({ width: Number.NaN, height: 100 })).toBe(1);
  });
});

describe("layoutMap", () => {
  it("VP-5 島の配置の外接矩形が、拡大した余白の内側に収まり、縦横比が保たれる", () => {
    for (const size of [
      { width: 1200, height: 600 },
      { width: 880, height: 420 },
      { width: 600, height: 900 },
    ]) {
      const { transform, markScale } = layoutMap(STAGE1, size);
      const pad = mapInsets(markScaleFor(size));
      const box = screenBox(STAGE1, size);
      expect(box.minX).toBeGreaterThanOrEqual(pad.left - 1e-9);
      expect(box.maxX).toBeLessThanOrEqual(size.width - pad.right + 1e-9);
      expect(box.minY).toBeGreaterThanOrEqual(pad.top - 1e-9);
      expect(box.maxY).toBeLessThanOrEqual(size.height - pad.bottom + 1e-9);
      // 下の余白には、島のボタンの列の分を足す。
      expect(pad.bottom).toBeCloseTo(pad.top + MAP_VIEW.bottomReserve);
      // 縦横比（外接矩形の幅 480・高さ 70）が保たれる。
      expect((box.maxX - box.minX) / (box.maxY - box.minY)).toBeCloseTo(480 / 70);
      // 余白を除いた幅か高さのどちらかいっぱいに広がる。
      const innerW = size.width - pad.left - pad.right;
      const innerH = size.height - pad.top - pad.bottom;
      const filled = Math.max((box.maxX - box.minX) / innerW, (box.maxY - box.minY) / innerH);
      expect(filled).toBeCloseTo(1);
      // 外接矩形の中心が、余白の内側の中心に来る。
      expect((box.minX + box.maxX) / 2).toBeCloseTo(pad.left + innerW / 2);
      expect((box.minY + box.maxY) / 2).toBeCloseTo(pad.top + innerH / 2);
      expect(transform.scale).toBeGreaterThan(0);
      expect(markScale).toBeGreaterThanOrEqual(MAP_VIEW.minMarkScale);
    }
  });

  it("VP-5 大きな画面ほど絵が大きい（1600×900 の画面のマップは 1280×720 より大きい）", () => {
    const big = layoutMap(STAGE1, { width: 1200, height: 600 });
    const small = layoutMap(STAGE1, { width: 880, height: 420 });
    expect(big.markScale).toBeGreaterThan(small.markScale);
    expect(big.markScale).toBeGreaterThan(1);
    expect(big.transform.scale).toBeGreaterThan(small.transform.scale);
    // 以前の上限（MAP_STYLE.maxScale）で止めず、描画領域いっぱいに広げる。
    expect(big.transform.scale).toBeGreaterThan(MAP_STYLE.maxScale);
  });

  it("VP-6 島が近くて名札が重なるなら、絵の拡大率を下げる（下限より下げない）", () => {
    const size = { width: 1200, height: 600 };
    const near = [
      { x: 0, y: 0 },
      { x: 1000, y: 0 },
      { x: 1040, y: 0 },
    ];
    const { transform, markScale } = layoutMap(near, size);
    const gap = 40 * transform.scale;
    expect(markScale).toBeLessThan(markScaleFor(size));
    expect(markScale).toBeCloseTo(Math.max(MAP_VIEW.minMarkScale, gap / MAP_VIEW.islandSpacing));
    // 隣り合う島の中心どうしの間隔が、拡大した島の間隔より狭くならない（下限で止まるときを除く）。
    if (markScale > MAP_VIEW.minMarkScale) {
      expect(gap).toBeGreaterThanOrEqual(MAP_VIEW.islandSpacing * markScale - 1e-9);
    }
  });

  it("VP-7 島が 1 つ・同じ座標に重なる・空・大きさが NaN でも壊れない", () => {
    const size = { width: 1200, height: 600 };
    for (const points of [
      [{ x: 5, y: 5 }],
      [
        { x: 5, y: 5 },
        { x: 5, y: 5 },
      ],
      [],
    ]) {
      const { transform, markScale } = layoutMap(points, size);
      expect(markScale).toBe(markScaleFor(size));
      for (const v of Object.values(transform)) expect(Number.isFinite(v)).toBe(true);
    }
    const nan = layoutMap(STAGE1, { width: Number.NaN, height: Number.NaN });
    expect(Number.isFinite(nan.markScale)).toBe(true);
    for (const v of Object.values(nan.transform)) expect(Number.isFinite(v)).toBe(true);
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
