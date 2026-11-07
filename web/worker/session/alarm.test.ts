import { describe, expect, it } from "vitest";

import { desiredAlarmAt } from "./alarm.ts";

describe("A-1 desiredAlarmAt", () => {
  const now = 1_000_000;

  it("接続中 → deadline_at", () => {
    expect(desiredAlarmAt({ deadlineAt: now + 1_800_000, idleSince: null }, true, now)).toBe(
      now + 1_800_000,
    );
  });

  it("接続なし → min(deadline_at, idle_since + 300000)", () => {
    expect(desiredAlarmAt({ deadlineAt: now + 1_800_000, idleSince: now }, false, now)).toBe(
      now + 300_000,
    );
    expect(desiredAlarmAt({ deadlineAt: now + 100_000, idleSince: now }, false, now)).toBe(
      now + 100_000,
    );
  });

  it("idle_since が NULL で接続なし → now + 300000 と deadline_at の小さいほう", () => {
    expect(desiredAlarmAt({ deadlineAt: now + 1_800_000, idleSince: null }, false, now)).toBe(
      now + 300_000,
    );
    expect(desiredAlarmAt({ deadlineAt: now + 200_000, idleSince: null }, false, now)).toBe(
      now + 200_000,
    );
  });
});
