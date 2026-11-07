// マップのテストのための Snapshot（T-404）。テストだけが import する（本番のコードから import しない）。
import type { Ferry, Island, Letter, NodeSummary, Snapshot } from "../../protocol/index.ts";

export const COLORS: Readonly<Record<1 | 2 | 3, string>> = {
  1: "#E07A5F",
  2: "#3D405B",
  3: "#81B29A",
};

function island(id: 1 | 2 | 3, name: string, x: number, y: number): Island {
  return {
    id,
    name,
    municipality: "テスト町",
    layout: { x, y },
    population: { value: 100, asOf: "令和8年4月1日", provenance: "creative", source: "test" },
    color: COLORS[id],
  };
}

export function node(id: number, role: NodeSummary["role"], term: number): NodeSummary {
  return {
    id,
    role,
    term,
    vote: role === "leader" ? id : 0,
    lead: 0,
    commit: 0,
    applied: 0,
    lastIndex: 0,
  };
}

export function dockedFerry(id: string, islandId: number, cargo: number[] = []): Ferry {
  return {
    id,
    name: `${id}丸`,
    capacity: 4,
    state: "docked",
    islandId,
    departAt: 0,
    arriveAt: 0,
    readyAt: 0,
    cargo,
  };
}

export function sailingFerry(
  id: string,
  from: number,
  to: number,
  departAt: number,
  arriveAt: number,
): Ferry {
  return {
    id,
    name: `${id}丸`,
    capacity: 4,
    state: "sailing",
    from,
    to,
    departAt,
    arriveAt,
    readyAt: 0,
    cargo: [],
  };
}

export function portLetter(
  id: number,
  islandId: number,
  to: number,
  kind: Letter["kind"] = "raft",
): Letter {
  return {
    id,
    kind,
    from: islandId,
    to,
    createdAt: 0,
    loc: { type: "port", islandId },
    label: kind === "request" ? "request" : "heartbeat",
  };
}

export function ferryLetter(id: number, ferryId: string, to: number): Letter {
  return {
    id,
    kind: "raft",
    from: 1,
    to,
    createdAt: 0,
    loc: { type: "ferry", ferryId },
    label: "heartbeat",
  };
}

// 島 3 つ（一の島 (0,0)・二の島 (400,0)・三の島 (200,300)）と航路 1-2・1-3・2-3。
export function makeSnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  return {
    protocol: 1,
    session: { id: "s-map", stageId: "stage-map", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { t: 0, speed: 1, paused: false, endsAt: 180000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: {
      title: "テストの海",
      islands: [
        island(1, "一の島", 0, 0),
        island(2, "二の島", 400, 0),
        island(3, "三の島", 200, 300),
      ],
      routes: [
        { id: "1-2", a: 1, b: 2, travelMs: 4000, provenance: "creative" },
        { id: "1-3", a: 1, b: 3, travelMs: 4000, provenance: "creative" },
        { id: "2-3", a: 2, b: 3, travelMs: 4000, provenance: "creative" },
      ],
      resources: [],
    },
    ferries: [],
    routesState: [
      { id: "1-2", blockedUntil: null },
      { id: "1-3", blockedUntil: null },
      { id: "2-3", blockedUntil: null },
    ],
    letters: [],
    requests: [],
    nodes: [node(1, "leader", 2), node(2, "follower", 2), node(3, "follower", 2)],
    islandsState: [],
    score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
    lastSeq: 0,
    ...overrides,
  };
}
