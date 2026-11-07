import { describe, expect, it } from "vitest";

import { MAP_STYLE } from "../style.ts";
import { islandOutline, scaleOutline, smoothOutline } from "./islandShape.ts";
import { MAP_ART_STYLE } from "./style.ts";

const R = MAP_STYLE.islandRadius;

describe("islandOutline", () => {
  it("同じ id なら同じ頂点を返す", () => {
    expect(islandOutline(3, R)).toEqual(islandOutline(3, R));
  });

  it("違う id なら違う形になる", () => {
    const shapes = [1, 2, 3, 4, 5].map((id) => JSON.stringify(islandOutline(id, R)));
    expect(new Set(shapes).size).toBe(shapes.length);
  });

  it("すべての頂点が中心から radius × 0.82 以上 radius 以下にある", () => {
    for (let id = 0; id < 200; id++) {
      for (const p of islandOutline(id, R)) {
        const d = Math.hypot(p.x, p.y);
        expect(d).toBeGreaterThanOrEqual(R * 0.82 - 1e-9);
        expect(d).toBeLessThanOrEqual(R + 1e-9);
      }
    }
  });

  it("当たり判定の円（islandRadius + islandHitSlop）からはみ出さない", () => {
    const hit = MAP_STYLE.islandRadius + MAP_STYLE.islandHitSlop;
    for (let id = 0; id < 50; id++) {
      for (const p of islandOutline(id, hit)) expect(Math.hypot(p.x, p.y)).toBeLessThanOrEqual(hit);
    }
  });

  it("頂点の数が指定どおり（省略すると MAP_ART_STYLE.islandPoints）", () => {
    expect(islandOutline(1, R, 8)).toHaveLength(8);
    expect(islandOutline(1, R, 20)).toHaveLength(20);
    expect(islandOutline(1, R)).toHaveLength(MAP_ART_STYLE.islandPoints);
  });

  it("大きさを変えても同じ形（相似）", () => {
    const small = islandOutline(7, 10);
    const large = islandOutline(7, 20);
    small.forEach((p, i) => {
      expect(large[i]?.x).toBeCloseTo(p.x * 2);
      expect(large[i]?.y).toBeCloseTo(p.y * 2);
    });
  });
});

describe("scaleOutline", () => {
  it("頂点を scale 倍にする", () => {
    expect(scaleOutline([{ x: 2, y: -4 }], 0.5)).toEqual([{ x: 1, y: -2 }]);
  });
});

describe("smoothOutline", () => {
  it("隣の頂点の中点を通り、頂点を制御点にした閉じた曲線になる", () => {
    const square = [
      { x: 1, y: 0 },
      { x: 0, y: 1 },
      { x: -1, y: 0 },
      { x: 0, y: -1 },
    ];
    const { start, curves } = smoothOutline(square);
    expect(start).toEqual({ x: 0.5, y: -0.5 });
    expect(curves).toHaveLength(4);
    expect(curves[0]).toEqual([
      { x: 1, y: 0 },
      { x: 0.5, y: 0.5 },
    ]);
    expect(curves[3]?.[1]).toEqual(start); // 最後に始点へ戻る
  });

  it("頂点が無ければ空の曲線", () => {
    expect(smoothOutline([]).curves).toEqual([]);
  });
});
