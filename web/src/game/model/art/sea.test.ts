import { describe, expect, it } from "vitest";

import { seaBlobs, waveMarks } from "./sea.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

describe("seaBlobs", () => {
  const size = { width: 1920, height: 1080 };

  it("同じ大きさなら同じ結果", () => {
    expect(seaBlobs(size, 10)).toEqual(seaBlobs({ width: 1920, height: 1080 }, 10));
  });

  it("数が指定どおりで、にじみの中心は画面の中・半径は決まった範囲", () => {
    const blobs = seaBlobs(size, 12);
    expect(blobs).toHaveLength(12);
    const short = Math.min(size.width, size.height);
    for (const b of blobs) {
      expect(Math.abs(b.x - size.width / 2)).toBeLessThanOrEqual(size.width / 2);
      expect(Math.abs(b.y - size.height / 2)).toBeLessThanOrEqual(size.height / 2);
      expect(b.r).toBeGreaterThanOrEqual(short * A.seaBlobMinRatio);
      expect(b.r).toBeLessThanOrEqual(short * A.seaBlobMaxRatio);
      expect(["light", "mid", "deep"]).toContain(b.tone);
    }
  });

  it("省略すると MAP_ART_STYLE.seaBlobCount 個", () => {
    expect(seaBlobs(size)).toHaveLength(A.seaBlobCount);
  });

  it("大きさ 0 では例外を出さず空の配列", () => {
    expect(seaBlobs({ width: 0, height: 0 }, 10)).toEqual([]);
    expect(seaBlobs({ width: 800, height: 0 }, 10)).toEqual([]);
    expect(seaBlobs(size, 0)).toEqual([]);
  });
});

describe("waveMarks", () => {
  it("同じ大きさなら同じ結果で、画面を覆う", () => {
    const size = { width: 960, height: 540 };
    const marks = waveMarks(size);
    expect(marks).toEqual(waveMarks({ width: 960, height: 540 }));
    expect(marks.length).toBeGreaterThan(10);
    for (const m of marks) {
      expect(m.y).toBeGreaterThanOrEqual(-A.waveSpacing);
      expect(m.y).toBeLessThanOrEqual(size.height + A.waveSpacing);
      expect(m.x).toBeGreaterThanOrEqual(-A.waveSpacing);
      expect(m.x).toBeLessThanOrEqual(size.width + A.waveSpacing);
    }
  });

  it("大きさ 0 では空の配列", () => {
    expect(waveMarks({ width: 0, height: 0 })).toEqual([]);
  });
});
