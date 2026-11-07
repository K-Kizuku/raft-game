// テストのための Snapshot（T-405）。ferry のテストだけが import する（本番のコードから import しない）。
import type { Island, Letter, Route, Snapshot } from "../../../protocol/index.ts";

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

export const FERRY_ISLAND_COLORS: Readonly<Record<number, string>> = {
  1: "#E07A5F",
  2: "#3D405B",
  3: "#81B29A",
};

// 港にある手紙（from の港）。
export function portLetter(
  id: number,
  label: Letter["label"],
  from: number,
  to: number,
  overrides: Partial<Letter> = {},
): Letter {
  return {
    id,
    kind: label === "request" ? "request" : "raft",
    from,
    to,
    createdAt: 1000 + id,
    loc: { type: "port", islandId: from },
    label,
    ...overrides,
  };
}

// 船の上の手紙。
export function ferryLetter(
  id: number,
  label: Letter["label"],
  from: number,
  to: number,
  ferryId: string,
): Letter {
  return portLetter(id, label, from, to, { loc: { type: "ferry", ferryId } });
}

export function makeFerrySnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  return {
    protocol: 1,
    session: { id: "s-ferry", stageId: "stage-test", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { t: 29000, speed: 1, paused: false, endsAt: 180000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: {
      title: "テストの海",
      islands: [
        island(1, "一の島", FERRY_ISLAND_COLORS[1] ?? "", 100, 100),
        island(2, "二の島", FERRY_ISLAND_COLORS[2] ?? "", 300, 100),
        island(3, "三の島", FERRY_ISLAND_COLORS[3] ?? "", 200, 300),
      ],
      routes: [route(1, 2), route(1, 3), route(2, 3)],
      resources: [],
    },
    ferries: [
      {
        id: "f1",
        name: "ごとう丸",
        capacity: 4,
        state: "docked",
        islandId: 1,
        departAt: 0,
        arriveAt: 0,
        readyAt: 0,
        cargo: [],
      },
      {
        id: "f2",
        name: "しまかぜ",
        capacity: 4,
        state: "sailing",
        from: 2,
        to: 3,
        departAt: 25000,
        arriveAt: 31000,
        readyAt: 32500,
        cargo: [7],
      },
    ],
    routesState: [
      { id: "1-2", blockedUntil: null },
      { id: "1-3", blockedUntil: null },
      { id: "2-3", blockedUntil: null },
    ],
    letters: [
      portLetter(2, "heartbeat", 1, 2),
      portLetter(5, "append", 1, 3),
      portLetter(9, "request", 1, 3),
      ferryLetter(7, "append_resp", 2, 3, "f2"),
    ],
    requests: [],
    nodes: [
      { id: 1, role: "leader", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
      { id: 2, role: "follower", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
      { id: 3, role: "follower", term: 2, lead: 1, commit: 3, applied: 3, lastIndex: 3 },
    ],
    islandsState: [
      { id: 1, satisfaction: 60, lastDeliveredAt: null },
      { id: 2, satisfaction: 60, lastDeliveredAt: null },
      { id: 3, satisfaction: 60, lastDeliveredAt: null },
    ],
    score: { total: 0, successes: 0, target: 8, criticalSecured: [] },
    lastSeq: 0,
    ...overrides,
  };
}

// f1 を差し替えた Snapshot（f2 はそのまま）。
export function withFerry1(
  patch: Partial<Snapshot["ferries"][number]>,
  overrides: Partial<Snapshot> = {},
): Snapshot {
  const base = makeFerrySnapshot(overrides);
  return {
    ...base,
    ferries: base.ferries.map((f) => (f.id === "f1" ? { ...f, ...patch } : f)),
  };
}
