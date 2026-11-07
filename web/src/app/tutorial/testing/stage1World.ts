// テスト用の Stage 1 の世界（T-409）。チュートリアルのテストだけが import する（本番のコードから import しない）。
// ほかのモジュールの testing/ を import しない。
import { worldFromSnapshot } from "../../../state/index.ts";
import type { GameState, World } from "../../../state/index.ts";

export type Snapshot = Parameters<GameState["applySnapshot"]>[0];
type Ferry = Snapshot["ferries"][number];
type Letter = Snapshot["letters"][number];
type Request = Snapshot["requests"][number];
type Island = Snapshot["stage"]["islands"][number];
type NodeSummary = Snapshot["nodes"][number];

export const LEADER = 1; // 小豆島
export const RELAY = 2; // 豊島
export const FAR = 3; // 直島
export const FERRY_ID = "f1";

function island(id: number, name: string, x: number, y: number): Island {
  return {
    id,
    name,
    municipality: `${name}の町`,
    layout: { x, y },
    population: { value: 1000 * id, asOf: "2020-10-01", provenance: "fact", source: "テスト" },
    color: "#336699",
  };
}

function node(id: number, role: NodeSummary["role"] = "follower"): NodeSummary {
  return { id, role, term: 1, lead: LEADER, commit: 0, applied: 0, lastIndex: 0 };
}

// stage1Snapshot は航行中（時計は止めていない）の Stage 1 の Snapshot。
export function stage1Snapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  return {
    protocol: 1,
    session: { id: "s-stage1", stageId: "stage1", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { t: 1000, speed: 1, paused: false, endsAt: 300000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: {
      title: "はじめての郵便船",
      islands: [
        island(LEADER, "小豆島", 100, 100),
        island(RELAY, "豊島", 200, 150),
        island(FAR, "直島", 300, 200),
      ],
      routes: [
        { id: "1-2", a: LEADER, b: RELAY, travelMs: 6000, provenance: "derived" },
        { id: "2-3", a: RELAY, b: FAR, travelMs: 6000, provenance: "derived" },
      ],
      resources: [{ id: "stall:main", kind: "stall.reserve", label: "屋台", critical: false }],
    },
    ferries: [stage1Ferry()],
    routesState: [
      { id: "1-2", blockedUntil: null },
      { id: "2-3", blockedUntil: null },
    ],
    letters: [],
    requests: [],
    nodes: [node(LEADER, "leader"), node(RELAY), node(FAR)],
    islandsState: [LEADER, RELAY, FAR].map((id) => ({
      id,
      satisfaction: 60,
      lastDeliveredAt: null,
    })),
    score: { total: 0, successes: 0, target: 3, criticalSecured: [] },
    lastSeq: 0,
    ...overrides,
  };
}

// stage1Ferry は小豆島に停泊した「せとうち丸」（定員 4）。
export function stage1Ferry(overrides: Partial<Ferry> = {}): Ferry {
  return {
    id: FERRY_ID,
    name: "せとうち丸",
    capacity: 4,
    state: "docked",
    islandId: LEADER,
    departAt: 0,
    arriveAt: 0,
    readyAt: 0,
    cargo: [],
    ...overrides,
  };
}

// sailingFerry は from から to へ航行中の船。
export function sailingFerry(from: number, to: number, cargo: number[] = []): Ferry {
  return stage1Ferry({ state: "sailing", from, to, departAt: 1000, arriveAt: 7000, cargo });
}

// portLetter は from の港にある手紙。
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
    createdAt: 500,
    loc: { type: "port", islandId: from },
    label: "heartbeat",
    ...overrides,
  };
}

// ferryLetter は船に積んだ手紙。
export function ferryLetter(id: number, from: number, to: number): Letter {
  return portLetter(id, from, to, { loc: { type: "ferry", ferryId: FERRY_ID } });
}

export function stage1Request(
  id: string,
  origin: number,
  status: Request["status"] = "received",
): Request {
  return {
    id,
    origin,
    kind: "stall.reserve",
    resource: "stall:main",
    critical: false,
    createdAt: 500,
    deadline: 60000,
    status,
    expired: false,
    result: null,
  };
}

export function withFerry(snapshot: Snapshot, ferry: Ferry): Snapshot {
  return { ...snapshot, ferries: [ferry] };
}

export function withLetters(snapshot: Snapshot, letters: Letter[]): Snapshot {
  return { ...snapshot, letters };
}

export function withRequests(snapshot: Snapshot, requests: Request[]): Snapshot {
  return { ...snapshot, requests };
}

export function withPaused(snapshot: Snapshot, paused: boolean): Snapshot {
  return { ...snapshot, clock: { ...snapshot.clock, paused } };
}

export function withTime(snapshot: Snapshot, t: number): Snapshot {
  return { ...snapshot, clock: { ...snapshot.clock, t } };
}

export function withRole(snapshot: Snapshot, id: number, role: NodeSummary["role"]): Snapshot {
  return {
    ...snapshot,
    nodes: snapshot.nodes.map((n) => (n.id === id ? { ...n, role } : n)),
  };
}

// stage1World は Snapshot から World を作る（純粋な関数のテスト用）。
export function stage1World(snapshot: Snapshot = stage1Snapshot()): World {
  return worldFromSnapshot(snapshot, 0);
}
