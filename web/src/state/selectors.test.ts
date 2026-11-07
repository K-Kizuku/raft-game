import { describe, expect, it } from "vitest";

import type { Letter, Loc, NodeSummary } from "../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "./applyEvent.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import {
  estimateSimTime,
  leaderId,
  lettersAtPort,
  lettersOnFerry,
  portPiles,
} from "./selectors.ts";
import type { ClockView, World } from "./world.ts";

function letter(id: number, to: number, loc: Loc): Letter {
  return { id, kind: "raft", from: 1, to, createdAt: 0, loc, label: "heartbeat" };
}

function worldWith(patch: { letters?: Letter[]; cargo?: number[]; nodes?: NodeSummary[] }): World {
  const { snapshot } = loadMiniRound();
  if (patch.letters) snapshot.letters = patch.letters;
  if (patch.cargo && snapshot.ferries[0]) snapshot.ferries[0].cargo = patch.cargo;
  if (patch.nodes) snapshot.nodes = patch.nodes;
  return worldFromSnapshot(snapshot, 0);
}

function node(id: number, role: NodeSummary["role"], term: number): NodeSummary {
  return { id, role, term, lead: 0, commit: 0, applied: 0, lastIndex: 0 };
}

const clock: ClockView = {
  simTime: 1000,
  speed: 1,
  paused: false,
  endsAt: 60000,
  allowedSpeeds: [0.5, 1, 2, 4],
  receivedAt: 100,
};

describe("estimateSimTime", () => {
  it.each<[string, ClockView, number, number]>([
    ["一時停止中は simTime", { ...clock, paused: true }, 5000, 1000],
    ["速度 2 で 500 ms 後", { ...clock, speed: 2 }, 600, 2000],
    ["endsAt で頭打ち", { ...clock, speed: 4 }, 100000, 60000],
    ["now < receivedAt は simTime", clock, 50, 1000],
    ["速度 0.5 は半分", { ...clock, speed: 0.5 }, 600, 1250],
    ["小数のまま返す", clock, 100.5, 1000.5],
  ])("SL-1 %s", (_, c, now, expected) => {
    expect(estimateSimTime(c, now)).toBe(expected);
  });
});

describe("港と船の手紙", () => {
  const port1 = { type: "port", islandId: 1 } as const;

  it("SL-2 lettersAtPort・portPiles は宛先ごと・ID の順", () => {
    const w = worldWith({
      letters: [
        letter(5, 3, port1),
        letter(2, 2, port1),
        letter(9, 3, port1),
        letter(7, 2, { type: "ferry", ferryId: "f1" }),
        letter(8, 1, { type: "port", islandId: 2 }),
      ],
      cargo: [7],
    });
    expect(lettersAtPort(w, 1).map((l) => l.id)).toEqual([2, 5, 9]);
    const piles = portPiles(w, 1);
    expect(piles.map((p) => ({ to: p.to, letters: p.letters.map((l) => l.id) }))).toEqual([
      { to: 2, letters: [2] },
      { to: 3, letters: [5, 9] },
    ]);
    expect(portPiles(w, 3)).toEqual([]);
    expect(portPiles(w, 1)).not.toBe(piles);
  });

  it("SL-3 lettersOnFerry は cargo の順で、無い ID を飛ばす", () => {
    const onFerry = { type: "ferry", ferryId: "f1" } as const;
    const w = worldWith({
      letters: [letter(2, 2, onFerry), letter(9, 3, onFerry)],
      cargo: [9, 4, 2],
    });
    expect(lettersOnFerry(w, "f1").map((l) => l.id)).toEqual([9, 2]);
    expect(lettersOnFerry(w, "f9")).toEqual([]);
  });

  it("SL-3 記録を流した後も使える", () => {
    const { snapshot, events } = loadMiniRound();
    const w = applyEvents(
      worldFromSnapshot(snapshot, 0),
      events.filter((e) => e.seq <= 10),
    );
    expect(lettersOnFerry(w, "f1").map((l) => l.id)).toEqual([1]);
    expect(lettersAtPort(w, 1).map((l) => l.id)).toEqual([2]);
  });
});

describe("leaderId", () => {
  it.each<[string, NodeSummary[], number | null]>([
    ["代表なし", [node(1, "follower", 1), node(2, "candidate", 2)], null],
    ["1 つ", [node(1, "follower", 2), node(3, "leader", 2)], 3],
    ["term の大きいほう", [node(1, "leader", 2), node(2, "leader", 3)], 2],
    ["同じ term なら小さい ID", [node(3, "leader", 2), node(2, "leader", 2)], 2],
  ])("SL-4 %s", (_, nodes, expected) => {
    expect(leaderId(worldWith({ nodes }))).toBe(expected);
  });
});
