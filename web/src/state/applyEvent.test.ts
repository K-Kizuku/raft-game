import { describe, expect, it } from "vitest";

import type { Island, Letter, Loc, Request, ServerEvent, Snapshot } from "../protocol/index.ts";
import { applyEvent, applyEvents, withClock, worldFromSnapshot } from "./applyEvent.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import { WORLD_LIMITS } from "./world.ts";
import type { World } from "./world.ts";

const RECEIVED_AT = 500;
const SEQ = 101;

function island(id: number): Island {
  return {
    id,
    name: `島${id}`,
    municipality: "テスト町",
    layout: { x: id * 100, y: 0 },
    population: { value: 10, asOf: "令和8年4月1日", provenance: "creative", source: "test" },
    color: "#000000",
  };
}

function raftLetter(id: number, loc: Loc, to = 2): Letter {
  return {
    id,
    kind: "raft",
    from: 1,
    to,
    createdAt: 0,
    loc,
    label: "heartbeat",
    raft: {
      type: "MsgHeartbeat",
      term: 2,
      logTerm: 0,
      index: 0,
      commit: 0,
      entries: [],
      reject: false,
      rejectHint: 0,
      stale: false,
    },
  };
}

function requestLetter(id: number, loc: Loc): Letter {
  return {
    id,
    kind: "request",
    from: 2,
    to: 1,
    createdAt: 0,
    loc,
    label: "request",
    request: { reqId: "r1", hops: 0 },
  };
}

const REQUEST_R1: Request = {
  id: "r1",
  origin: 2,
  kind: "stall.reserve",
  resource: "stall:main",
  critical: false,
  createdAt: 0,
  deadline: 50000,
  status: "received",
  expired: false,
  result: null,
};

