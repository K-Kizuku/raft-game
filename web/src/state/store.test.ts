import { describe, expect, it } from "vitest";

import type { Clock, NodeDetail, RoundResult } from "../protocol/index.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import { createGameStore } from "./store.ts";
import type { GameStore } from "./store.ts";

const CLOCK: Clock = { t: 4000, speed: 2, paused: false, endsAt: 60000 };

const RESULT: RoundResult = {
  score: { total: 100, breakdown: { requests: 100, expired: 0, ferryCost: 0, fairness: 0 } },
  stars: 1,
  stats: {
    succeeded: 1,
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

const DETAIL: NodeDetail = {
  id: 1,
  role: "leader",
  term: 2,
  vote: 1,
  lead: 1,
  commit: 1,
  applied: 1,
  lastIndex: 1,
  log: [],
  ledger: {},
  electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
};

function counted(store: GameStore): { count: () => number } {
  let n = 0;
  store.subscribe(() => {
    n++;
  });
  return { count: () => n };
}

function initial(store: GameStore): void {
  const s = store.getState();
  expect(s.world).toBeNull();
  expect(s.roundResult).toBeNull();
  expect(s.nodeDetail).toBeNull();
  expect(s.selection).toEqual({ islandId: null, ferryId: null });
}

describe("createGameStore", () => {
  it("SO-1 初めの状態", () => {
    initial(createGameStore());
  });

  it("SO-2 スナップショットの前の applyEvents は何もしない", () => {
    const store = createGameStore();
    const { count } = counted(store);
    const before = store.getState();
    store.getState().applyEvents(CLOCK, loadMiniRound().events, 1);
    expect(store.getState()).toBe(before);
    expect(count()).toBe(0);
  });

  it("SO-3 applySnapshot の後の applyEvents", () => {
    const store = createGameStore();
    const { snapshot, events } = loadMiniRound();
    store.getState().applySnapshot(snapshot, 10);
    const { count } = counted(store);

    store.getState().applyEvents(CLOCK, events.slice(0, 10), 1234);
    expect(count()).toBe(1);
    const world = store.getState().world;
    expect(world?.lastEventSeq).toBe(10);
    expect(world?.ferries.f1?.cargo).toEqual([1]);
    expect(world?.clock).toEqual({
      simTime: 4000,
      speed: 2,
      paused: false,
      endsAt: 60000,
      allowedSpeeds: [0.5, 1, 2, 4],
      receivedAt: 1234,
    });

    store.getState().applyEvents({ ...CLOCK, t: 5000 }, [], 2000);
    expect(count()).toBe(2);
    expect(store.getState().world?.clock.simTime).toBe(5000);
    expect(store.getState().world?.clock.receivedAt).toBe(2000);
    expect(store.getState().world?.lastEventSeq).toBe(10);
  });

  it("SO-4 操作の関数はストアの寿命の間同じで、this に依らない", () => {
    const store = createGameStore();
    const before = store.getState();
    const { applySnapshot, applyEvents, selectIsland, reset } = before;
    const { snapshot, events } = loadMiniRound();

    applySnapshot(snapshot, 0);
    applyEvents(CLOCK, events, 1);
    selectIsland(2);
    expect(store.getState().world?.lastEventSeq).toBe(30);
    expect(store.getState().selection.islandId).toBe(2);

    const after = store.getState();
    for (const key of [
      "applySnapshot",
      "applyEvents",
      "setRoundResult",
      "setNodeDetail",
      "selectIsland",
      "selectFerry",
      "reset",
    ] as const) {
      expect(after[key], key).toBe(before[key]);
    }
    reset();
    expect(store.getState().applySnapshot).toBe(applySnapshot);
  });

  it("SO-5 選択", () => {
    const store = createGameStore();
    const s = () => store.getState();
    s().selectIsland(1);
    s().selectFerry("f1");
    expect(s().selection).toEqual({ islandId: null, ferryId: null });

    const { snapshot } = loadMiniRound();
    s().applySnapshot(snapshot, 0);
    const { count } = counted(store);

    s().selectIsland(2);
    expect(s().selection.islandId).toBe(2);
    expect(count()).toBe(1);
    s().selectIsland(2);
    s().selectIsland(9);
    expect(count()).toBe(1);
    expect(s().selection.islandId).toBe(2);

    s().selectFerry("f1");
    expect(s().selection).toEqual({ islandId: 2, ferryId: "f1" });
    expect(count()).toBe(2);
    s().selectFerry("f1");
    s().selectFerry("f9");
    expect(count()).toBe(2);

    // 島 2 と船 f1 の無いスナップショットで選択を外す
    const next = loadMiniRound().snapshot;
    next.stage.islands = next.stage.islands.filter((i) => i.id !== 2);
    next.nodes = next.nodes.filter((n) => n.id !== 2);
    next.ferries = [];
    s().applySnapshot(next, 0);
    expect(s().selection).toEqual({ islandId: null, ferryId: null });

    // 島が stage に無くても nodes にあれば選べる
    const nodesOnly = loadMiniRound().snapshot;
    nodesOnly.stage.islands = [];
    s().applySnapshot(nodesOnly, 0);
    s().selectIsland(3);
    expect(s().selection.islandId).toBe(3);
    s().selectIsland(null);
    expect(s().selection.islandId).toBeNull();
  });

  it("SO-5 選んでいる島・船が残るスナップショットでは選択を保つ", () => {
    const store = createGameStore();
    const { snapshot } = loadMiniRound();
    store.getState().applySnapshot(snapshot, 0);
    store.getState().selectIsland(2);
    store.getState().selectFerry("f1");
    const selection = store.getState().selection;
    store.getState().applySnapshot(loadMiniRound().snapshot, 0);
    expect(store.getState().selection).toBe(selection);
  });

  it("SO-6 setRoundResult・setNodeDetail と reset", () => {
    const store = createGameStore();
    const s = () => store.getState();
    s().setRoundResult(RESULT);
    s().setNodeDetail(DETAIL);
    const { snapshot, events } = loadMiniRound();
    s().applySnapshot(snapshot, 0);
    expect(s().roundResult).toBe(RESULT);
    expect(s().nodeDetail).toBe(DETAIL);
    s().setNodeDetail(null);
    expect(s().nodeDetail).toBeNull();
    s().setNodeDetail(DETAIL);
    s().selectIsland(1);

    s().reset();
    initial(store);

    const { count } = counted(store);
    s().applyEvents(CLOCK, events, 1);
    expect(s().world).toBeNull();
    expect(count()).toBe(0);
  });

  it("SO-7 2 つのストアは互いに影響しない", () => {
    const a = createGameStore();
    const b = createGameStore();
    const { snapshot } = loadMiniRound();
    a.getState().applySnapshot(snapshot, 0);
    a.getState().selectIsland(1);
    a.getState().setRoundResult(RESULT);
    initial(b);
    expect(a.getState().applySnapshot).not.toBe(b.getState().applySnapshot);
    b.getState().applySnapshot(loadMiniRound().snapshot, 0);
    b.getState().reset();
    expect(a.getState().world).not.toBeNull();
    expect(a.getState().selection.islandId).toBe(1);
  });
});
