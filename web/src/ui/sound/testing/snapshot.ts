// テストのための Snapshot とイベント（T-434）。sound のテストだけが import する（本番のコードから import しない）。
import type { Clock, ServerEvent, Snapshot } from "../../../protocol/index.ts";

export const CLOCK: Clock = { t: 20000, speed: 1, paused: false, endsAt: 180000 };

export function soundSnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  const island = (id: number, name: string, x: number) => ({
    id,
    name,
    municipality: `${name}村`,
    layout: { x, y: 100 },
    population: { value: 1000, asOf: "2020-10-01", provenance: "fact" as const, source: "テスト" },
    color: "#0891B2",
  });
  return {
    protocol: 1,
    session: { id: "s-sound", stageId: "stage-test", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { ...CLOCK, allowedSpeeds: [1, 2] },
    stage: {
      title: "音の海",
      islands: [island(1, "一の島", 100), island(2, "二の島", 300)],
      routes: [{ id: "1-2", a: 1, b: 2, travelMs: 6000, provenance: "derived" }],
      resources: [],
    },
    ferries: [
      {
        id: "f1",
        name: "一号",
        capacity: 4,
        state: "docked",
        islandId: 1,
        departAt: 0,
        arriveAt: 0,
        readyAt: 0,
        cargo: [7],
      },
      {
        id: "f2",
        name: "二号",
        capacity: 4,
        state: "sailing",
        from: 1,
        to: 2,
        departAt: 15000,
        arriveAt: 21000,
        readyAt: 0,
        cargo: [],
      },
    ],
    routesState: [{ id: "1-2", blockedUntil: null }],
    letters: [
      {
        id: 7,
        kind: "raft",
        from: 1,
        to: 2,
        createdAt: 1000,
        loc: { type: "ferry", ferryId: "f1" },
        label: "heartbeat",
      },
    ],
    requests: [
      {
        id: "r1",
        origin: 1,
        kind: "stall.reserve",
        resource: "stall:main",
        critical: false,
        createdAt: 10000,
        deadline: 70000,
        status: "committed",
        expired: false,
        result: null,
      },
    ],
    nodes: [],
    islandsState: [],
    score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
    lastSeq: 10,
    ...overrides,
  };
}

export const departed = (seq: number): ServerEvent => ({
  type: "ferry.departed",
  seq,
  t: 20000,
  ferryId: "f1",
  from: 1,
  to: 2,
  departAt: 20000,
  arriveAt: 26000,
});

export const arrived = (seq: number): ServerEvent => ({
  type: "ferry.arrived",
  seq,
  t: 21000,
  ferryId: "f2",
  islandId: 2,
  readyAt: 22000,
});

export const delivered = (seq: number): ServerEvent => ({
  type: "letter.delivered",
  seq,
  t: 21000,
  letterId: 7,
  islandId: 2,
  accepted: true,
});

export const dropped = (seq: number): ServerEvent => ({
  type: "letter.dropped",
  seq,
  t: 21000,
  letterId: 7,
  reason: "overflow",
});

export const requestUpdated = (seq: number, status: "succeeded" | "rejected"): ServerEvent => ({
  type: "request.updated",
  seq,
  t: 21000,
  id: "r1",
  status,
  expired: false,
  result: null,
});

export const forecast = (seq: number, event: "storm" | "rush"): ServerEvent => ({
  type: "forecast",
  seq,
  t: 21000,
  event,
  at: 40000,
  routeIds: event === "storm" ? ["1-2"] : [],
});
