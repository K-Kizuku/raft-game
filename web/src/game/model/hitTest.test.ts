import { describe, expect, it } from "vitest";

import { hitTest } from "./hitTest.ts";
import type { FerrySprite, IslandNode, MapScene } from "./scene.ts";
import { MAP_STYLE } from "./style.ts";
import type { Point } from "./viewport.ts";

const reach = MAP_STYLE.islandRadius + MAP_STYLE.islandHitSlop;

function island(id: number, at: Point): IslandNode {
  return {
    id,
    name: `島${String(id)}`,
    color: "#000000",
    at,
    roleMark: null,
    tagOffset: { x: 0, y: MAP_STYLE.islandRadius },
    selected: false,
    candidate: false,
  };
}

function ferry(id: string, at: Point): FerrySprite {
  return {
    id,
    name: id,
    at,
    heading: 0,
    state: "docked",
    cargoCount: 0,
    capacity: 4,
    etaMs: null,
    broken: false,
    auto: false,
    selected: false,
    labels: "split",
  };
}

function scene(islands: IslandNode[], ferries: FerrySprite[] = []): MapScene {
  return {
    size: { width: 800, height: 600 },
    transform: { scale: 1, offsetX: 0, offsetY: 0 },
    routes: [],
    islands,
    piles: [],
    ferries,
    partition: null,
  };
}

describe("hitTest", () => {
  const one = scene([island(1, { x: 100, y: 100 })]);

  it.each([
    { name: "島の中心", p: { x: 100, y: 100 }, want: { kind: "island", id: 1 } },
    {
      name: "半径＋余裕の内側",
      p: { x: 100 + reach - 0.5, y: 100 },
      want: { kind: "island", id: 1 },
    },
    {
      name: "半径＋余裕のちょうど",
      p: { x: 100, y: 100 + reach },
      want: { kind: "island", id: 1 },
    },
    { name: "外側（海）", p: { x: 100 + reach + 0.5, y: 100 }, want: null },
  ])("HT-1 $name", ({ p, want }) => {
    expect(hitTest(one, p)).toEqual(want);
  });

  it("HT-1 船と島が重なる点 → 船", () => {
    const s = scene([island(1, { x: 100, y: 100 })], [ferry("f1", { x: 110, y: 100 })]);
    expect(hitTest(s, { x: 105, y: 100 })).toEqual({ kind: "ferry", id: "f1" });
    // 船の当たり判定の外で、島の内側 → 島。
    expect(hitTest(s, { x: 110 - MAP_STYLE.ferryHitRadius - 1, y: 100 })).toEqual({
      kind: "island",
      id: 1,
    });
  });

  it("HT-1 2 つの島の真ん中 → 近いほう、同じ距離なら並びで先のほう", () => {
    const gap = reach * 1.5; // 真ん中からどちらにも届く距離
    const s = scene([island(2, { x: 0, y: 0 }), island(1, { x: gap, y: 0 })]);
    expect(hitTest(s, { x: gap / 2, y: 0 })).toEqual({ kind: "island", id: 2 });
    expect(hitTest(s, { x: gap / 2 + 1, y: 0 })).toEqual({ kind: "island", id: 1 });
    expect(hitTest(s, { x: gap / 2 - 1, y: 0 })).toEqual({ kind: "island", id: 2 });
  });

  it("HT-1 船どうしは近いほう", () => {
    const s = scene([], [ferry("a", { x: 0, y: 0 }), ferry("b", { x: 10, y: 0 })]);
    expect(hitTest(s, { x: 7, y: 0 })).toEqual({ kind: "ferry", id: "b" });
    expect(hitTest(s, { x: 5, y: 0 })).toEqual({ kind: "ferry", id: "a" });
  });
});
