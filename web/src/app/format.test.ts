import { describe, expect, it } from "vitest";

import { formatDuration } from "./format.ts";

describe("formatDuration", () => {
  // FM-1
  it.each<[number, string]>([
    [0, "0:00"],
    [999, "0:00"],
    [59999, "0:59"],
    [60000, "1:00"],
    [180000, "3:00"],
    [210000, "3:30"],
    [3600000, "60:00"],
  ])("%d → %s", (ms, want) => {
    expect(formatDuration(ms)).toBe(want);
  });
});