// 島 2 つ・船 2 隻・手紙 3 通（港 1・船 f1・港 2 の依頼書）の小さなスナップショット。
function smallSnapshot(): Snapshot {
  return {
    protocol: 1,
    session: { id: "s1", stageId: "small", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { t: 1000, speed: 1, paused: false, endsAt: 60000, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: {
      title: "小さな海",
      islands: [island(1), island(2)],
      routes: [{ id: "1-2", a: 1, b: 2, travelMs: 4000, provenance: "creative" }],
      resources: [{ id: "stall:main", kind: "stall", label: "屋台", critical: false }],
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
        readyAt: 900,
        cargo: [11],
      },
      {
        id: "f2",
        name: "二号",
        capacity: 4,
        state: "docked",
        islandId: 2,
        departAt: 0,
        arriveAt: 0,
        readyAt: 0,
        cargo: [],
      },
    ],
    routesState: [{ id: "1-2", blockedUntil: null }],
    letters: [
      raftLetter(10, { type: "port", islandId: 1 }),
      raftLetter(11, { type: "ferry", ferryId: "f1" }),
      requestLetter(12, { type: "port", islandId: 2 }),
    ],
    requests: [REQUEST_R1],
    nodes: [
      { id: 1, role: "leader", term: 2, vote: 1, lead: 1, commit: 1, applied: 1, lastIndex: 1 },
      { id: 2, role: "follower", term: 2, vote: 1, lead: 1, commit: 1, applied: 1, lastIndex: 1 },
    ],
    islandsState: [{ id: 1, satisfaction: 50, lastDeliveredAt: null }],
    score: { total: 0, successes: 0, target: 500, criticalSecured: [] },
    lastSeq: 100,
  };
}

function smallWorld(): World {
  return worldFromSnapshot(smallSnapshot(), RECEIVED_AT);
}

function deepFreeze<T>(value: T): T {
  if (typeof value === "object" && value !== null && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
  }
  return value;
}

// sameExcept は keys 以外の World のフィールドが前と同じオブジェクトであることを確かめる。
function expectSameExcept(before: World, after: World, keys: readonly (keyof World)[]): void {
  for (const key of Object.keys(before) as (keyof World)[]) {
    if (key === "lastEventSeq" || keys.includes(key)) continue;
    expect(after[key], `${key} は同じオブジェクト`).toBe(before[key]);
  }
}

describe("worldFromSnapshot", () => {
  it("WF-1 記録のスナップショットから World を作る", () => {
    const { snapshot } = loadMiniRound();
    const world = worldFromSnapshot(snapshot, 1234);
    expect(world.stage.islandIds).toEqual([1, 2, 3]);
    expect(world.stage.routeIds).toEqual(["1-2", "1-3", "2-3"]);
    expect(world.ferryIds).toEqual(["f1"]);
    expect(world.ferries.f1?.brokenUntil).toBeNull();
    expect(world.letters).toEqual({});
    expect(world.lastEventSeq).toBe(0);
    expect(world.clock).toEqual({
      simTime: 0,
      speed: 1,
      paused: false,
      endsAt: 60000,
      allowedSpeeds: [0.5, 1, 2, 4],
      receivedAt: 1234,
    });
    expect(world.forecasts).toEqual([]);
    expect(world.notices).toEqual([]);
    expect(world.letterExits).toEqual([]);
    expect(world.leaderChanges).toEqual([]);
    expect(world.phase).toBe("intro");
    expect(world.stage.title).toBe("テストの海");
    expect(world.nodes[1]?.role).toBe("follower");
  });

  it("AE-B1 スナップショットの brokenUntil（故障中だけ）をストアに写す", () => {
    const snapshot = smallSnapshot();
    const [f1, f2] = snapshot.ferries;
    if (f1 === undefined || f2 === undefined) throw new Error("船が 2 隻ない");
    snapshot.ferries = [{ ...f1, brokenUntil: 20000 }, f2];
    const world = worldFromSnapshot(snapshot, 0);
    expect(world.ferries.f1?.brokenUntil).toBe(20000);
    expect(world.ferries.f2?.brokenUntil).toBeNull();
  });

  it("AE-R1 snapshot.stage.relayCap を写し、無ければ項目を作らない", () => {
    const snapshot = smallSnapshot();
    expect("relayCap" in worldFromSnapshot(snapshot, 0).stage).toBe(false);
    snapshot.stage.relayCap = 12;
    expect(worldFromSnapshot(snapshot, 0).stage.relayCap).toBe(12);
  });

  it("WF-2 配達済み・破棄の手紙は入れず、同じ ID は後のものを使う", () => {
    const snapshot = smallSnapshot();
    const second = { ...island(1), name: "後の島" };
    snapshot.stage.islands = [island(1), island(2), second];
    snapshot.letters = [
      ...snapshot.letters,
      raftLetter(20, { type: "delivered", islandId: 2 }),
      raftLetter(21, { type: "dropped" }),
      raftLetter(22, { type: "relay", islandId: 2 }),
    ];
    const world = worldFromSnapshot(snapshot, 0);
    expect(world.letters[20]).toBeUndefined();
    expect(world.letters[21]).toBeUndefined();
    expect(world.letters[22]?.loc.type).toBe("relay");
    expect(Object.keys(world.letters).map(Number)).toEqual([10, 11, 12, 22]);
    expect(world.stage.islands[1]?.name).toBe("後の島");
    expect(world.stage.islandIds).toEqual([1, 2]);
  });
});

type Row = Readonly<{
  name: string;
  before?: () => World;
  event: ServerEvent;
  changed: readonly (keyof World)[];
  expect: (before: World, after: World) => void;
}>;

function departed(seq: number): ServerEvent {
  return {
    seq,
    t: 2000,
    type: "ferry.departed",
    ferryId: "f1",
    from: 1,
    to: 2,
    departAt: 2000,
    arriveAt: 6000,
  };
}

const rows: readonly Row[] = [
  {
    name: "phase.changed",
    event: { seq: SEQ, t: 0, type: "phase.changed", phase: "ended" },
    changed: ["phase"],
    expect: (_, w) => expect(w.phase).toBe("ended"),
  },
  {
    name: "letter.created（港）",
    event: {
      seq: SEQ,
      t: 0,
      type: "letter.created",
      letter: raftLetter(30, { type: "port", islandId: 2 }),
    },
    changed: ["letters"],
    expect: (_, w) => expect(w.letters[30]).toEqual(raftLetter(30, { type: "port", islandId: 2 })),
  },
  {
    name: "letter.created（船の上）",
    event: {
      seq: SEQ,
      t: 0,
      type: "letter.created",
      letter: raftLetter(30, { type: "ferry", ferryId: "f1" }),
    },
    changed: ["letters", "ferries"],
    expect: (_, w) => {
      expect(w.letters[30]?.loc).toEqual({ type: "ferry", ferryId: "f1" });
      expect(w.ferries.f1?.cargo).toEqual([11, 30]);
    },
  },
  {
    name: "letter.created（配達済みは入れない）",
    event: {
      seq: SEQ,
      t: 0,
      type: "letter.created",
      letter: raftLetter(30, { type: "delivered", islandId: 2 }),
    },
    changed: [],
    expect: (_, w) => expect(w.letters[30]).toBeUndefined(),
  },
  {
    name: "letter.created（同じ ID は置き換え）",
    event: {
      seq: SEQ,
      t: 0,
      type: "letter.created",
      letter: { ...raftLetter(10, { type: "port", islandId: 1 }), to: 9 },
    },
    changed: ["letters"],
    expect: (_, w) => expect(w.letters[10]?.to).toBe(9),
  },
  {
    name: "letter.moved（港 → 船）",
    event: {
      seq: SEQ,
      t: 0,
      type: "letter.moved",
      letterId: 10,
      loc: { type: "ferry", ferryId: "f1" },
    },
    changed: ["letters", "ferries"],
    expect: (_, w) => {
      expect(w.letters[10]?.loc).toEqual({ type: "ferry", ferryId: "f1" });
      expect(w.ferries.f1?.cargo).toEqual([11, 10]);
    },
  },
  {
    name: "letter.moved（配達済みへは消す）",
    event: { seq: SEQ, t: 0, type: "letter.moved", letterId: 11, loc: { type: "dropped" } },
    changed: ["letters", "ferries"],
    expect: (_, w) => {
      expect(w.letters[11]).toBeUndefined();
      expect(w.ferries.f1?.cargo).toEqual([]);
    },
  },
  {
    name: "letter.delivered",
    event: {
      seq: SEQ,
      t: 7000,
      type: "letter.delivered",
      letterId: 11,
      islandId: 2,
      accepted: true,
    },
    changed: ["letters", "ferries", "letterExits"],
    expect: (b, w) => {
      expect(w.letters[11]).toBeUndefined();
      expect(w.ferries.f1?.cargo).toEqual([]);
      expect(w.letterExits).toEqual([
        {
          seq: SEQ,
          t: 7000,
          outcome: "delivered",
          letter: b.letters[11],
          islandId: 2,
          accepted: true,
        },
      ]);
    },
  },
  {
    name: "letter.dropped",
    event: { seq: SEQ, t: 8000, type: "letter.dropped", letterId: 10, reason: "overflow" },
    changed: ["letters", "letterExits"],
    expect: (b, w) => {
      expect(w.letters[10]).toBeUndefined();
      expect(w.letterExits).toEqual([
        { seq: SEQ, t: 8000, outcome: "dropped", letter: b.letters[10], reason: "overflow" },
      ]);
    },
  },
  {
    name: "letter.stale",
    event: { seq: SEQ, t: 0, type: "letter.stale", letterId: 10 },
    changed: ["letters"],
    expect: (b, w) => {
      expect(w.letters[10]?.raft?.stale).toBe(true);
      expect(b.letters[10]?.raft?.stale).toBe(false);
      expect(w.letters[11]).toBe(b.letters[11]);
    },
  },
  {
    name: "ferry.departed",
    event: departed(SEQ),
    changed: ["ferries"],
    expect: (b, w) => {
      const f = w.ferries.f1;
      expect(f).toMatchObject({ state: "sailing", from: 1, to: 2, departAt: 2000, arriveAt: 6000 });
      expect(f && "islandId" in f).toBe(false);
      expect(f?.readyAt).toBe(900);
      expect(f?.cargo).toBe(b.ferries.f1?.cargo);
      expect(w.ferries.f2).toBe(b.ferries.f2);
    },
  },
  {
    name: "ferry.arrived",
    before: () => applyEvent(smallWorld(), departed(SEQ - 1)),
    event: { seq: SEQ, t: 6000, type: "ferry.arrived", ferryId: "f1", islandId: 2, readyAt: 7500 },
    changed: ["ferries"],
    expect: (_, w) => {
      const f = w.ferries.f1;
      expect(f).toMatchObject({
        state: "docked",
        islandId: 2,
        readyAt: 7500,
        departAt: 0,
        arriveAt: 0,
      });
      expect(f && ("from" in f || "to" in f)).toBe(false);
    },
  },
  {
    name: "ferry.broken",
    event: { seq: SEQ, t: 0, type: "ferry.broken", ferryId: "f1", until: 5000 },
    changed: ["ferries"],
    expect: (_, w) => expect(w.ferries.f1?.brokenUntil).toBe(5000),
  },
  {
    name: "ferry.repaired",
    before: () =>
      applyEvent(smallWorld(), {
        seq: SEQ - 1,
        t: 0,
        type: "ferry.broken",
        ferryId: "f1",
        until: 5000,
      }),
    event: { seq: SEQ, t: 5000, type: "ferry.repaired", ferryId: "f1", until: 5000 },
    changed: ["ferries"],
    expect: (_, w) => expect(w.ferries.f1?.brokenUntil).toBeNull(),
  },
  {
    name: "route.blocked",
    before: () =>
      applyEvent(smallWorld(), {
        seq: SEQ - 1,
        t: 0,
        type: "forecast",
        event: "storm",
        at: 3000,
        routeIds: ["1-2"],
      }),
    event: { seq: SEQ, t: 3000, type: "route.blocked", routeIds: ["1-2", "9-9"], until: 9000 },
    changed: ["routeStates", "forecasts"],
    expect: (_, w) => {
      expect(w.routeStates["1-2"]).toEqual({ id: "1-2", blockedUntil: 9000 });
      expect(w.routeStates["9-9"]).toEqual({ id: "9-9", blockedUntil: 9000 });
      expect(w.forecasts).toEqual([]);
    },
  },
  {
    name: "route.unblocked",
    event: { seq: SEQ, t: 9000, type: "route.unblocked", routeIds: ["1-2", "8-8"], until: 9000 },
    changed: ["routeStates"],
    expect: (_, w) => {
      expect(w.routeStates["1-2"]).toEqual({ id: "1-2", blockedUntil: null });
      expect(w.routeStates["8-8"]).toEqual({ id: "8-8", blockedUntil: null });
    },
  },
  {
    name: "forecast",
    event: { seq: SEQ, t: 100, type: "forecast", event: "storm", at: 3000, routeIds: ["1-2"] },
    changed: ["forecasts"],
    expect: (_, w) =>
      expect(w.forecasts).toEqual([
        { seq: SEQ, t: 100, event: "storm", at: 3000, routeIds: ["1-2"] },
      ]),
  },
  {
    name: "node.state",
    event: {
      seq: SEQ,
      t: 0,
      type: "node.state",
      id: 3,
      role: "candidate",
      term: 3,
      vote: 3,
      lead: 0,
      commit: 1,
      applied: 1,
      lastIndex: 2,
    },
    changed: ["nodes"],
    expect: (b, w) => {
      expect(w.nodes[3]).toEqual({
        id: 3,
        role: "candidate",
        term: 3,
        vote: 3,
        lead: 0,
        commit: 1,
        applied: 1,
        lastIndex: 2,
      });
      expect(w.nodes[1]).toBe(b.nodes[1]);
    },
  },
  {
    name: "node.leader_elected",
    event: { seq: SEQ, t: 10, type: "node.leader_elected", id: 2, term: 3 },
    changed: ["leaderChanges"],
    expect: (_, w) =>
      expect(w.leaderChanges).toEqual([{ seq: SEQ, t: 10, kind: "elected", id: 2, term: 3 }]),
  },
  {
    name: "node.stepped_down",
    event: { seq: SEQ, t: 10, type: "node.stepped_down", id: 1, term: 2, reason: "higher_term" },
    changed: ["leaderChanges"],
    expect: (_, w) =>
      expect(w.leaderChanges).toEqual([
        { seq: SEQ, t: 10, kind: "stepped_down", id: 1, term: 2, reason: "higher_term" },
      ]),
  },
  {
    name: "request.created（新しい ID）",
    event: { seq: SEQ, t: 0, type: "request.created", request: { ...REQUEST_R1, id: "r2" } },
    changed: ["requests", "requestIds"],
    expect: (_, w) => {
      expect(w.requests.r2?.id).toBe("r2");
      expect(w.requestIds).toEqual(["r1", "r2"]);
    },
  },
  {
    name: "request.created（同じ ID）",
    event: { seq: SEQ, t: 0, type: "request.created", request: { ...REQUEST_R1, deadline: 1 } },
    changed: ["requests"],
    expect: (b, w) => {
      expect(w.requests.r1?.deadline).toBe(1);
      expect(w.requestIds).toBe(b.requestIds);
    },
  },
  {
    name: "request.updated",
    event: {
      seq: SEQ,
      t: 0,
      type: "request.updated",
      id: "r1",
      status: "rejected",
      expired: true,
      result: "full",
    },
    changed: ["requests"],
    expect: (_, w) =>
      expect(w.requests.r1).toEqual({
        ...REQUEST_R1,
        status: "rejected",
        expired: true,
        result: "full",
      }),
  },
  {
    name: "ledger.applied",
    event: {
      seq: SEQ,
      t: 0,
      type: "ledger.applied",
      islandId: 1,
      index: 2,
      reqId: "r1",
      result: "ok",
    },
    changed: [],
    expect: () => {},
  },
  {
    name: "island.updated",
    event: {
      seq: SEQ,
      t: 0,
      type: "island.updated",
      id: 5,
      satisfaction: 80,
      lastDeliveredAt: 300,
    },
    changed: ["islandStates"],
    expect: (b, w) => {
      expect(w.islandStates[5]).toEqual({ id: 5, satisfaction: 80, lastDeliveredAt: 300 });
      expect(w.islandStates[1]).toBe(b.islandStates[1]);
    },
  },
  {
    name: "score.updated",
    event: {
      seq: SEQ,
      t: 0,
      type: "score.updated",
      total: 120,
      successes: 2,
      criticalSecured: ["stall:main"],
    },
    changed: ["score"],
    expect: (_, w) =>
      expect(w.score).toEqual({
        total: 120,
        successes: 2,
        target: 500,
        criticalSecured: ["stall:main"],
      }),
  },
  {
    name: "notice",
    event: {
      seq: SEQ,
      t: 40,
      type: "notice",
      islandId: 2,
      key: "request.succeeded",
      params: { reqId: "r1" },
    },
    changed: ["notices"],
    expect: (_, w) =>
      expect(w.notices).toEqual([
        { seq: SEQ, t: 40, islandId: 2, key: "request.succeeded", params: { reqId: "r1" } },
      ]),
  },
];

describe("applyEvent", () => {
  it.each(rows)("AE-1 $name", (row) => {
    const before = (row.before ?? smallWorld)();
    const after = applyEvent(before, row.event);
    expect(after.lastEventSeq).toBe(row.event.seq);
    row.expect(before, after);
    expectSameExcept(before, after, row.changed);
  });

  it("AE-1 表が 22 種類をすべて含む", () => {
    expect(new Set(rows.map((r) => r.event.type)).size).toBe(22);
  });

  it("AE-2 cargo を letter.* から保つ", () => {
    let w = smallWorld();
    // 港 → 船（末尾に足す）
    w = applyEvent(w, {
      seq: 101,
      t: 0,
      type: "letter.moved",
      letterId: 10,
      loc: { type: "ferry", ferryId: "f1" },
    });
    w = applyEvent(w, {
      seq: 102,
      t: 0,
      type: "letter.moved",
      letterId: 12,
      loc: { type: "ferry", ferryId: "f1" },
    });
    expect(w.ferries.f1?.cargo).toEqual([11, 10, 12]);
    // 船 → 港（外す。残りの順はそのまま）
    w = applyEvent(w, {
      seq: 103,
      t: 0,
      type: "letter.moved",
      letterId: 10,
      loc: { type: "port", islandId: 1 },
    });
    expect(w.ferries.f1?.cargo).toEqual([11, 12]);
    expect(w.letters[10]?.loc).toEqual({ type: "port", islandId: 1 });
    // 船 → 別の船
    w = applyEvent(w, {
      seq: 104,
      t: 0,
      type: "letter.moved",
      letterId: 11,
      loc: { type: "ferry", ferryId: "f2" },
    });
    expect(w.ferries.f1?.cargo).toEqual([12]);
    expect(w.ferries.f2?.cargo).toEqual([11]);
    // 配達・破棄で cargo から外れる
    w = applyEvent(w, {
      seq: 105,
      t: 0,
      type: "letter.delivered",
      letterId: 11,
      islandId: 1,
      accepted: false,
    });
    expect(w.ferries.f2?.cargo).toEqual([]);
    w = applyEvent(w, {
      seq: 106,
      t: 0,
      type: "letter.dropped",
      letterId: 12,
      reason: "superseded",
    });
    expect(w.ferries.f1?.cargo).toEqual([]);
    expect(Object.keys(w.letters).map(Number)).toEqual([10]);
    // cargo を持つ船が無い loc でも例外が出ない（手紙だけを変える）
    const ferries = w.ferries;
    w = applyEvent(w, {
      seq: 107,
      t: 0,
      type: "letter.moved",
      letterId: 10,
      loc: { type: "ferry", ferryId: "f9" },
    });
    expect(w.letters[10]?.loc).toEqual({ type: "ferry", ferryId: "f9" });
    expect(w.ferries).toBe(ferries);
    w = applyEvent(w, { seq: 108, t: 0, type: "letter.dropped", letterId: 10, reason: "overflow" });
    expect(w.letters[10]).toBeUndefined();
    expect(w.ferries).toBe(ferries);
  });

  it.each<[string, ServerEvent]>([
    [
      "無い手紙への letter.moved",
      { seq: SEQ, t: 0, type: "letter.moved", letterId: 99, loc: { type: "port", islandId: 1 } },
    ],
    [
      "無い手紙への letter.delivered",
      { seq: SEQ, t: 0, type: "letter.delivered", letterId: 99, islandId: 1, accepted: true },
    ],
    [
      "無い手紙への letter.dropped",
      { seq: SEQ, t: 0, type: "letter.dropped", letterId: 99, reason: "overflow" },
    ],
    ["無い手紙への letter.stale", { seq: SEQ, t: 0, type: "letter.stale", letterId: 99 }],
    ["依頼書への letter.stale", { seq: SEQ, t: 0, type: "letter.stale", letterId: 12 }],
    ["無い船への ferry.departed", { ...departed(SEQ), ferryId: "f9" } as ServerEvent],
    [
      "無い船への ferry.arrived",
      { seq: SEQ, t: 0, type: "ferry.arrived", ferryId: "f9", islandId: 1, readyAt: 0 },
    ],
    ["無い船への ferry.broken", { seq: SEQ, t: 0, type: "ferry.broken", ferryId: "f9", until: 1 }],
    [
      "無い船への ferry.repaired",
      { seq: SEQ, t: 0, type: "ferry.repaired", ferryId: "f9", until: 1 },
    ],
    [
      "無い依頼への request.updated",
      {
        seq: SEQ,
        t: 0,
        type: "request.updated",
        id: "r9",
        status: "succeeded",
        expired: false,
        result: null,
      },
    ],
    [
      "ledger.applied",
      {
        seq: SEQ,
        t: 0,
        type: "ledger.applied",
        islandId: 1,
        index: 1,
        reqId: "r1",
        result: "duplicate",
      },
    ],
    ["知らない type", { seq: SEQ, t: 0, type: "future.event" } as unknown as ServerEvent],
  ])("AE-3 %s は lastEventSeq だけを進める", (_, event) => {
    const before = deepFreeze(smallWorld());
    const after = applyEvent(before, event);
    expect(after).not.toBe(before);
    expect(after.lastEventSeq).toBe(SEQ);
    expectSameExcept(before, after, []);
  });

  it("AE-3 2 回目の letter.stale は無視する", () => {
    const once = applyEvent(smallWorld(), { seq: 101, t: 0, type: "letter.stale", letterId: 10 });
    const twice = applyEvent(once, { seq: 102, t: 0, type: "letter.stale", letterId: 10 });
    expect(twice.lastEventSeq).toBe(102);
    expectSameExcept(once, twice, []);
  });

  it.each([100, 50, 1])("AE-4 seq %i（<= lastEventSeq）は同じオブジェクトを返す", (seq) => {
    const before = smallWorld();
    const after = applyEvent(before, { seq, t: 0, type: "phase.changed", phase: "ended" });
    expect(after).toBe(before);
  });

  it.each(rows)("AE-5 凍らせた World とイベントで $name が例外を出さない", (row) => {
    const before = deepFreeze((row.before ?? smallWorld)());
    const event = deepFreeze(structuredClone(row.event));
    const snapshot = JSON.stringify(before);
    const after = applyEvent(before, event);
    expect(after.lastEventSeq).toBe(event.seq);
    expect(JSON.stringify(before)).toBe(snapshot);
  });

  it("AE-5 変わらなかった部分は同じオブジェクト", () => {
    const before = deepFreeze(smallWorld());
    const created = applyEvent(before, {
      seq: 101,
      t: 0,
      type: "letter.created",
      letter: raftLetter(30, { type: "port", islandId: 1 }),
    });
    expect(created.ferries).toBe(before.ferries);
    expect(created.nodes).toBe(before.nodes);
    expect(created.requests).toBe(before.requests);
    expect(created.stage).toBe(before.stage);
    expect(created.letters[10]).toBe(before.letters[10]);

    const nodeState = applyEvent(before, {
      seq: 101,
      t: 0,
      type: "node.state",
      id: 1,
      role: "follower",
      term: 3,
      vote: 0,
      lead: 0,
      commit: 1,
      applied: 1,
      lastIndex: 1,
    });
    expect(nodeState.letters).toBe(before.letters);
    expect(nodeState.nodes).not.toBe(before.nodes);
    expect(nodeState.nodes[2]).toBe(before.nodes[2]);
  });

  it("AE-6 一覧は WORLD_LIMITS で古いものから捨てる", () => {
    let w = smallWorld();
    const n = (k: number) => SEQ + k;
    const notices: ServerEvent[] = [];
    for (let k = 0; k < 51; k++) {
      notices.push({ seq: n(k), t: k, type: "notice", islandId: 1, key: `k${k}`, params: {} });
    }
    w = applyEvents(w, notices);
    expect(w.notices).toHaveLength(WORLD_LIMITS.notices);
    expect(w.notices[0]?.key).toBe("k1");

    const base = w.lastEventSeq;
    const many = (count: number, make: (seq: number, k: number) => ServerEvent) =>
      Array.from({ length: count }, (_, k) => make(base + 1 + k, k));

    let x = applyEvents(
      w,
      many(WORLD_LIMITS.forecasts + 1, (seq, k) => ({
        seq,
        t: 0,
        type: "forecast",
        event: "storm",
        at: 100000 + k,
        routeIds: [],
      })),
    );
    expect(x.forecasts).toHaveLength(WORLD_LIMITS.forecasts);
    expect(x.forecasts[0]?.at).toBe(100001);

    x = applyEvents(
      w,
      many(WORLD_LIMITS.leaderChanges + 1, (seq, k) => ({
        seq,
        t: 0,
        type: "node.leader_elected",
        id: 1,
        term: k,
      })),
    );
    expect(x.leaderChanges).toHaveLength(WORLD_LIMITS.leaderChanges);
    expect(x.leaderChanges[0]?.term).toBe(1);

    const created = many(WORLD_LIMITS.letterExits + 1, (seq, k) => ({
      seq,
      t: 0,
      type: "letter.created",
      letter: raftLetter(1000 + k, { type: "port", islandId: 1 }),
    }));
    x = applyEvents(w, created);
    const start = x.lastEventSeq;
    x = applyEvents(
      x,
      Array.from({ length: WORLD_LIMITS.letterExits + 1 }, (_, k) => ({
        seq: start + 1 + k,
        t: 0,
        type: "letter.dropped" as const,
        letterId: 1000 + k,
        reason: "overflow" as const,
      })),
    );
    expect(x.letterExits).toHaveLength(WORLD_LIMITS.letterExits);
    expect(x.letterExits[0]?.letter.id).toBe(1001);
  });

  it("AE-7 予報は at <= t の route.blocked で消える", () => {
    let w = applyEvent(smallWorld(), {
      seq: 101,
      t: 5000,
      type: "forecast",
      event: "storm",
      at: 10000,
      routeIds: ["1-2"],
    });
    w = applyEvent(w, { seq: 102, t: 9000, type: "route.blocked", routeIds: ["1-2"], until: 9500 });
    expect(w.forecasts).toHaveLength(1);
    const kept = w.forecasts;
    w = applyEvent(w, {
      seq: 103,
      t: 9500,
      type: "route.unblocked",
      routeIds: ["1-2"],
      until: 9500,
    });
    expect(w.forecasts).toBe(kept);
    w = applyEvent(w, {
      seq: 104,
      t: 10000,
      type: "route.blocked",
      routeIds: ["1-2"],
      until: 20000,
    });
    expect(w.forecasts).toEqual([]);
  });
});

describe("withClock", () => {
  it("AE-8 時計を置き換え、allowedSpeeds は前の値を保つ", () => {
    const before = smallWorld();
    const after = withClock(before, { t: 4200, speed: 2, paused: true, endsAt: 70000 }, 9999);
    expect(after.clock).toEqual({
      simTime: 4200,
      speed: 2,
      paused: true,
      endsAt: 70000,
      allowedSpeeds: [0.5, 1, 2, 4],
      receivedAt: 9999,
    });
    expect(after.clock.allowedSpeeds).toBe(before.clock.allowedSpeeds);
    expectSameExcept(before, after, ["clock"]);
    expect(after.lastEventSeq).toBe(before.lastEventSeq);
  });
});
