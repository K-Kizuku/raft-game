import { describe, expect, it } from "vitest";

import type { FerryView } from "../../state/index.ts";
import { ferryProgress, lerp } from "./ferry.ts";

function sailing(departAt: number, arriveAt: number): FerryView {
  return {
    id: "f1",
    name: "テスト丸",
    capacity: 4,
    state: "sailing",
    from: 1,
    to: 2,
    departAt,
    arriveAt,
    readyAt: 0,
    cargo: [],
    brokenUntil: null,
  };
}

describe("ferryProgress", () => {
  it.each([
    { simNow: 2000, want: 0 },
    { simNow: 3000, want: 0 },
    { simNow: 5000, want: 0.5 },
    { simNow: 7000, want: 1 },
    { simNow: 9000, want: 1 },
  ])("FP-1 departAt 3000・arriveAt 7000 で simNow $simNow → $want", ({ simNow, want }) => {
    expect(ferryProgress(sailing(3000, 7000), simNow)).toBeCloseTo(want);
  });

  it("FP-1 arriveAt ≤ departAt → 1、docked → 1", () => {
    expect(ferryProgress(sailing(5000, 5000), 0)).toBe(1);
    expect(ferryProgress(sailing(5000, 4000), 0)).toBe(1);
    const docked: FerryView = {
      id: "f2",
      name: "停泊丸",
      capacity: 4,
      state: "docked",
      islandId: 1,
      departAt: 0,
      arriveAt: 0,
      readyAt: 0,
      cargo: [],
      brokenUntil: null,
    };
    expect(ferryProgress(docked, 100)).toBe(1);
  });
});

describe("lerp", () => {
  it("FP-2 端と中点", () => {
    const a = { x: 10, y: 20 };
    const b = { x: 110, y: -80 };
    expect(lerp(a, b, 0)).toEqual(a);
    expect(lerp(a, b, 1)).toEqual(b);
    const mid = lerp(a, b, 0.5);
    expect(mid.x).toBeCloseTo(60);
    expect(mid.y).toBeCloseTo(-30);
  });
});
