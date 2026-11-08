import { describe, expect, it } from "vitest";

import type { Clock, RoundResult } from "../protocol/index.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import { createGameStore } from "./store.ts";
import { EMPTY_TIMELINE } from "./timeline.ts";

const CLOCK: Clock = { t: 4000, speed: 1, paused: false, endsAt: 60000 };

const RESULT: RoundResult = {
  score: { total: 0, breakdown: { requests: 0, expired: 0, ferryCost: 0, fairness: 0 } },
  stars: 0,
  stats: {
    succeeded: 0,
    rejected: 0,
    expired: 0,
    lettersDelivered: {},
    leaderChanges: 1,
    avgCommitLatencyMs: 0,
    longestSilenceMs: 0,
    finalLogs: [],
  },
  story: [],
  inputLogAvailable: false,
};

describe("ストアのタイムライン", () => {
  it("ST-1 applySnapshot → applyEvents で記録し、round.result の後も残り、reset で消える", () => {
    const store = createGameStore();
    expect(store.getState().timeline).toBe(EMPTY_TIMELINE);
    const { snapshot, events } = loadMiniRound();
    store.getState().applySnapshot(snapshot, 0);
    expect(store.getState().timeline.sessionId).toBe(snapshot.session.id);

    const picked = events.filter(
      (event) => event.type === "node.state" || event.type === "node.leader_elected",
    );
    store.getState().applyEvents(CLOCK, picked, 1);
    const timeline = store.getState().timeline;
    const kinds = timeline.items.map((item) => item.kind);
    expect(kinds).toContain("elected");
    expect(kinds).toContain("term");
    expect(timeline.lastSeq).toBe(picked[picked.length - 1]?.seq);

    store.getState().setRoundResult(RESULT);
    expect(store.getState().timeline).toBe(timeline);

    store.getState().reset();
    expect(store.getState().timeline).toBe(EMPTY_TIMELINE);
  });

  it("ST-2 world が null のときの applyEvents は timeline を変えない", () => {
    const store = createGameStore();
    const before = store.getState().timeline;
    store.getState().applyEvents(CLOCK, loadMiniRound().events, 1);
    expect(store.getState().timeline).toBe(before);
    expect(store.getState().world).toBeNull();
  });
});
