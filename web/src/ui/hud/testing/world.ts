// テストのための Snapshot（T-406）。hud・island のテストだけが import する（本番のコードから import しない）。
import type { Island, Letter, Request, Route, Snapshot } from "../../../protocol/index.ts";

function island(id: number, name: string, color: string, x: number, y: number): Island {
  return {
    id,
    name,
    municipality: `${name}村`,
    layout: { x, y },
    population: { value: 1000 * id, asOf: "2020-10-01", provenance: "fact", source: "テスト" },
    color,
  };
}

function route(a: number, b: number): Route {
  return { id: `${String(a)}-${String(b)}`, a, b, travelMs: 6000, provenance: "derived" };
}

export const ISLAND_COLORS: Readonly<Record<number, string>> = {
  1: "#E11D48",
  2: "#0891B2",
  3: "#65A30D",
};

export function makeSnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  return {
    protocol: 1,
    session: { id: "s-test", stageId: "stage-test", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { t: 29000, speed: 1, paused: false, endsAt: 180000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: {
      title: "テストの海",
      islands: [
        island(1, "一の島", ISLAND_COLORS[1] ?? "", 100, 100),
        island(2, "二の島", ISLAND_COLORS[2] ?? "", 300, 100),
        island(3, "三の島", ISLAND_COLORS[3] ?? "", 200, 300),
      ],
      routes: [route(1, 2), route(1, 3), route(2, 3)],
      resources: [
        { id: "firework:a", kind: "firework.reserve", label: "大花火 A", critical: true },
        { id: "firework:b", kind: "firework.reserve", label: "大花火 B", critical: true },
        { id: "stall:main", kind: "stall.reserve", label: "屋台", critical: false },
      ],
    },
    ferries: [],
    routesState: [
      { id: "1-2", blockedUntil: null },
      { id: "1-3", blockedUntil: null },
      { id: "2-3", blockedUntil: null },
    ],
    letters: [],
    requests: [],
    nodes: [
      { id: 1, role: "leader", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
      { id: 2, role: "follower", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
      { id: 3, role: "follower", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
    ],
    islandsState: [
      { id: 1, satisfaction: 72, lastDeliveredAt: null },
      { id: 2, satisfaction: 60, lastDeliveredAt: null },
      { id: 3, satisfaction: 60, lastDeliveredAt: null },
    ],
    score: { total: 1240, successes: 5, target: 8, criticalSecured: ["firework:a"] },
    lastSeq: 0,
    ...overrides,
  };
}

// 港にある手紙（label は heartbeat）。
export function portLetter(
  id: number,
  from: number,
  to: number,
  overrides: Partial<Letter> = {},
): Letter {
  return {
    id,
    kind: "raft",
    from,
    to,
    createdAt: 1000,
    loc: { type: "port", islandId: from },
    label: "heartbeat",
    ...overrides,
  };
}

export function makeRequest(id: string, origin: number, overrides: Partial<Request> = {}): Request {
  return {
    id,
    origin,
    kind: "stall.reserve",
    resource: "stall:main",
    critical: false,
    createdAt: 10000,
    deadline: 70000,
    status: "received",
    expired: false,
    result: null,
    ...overrides,
  };
}
