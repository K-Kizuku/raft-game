import { describe, expect, it } from "vitest";

import type { ServerEvent } from "../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "./applyEvent.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import type { World } from "./world.ts";

function start(): { world: World; events: ServerEvent[] } {
  const { snapshot, events } = loadMiniRound();
  return { world: worldFromSnapshot(snapshot, 0), events };
}

function upTo(seq: number): World {
  const { world, events } = start();
  return applyEvents(
    world,
    events.filter((e) => e.seq <= seq),
  );
}

function inChunks(world: World, events: readonly ServerEvent[], size: number): World {
  let current = world;
  for (let i = 0; i < events.length; i += size) {
    current = applyEvents(current, events.slice(i, i + size));
  }
  return current;
}

describe("記録を流す", () => {
  it("RC-1 30 件を当てはめた最後の World", () => {
    const { world, events } = start();
    expect(events).toHaveLength(30);
    const w = applyEvents(world, events);

    expect(w.phase).toBe("ended");
    expect(w.lastEventSeq).toBe(30);

    expect(Object.keys(w.letters)).toEqual(["2"]);
    expect(w.letters[2]?.loc).toEqual({ type: "port", islandId: 1 });
    expect(w.letters[2]?.raft?.stale).toBe(true);

    const f1 = w.ferries.f1;
    expect(f1).toMatchObject({
      state: "docked",
      islandId: 2,
      readyAt: 8500,
      departAt: 0,
      arriveAt: 0,
      cargo: [],
      brokenUntil: null,
    });
    expect(f1 && ("from" in f1 || "to" in f1)).toBe(false);

    expect(w.routeStates["2-3"]?.blockedUntil).toBeNull();
    expect(w.forecasts).toEqual([]);

    expect(w.requests["mini-0001"]).toMatchObject({ status: "succeeded", result: "ok" });
    expect(w.requestIds).toEqual(["mini-0001"]);

    expect(w.nodes[1]).toMatchObject({ role: "follower", lead: 0 });
    expect(w.islandStates[2]).toEqual({ id: 2, satisfaction: 70, lastDeliveredAt: 7000 });
    expect(w.score).toEqual({ total: 100, successes: 1, target: 0, criticalSecured: [] });

    expect(w.notices.map((n) => n.seq)).toEqual([29]);

    expect(w.letterExits).toHaveLength(2);
    const [delivered, dropped] = w.letterExits;
    expect(delivered).toMatchObject({ seq: 14, outcome: "delivered", islandId: 2, accepted: true });
    expect(delivered?.letter.id).toBe(1);
    expect(delivered?.letter.loc).toEqual({ type: "ferry", ferryId: "f1" });
    expect(dropped).toMatchObject({ seq: 19, outcome: "dropped", reason: "superseded" });
    expect(dropped?.letter.id).toBe(3);

    expect(w.leaderChanges).toEqual([
      { seq: 5, t: 0, kind: "elected", id: 1, term: 2 },
      { seq: 24, t: 21000, kind: "stepped_down", id: 1, term: 2, reason: "check_quorum" },
    ]);
  });

  it("RC-1 途中の状態", () => {
    expect(upTo(10).ferries.f1?.cargo).toEqual([1]);

    const sailing = upTo(11).ferries.f1;
    expect(sailing).toMatchObject({ state: "sailing", from: 1, to: 2 });
    expect(sailing && "islandId" in sailing).toBe(false);

    expect(upTo(12).forecasts).toHaveLength(1);

    const blocked = upTo(17);
    expect(blocked.routeStates["2-3"]?.blockedUntil).toBe(20000);
    expect(blocked.forecasts).toEqual([]);

    expect(upTo(21).ferries.f1?.brokenUntil).toBe(16000);
  });

  it("RC-2 同じイベントをもう一度流しても変わらない", () => {
    const { world, events } = start();
    const once = applyEvents(world, events);
    expect(applyEvents(once, events)).toBe(once);

    const partial = applyEvents(
      world,
      events.filter((e) => e.seq <= 11),
    );
    expect(applyEvents(partial, events)).toEqual(once);
  });

  it("RC-3 まとめ方に依らない", () => {
    const { world, events } = start();
    const all = applyEvents(world, events);
    expect(inChunks(world, events, 1)).toEqual(all);
    expect(inChunks(world, events, 7)).toEqual(all);
  });

  it("RC-4 fixture がプロトコル §6 の 22 種類を網羅する", () => {
    const { events } = loadMiniRound();
    const types = new Set(events.map((e) => e.type));
    expect([...types].sort()).toEqual(
      [
        "phase.changed",
        "letter.created",
        "letter.moved",
        "letter.delivered",
        "letter.dropped",
        "letter.stale",
        "ferry.departed",
        "ferry.arrived",
        "ferry.broken",
        "ferry.repaired",
        "route.blocked",
        "route.unblocked",
        "forecast",
        "node.state",
        "node.leader_elected",
        "node.stepped_down",
        "request.created",
        "request.updated",
        "ledger.applied",
        "island.updated",
        "score.updated",
        "notice",
      ].sort(),
    );
    expect(events.map((e) => e.seq)).toEqual(Array.from({ length: 30 }, (_, i) => i + 1));
  });
});
