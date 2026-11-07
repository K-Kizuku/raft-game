import { describe, expect, it } from "vitest";

import { MAP_STYLE } from "./style.ts";
import { smoothSimTime } from "./time.ts";

const hold = MAP_STYLE.timeHoldMs;

describe("smoothSimTime", () => {
  it.each([
    { name: "null なら推定のまま", prev: null, estimate: 5000, want: 5000 },
    { name: "進んだら推定", prev: 5000, estimate: 5100, want: 5100 },
    { name: "同じなら推定", prev: 5000, estimate: 5000, want: 5000 },
    { name: "少し戻ったら前の値", prev: 5000, estimate: 5000 - hold / 2, want: 5000 },
    { name: "ちょうど timeHoldMs 戻ったら前の値", prev: 5000, estimate: 5000 - hold, want: 5000 },
    { name: "大きく戻ったら推定", prev: 5000, estimate: 5000 - hold - 1, want: 5000 - hold - 1 },
  ])("TM-1 $name", ({ prev, estimate, want }) => {
    expect(smoothSimTime(prev, estimate)).toBe(want);
  });
});
