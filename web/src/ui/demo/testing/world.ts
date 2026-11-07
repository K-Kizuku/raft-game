// テストのための Snapshot（T-411）。demo のテストだけが import する（本番のコードから import しない）。
// デモのセッション（session.demo が真）の航行中で、速度は 1、8 倍速まで選べる。
import type { Snapshot } from "../../../protocol/index.ts";

export function makeSnapshot(
  overrides: Partial<Snapshot> = {},
  clock: Partial<Snapshot["clock"]> = {},
): Snapshot {
  const base: Snapshot = {
    protocol: 1,
    session: { id: "s-demo", stageId: "stage-demo", seed: 1, demo: true, restored: false },
    phase: "sailing",
    clock: {
      t: 29000,
      speed: 1,
      paused: false,
      endsAt: 180000,
      allowedSpeeds: [0.5, 1, 2, 4, 8],
    },
    stage: {
      title: "デモの海",
      islands: [
        {
          id: 1,
          name: "一の島",
          municipality: "一の島村",
          layout: { x: 100, y: 100 },
          population: { value: 1000, asOf: "2020-10-01", provenance: "fact", source: "テスト" },
          color: "#E11D48",
        },
        {
          id: 2,
          name: "二の島",
          municipality: "二の島村",
          layout: { x: 300, y: 100 },
          population: { value: 2000, asOf: "2020-10-01", provenance: "fact", source: "テスト" },
          color: "#0891B2",
        },
      ],
      routes: [{ id: "1-2", a: 1, b: 2, travelMs: 6000, provenance: "derived" }],
      resources: [],
    },
    ferries: [],
    routesState: [{ id: "1-2", blockedUntil: null }],
    letters: [],
    requests: [],
    nodes: [
      { id: 1, role: "leader", term: 2, vote: 1, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
      { id: 2, role: "follower", term: 2, vote: 1, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
    ],
    islandsState: [
      { id: 1, satisfaction: 60, lastDeliveredAt: null },
      { id: 2, satisfaction: 60, lastDeliveredAt: null },
    ],
    score: { total: 0, successes: 0, target: 8, criticalSecured: [] },
    lastSeq: 0,
    ...overrides,
  };
  return { ...base, clock: { ...base.clock, ...clock } };
}
