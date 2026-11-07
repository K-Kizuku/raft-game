import { describe, expect, it } from "vitest";

import {
  adjacentSpeeds,
  festReadiness,
  formatRemaining,
  formatScore,
  formatSpeed,
} from "./format.ts";

describe("format", () => {
  it.each([
    [151000, "2:31"],
    [150400, "2:31"],
    [150000, "2:30"],
    [1, "0:01"],
    [0, "0:00"],
    [-500, "0:00"],
  ])("FM-1 formatRemaining(%d) は %s", (ms, want) => {
    expect(formatRemaining(ms)).toBe(want);
  });

  it("FM-2 formatScore・formatSpeed", () => {
    expect(formatScore(1240)).toBe("1,240");
    expect(formatScore(0)).toBe("0");
    expect(formatScore(-50)).toBe("-50");
    expect(formatSpeed(0.5)).toBe("0.5x");
    expect(formatSpeed(1)).toBe("1x");
    expect(formatSpeed(4)).toBe("4x");
  });

  it("FM-3 festReadiness", () => {
    expect(festReadiness(5, 8)).toEqual({ done: 5, target: 8, percent: 62 });
    expect(festReadiness(9, 8)).toEqual({ done: 8, target: 8, percent: 100 });
    expect(festReadiness(0, 8)).toEqual({ done: 0, target: 8, percent: 0 });
    expect(festReadiness(3, 0)).toEqual({ done: 3, target: 0, percent: 0 });
  });

  it.each<[readonly number[], number, { slower: number | null; faster: number | null }]>([
    [[0.5, 1, 2, 4], 1, { slower: 0.5, faster: 2 }],
    [[0.5, 1, 2, 4], 4, { slower: 2, faster: null }],
    [[0.5, 1, 2, 4], 0.5, { slower: null, faster: 1 }],
    [[0.5, 1, 2, 4], 8, { slower: 4, faster: null }],
    [[], 1, { slower: null, faster: null }],
    [[4, 1, 2, 0.5, 2, 1], 1, { slower: 0.5, faster: 2 }],
    [[4, 4, 0.5, 2, 1, 0.5], 8, { slower: 4, faster: null }],
  ])("FM-4 adjacentSpeeds(%j, %d)", (allowed, current, want) => {
    expect(adjacentSpeeds(allowed, current)).toEqual(want);
  });
});
