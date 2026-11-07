import { describe, expect, it } from "vitest";

import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { islandLabel, openingResult } from "./openingView.ts";

type ServerEvent = Parameters<typeof applyEvents>[1][number];

// mini のスナップショットに、seq が [from, to] のイベントを当てはめた世界。
function miniWorld(from = 1, to = 0): World {
  const { snapshot, events } = loadMiniRound();
  const world = worldFromSnapshot(snapshot, 0);
  return applyEvents(
    world,
    events.filter((e) => e.seq >= from && e.seq <= to),
  );
}

describe("openingResult", () => {
  it("OV-1 世界が無い・intro のスナップショットだけなら null", () => {
    expect(openingResult(null)).toBeNull();
    expect(openingResult(miniWorld())).toBeNull();
  });

  it("OV-2 seq 1〜6 を当てはめた世界は一の島の当選", () => {
    const result = openingResult(miniWorld(1, 6));
    expect(result).not.toBeNull();
    expect(result?.leaderId).toBe(1);
    expect(result?.term).toBe(2);
    expect(result?.islandName).toBe("一の島");
    expect(result?.color).toBe("#E07A5F");
    expect(result?.key).toBe("mock_mini_1:5");
    expect(result?.changes).toHaveLength(1);
  });

  it("OV-3 開会式で 2 回当選したら最後の当選が結果", () => {
    const opening: ServerEvent[] = [
      { seq: 1, t: 0, type: "phase.changed", phase: "opening" },
      { seq: 2, t: 0, type: "node.leader_elected", id: 1, term: 2 },
      { seq: 3, t: 0, type: "node.stepped_down", id: 1, term: 3, reason: "higher_term" },
      { seq: 4, t: 0, type: "node.leader_elected", id: 2, term: 3 },
      { seq: 5, t: 0, type: "phase.changed", phase: "sailing" },
    ];
    const { snapshot } = loadMiniRound();
    const world = applyEvents(worldFromSnapshot(snapshot, 0), opening);
    const result = openingResult(world);
    expect(result?.leaderId).toBe(2);
    expect(result?.term).toBe(3);
    expect(result?.islandName).toBe("二の島");
    expect(result?.key).toBe("mock_mini_1:4");
    expect(result?.changes.map((c) => [c.seq, c.kind, c.id])).toEqual([
      [2, "elected", 1],
      [3, "stepped_down", 1],
      [4, "elected", 2],
    ]);
  });

  it("OV-4 時刻 0 に elected が無ければ null。全イベントでも開会式の結果は変わらない", () => {
    const base = miniWorld(1, 6);
    const later: World = {
      ...base,
      leaderChanges: base.leaderChanges.map((c) => ({ ...c, t: 21000 })),
    };
    expect(openingResult(later)).toBeNull();

    const all = openingResult(miniWorld(1, 30));
    expect(all).toEqual(openingResult(miniWorld(1, 6)));
    expect(all?.changes).toHaveLength(1);
    const world = miniWorld(1, 30);
    expect(world.leaderChanges.some((c) => c.kind === "stepped_down" && c.t === 21000)).toBe(true);
  });

  it("OV-5 stage に無い島の当選は「島 {id}」で色なし", () => {
    const { snapshot } = loadMiniRound();
    const world = applyEvents(worldFromSnapshot(snapshot, 0), [
      { seq: 1, t: 0, type: "phase.changed", phase: "opening" },
      { seq: 2, t: 0, type: "node.leader_elected", id: 9, term: 1 },
    ]);
    const result = openingResult(world);
    expect(result?.leaderId).toBe(9);
    expect(result?.islandName).toBe("島 9");
    expect(result?.color).toBeNull();
    expect(islandLabel(world, 9)).toBe("島 9");
    expect(islandLabel(world, 3)).toBe("三の島");
  });

  it("OV-6 phase が intro のままなら時刻 0 の elected があっても null", () => {
    const base = miniWorld(1, 6);
    const world: World = { ...base, phase: "intro" };
    expect(world.leaderChanges.some((c) => c.kind === "elected" && c.t === 0)).toBe(true);
    expect(openingResult(world)).toBeNull();
  });
});
