import { describe, expect, it } from "vitest";

import { containerStartTimeoutMsOf, readGameTimeLimits } from "./config.ts";
import { FakeCostGuard } from "./test-fakes.ts";

describe("K-1 CONTAINER_START_TIMEOUT_MS", () => {
  it("なし → 30000", () => {
    expect(containerStartTimeoutMsOf({})).toBe(30000);
  });

  it.each([
    ["5000", 5000],
    ["1000", 1000],
    ["120000", 120000],
  ])("%s → %d", (raw, expected) => {
    expect(containerStartTimeoutMsOf({ CONTAINER_START_TIMEOUT_MS: raw })).toBe(expected);
  });

  it.each<[string, unknown]>([
    ["999", "999"],
    ["120001", "120001"],
    ["1.5", "1.5"],
    ["先頭の空白", " 5000"],
    ["先頭の 0", "05000"],
    ["abc", "abc"],
    ["数の 5000", 5000],
  ])("%s → 30000", (_name, raw) => {
    expect(containerStartTimeoutMsOf({ CONTAINER_START_TIMEOUT_MS: raw })).toBe(30000);
  });
});

describe("K-2 gameTime の読み込み", () => {
  it("view() の maxMinutes・maxAlarms を使う", async () => {
    expect(await readGameTimeLimits(new FakeCostGuard([], 10, 5))).toEqual({
      maxMinutes: 10,
      maxAlarms: 5,
    });
  });

  it("view() が例外 → 30・20", async () => {
    const costGuard = new FakeCostGuard([]);
    costGuard.view = async () => {
      throw new Error("down");
    };
    expect(await readGameTimeLimits(costGuard)).toEqual({ maxMinutes: 30, maxAlarms: 20 });
  });

  it("guards_invalid → 30・20", async () => {
    const costGuard = new FakeCostGuard([]);
    costGuard.view = async () => "guards_invalid";
    expect(await readGameTimeLimits(costGuard)).toEqual({ maxMinutes: 30, maxAlarms: 20 });
  });

  it("costGuard なし → 30・20", async () => {
    expect(await readGameTimeLimits(undefined)).toEqual({ maxMinutes: 30, maxAlarms: 20 });
  });
});
