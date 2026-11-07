import { describe, expect, it } from "vitest";

import miniRecord from "../fixtures/mini-record.json";
import type { IslandState, Score } from "../../protocol/index.ts";
import { parseRecord, snapshotFromRecord } from "./record.ts";

const clone = (): Record<string, unknown> =>
  structuredClone(miniRecord) as unknown as Record<string, unknown>;

describe("parseRecord（replay）", () => {
  it("RP-2: bot が空文字列の記録（simcli replay --trace・WASM の出力）を読める", () => {
    const input = clone();
    input["bot"] = "";
    const record = parseRecord(input);
    expect(record.header.bot).toBe("");
    expect(record.events).toHaveLength(30);
  });
});

describe("snapshotFromRecord（replay）", () => {
  it("RP-3: islandsState・score・sessionId を渡すとスナップショットに入り、配列を共有しない", () => {
    const record = parseRecord(clone());
    const islandsState: IslandState[] = [
      { id: 1, satisfaction: 80, lastDeliveredAt: 1200 },
      { id: 2, satisfaction: 40, lastDeliveredAt: null },
    ];
    const score: Score = { total: 30, successes: 2, target: 5, criticalSecured: ["water"] };
    const snapshot = snapshotFromRecord(record, {
      islandsState,
      score,
      sessionId: "replay_mini_1",
    });
    expect(snapshot.session.id).toBe("replay_mini_1");
    expect(snapshot.islandsState).toEqual(islandsState);
    expect(snapshot.score).toEqual(score);

    expect(snapshot.islandsState).not.toBe(islandsState);
    expect(snapshot.score.criticalSecured).not.toBe(score.criticalSecured);
    snapshot.islandsState.pop();
    snapshot.score.criticalSecured.push("food");
    expect(islandsState).toHaveLength(2);
    expect(score.criticalSecured).toEqual(["water"]);
  });

  it("RP-3: 省略すると今と同じ値", () => {
    const record = parseRecord(clone());
    const snapshot = snapshotFromRecord(record);
    expect(snapshot.session.id).toBe("mock_mini_1");
    expect(snapshot.islandsState).toEqual([]);
    expect(snapshot.score).toEqual({ total: 0, successes: 0, target: 0, criticalSecured: [] });
    expect(snapshotFromRecord(record, {})).toEqual(snapshot);
  });
});
