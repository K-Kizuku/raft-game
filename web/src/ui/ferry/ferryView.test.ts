import { describe, expect, it } from "vitest";

import type { Snapshot } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import {
  autoloadBlocker,
  departBlocker,
  ferryDetail,
  ferryTabs,
  islandColor,
  islandName,
  viaIsland,
} from "./ferryView.ts";
import type { FerryBlocker, FerryDetail } from "./ferryView.ts";
import { FERRY_ISLAND_COLORS, makeFerrySnapshot, portLetter, withFerry1 } from "./testing/world.ts";

const NO_SELECTION: UiSelection = { islandId: null, ferryId: null };

function worldOf(snapshot: Snapshot = makeFerrySnapshot()): World {
  return worldFromSnapshot(snapshot, 0);
}

function detailOf(world: World, ferryId: string, simNow = 29000): FerryDetail {
  const detail = ferryDetail(world, ferryId, simNow);
  if (detail === null) throw new Error(`船 ${ferryId} が無い`);
  return detail;
}

function withBroken(world: World, ferryId: string, until: number): World {
  return applyEvents(world, [{ seq: 1, t: 29000, type: "ferry.broken", ferryId, until }]);
}

function deepFreeze<T>(value: T): T {
  if (typeof value === "object" && value !== null && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const v of Object.values(value)) deepFreeze(v);
  }
  return value;
}

describe("islandName・islandColor", () => {
  it("ステージの島は名前と色、無い島は「島 id」と灰色", () => {
    const world = worldOf();
    expect(islandName(world, 2)).toBe("二の島");
    expect(islandColor(world, 2)).toBe(FERRY_ISLAND_COLORS[2]);
    expect(islandName(world, 9)).toBe("島 9");
    expect(islandColor(world, 9)).toBe("#9CA3AF");
  });
});

describe("ferryTabs", () => {
  it("FV-1 ferryIds の順に、状態・場所・残り時間・積み荷", () => {
    const tabs = ferryTabs(worldOf(), NO_SELECTION, 29000);
    expect(tabs).toEqual([
      {
        id: "f1",
        name: "ごとう丸",
        selected: false,
        state: "docked",
        placeName: "一の島",
        etaMs: null,
        cargoCount: 0,
        capacity: 4,
        broken: false,
      },
      {
        id: "f2",
        name: "しまかぜ",
        selected: false,
        state: "sailing",
        placeName: "三の島",
        etaMs: 2000,
        cargoCount: 1,
        capacity: 4,
        broken: false,
      },
    ]);
  });

  it("FV-1 selection.ferryId の船だけ selected", () => {
    const tabs = ferryTabs(worldOf(), { islandId: null, ferryId: "f2" }, 29000);
    expect(tabs.map((t) => t.selected)).toEqual([false, true]);
  });

  it("FV-1 到着の時刻を過ぎたら etaMs は 0", () => {
    const tabs = ferryTabs(worldOf(), NO_SELECTION, 40000);
    expect(tabs[1]?.etaMs).toBe(0);
  });

  it.each<[number, boolean]>([
    [30000, true],
    [29000, false],
    [20000, false],
  ])("FV-1 brokenUntil %i・simNow 29000 → broken %s", (until, want) => {
    const tabs = ferryTabs(withBroken(worldOf(), "f1", until), NO_SELECTION, 29000);
    expect(tabs[0]?.broken).toBe(want);
  });
});

describe("ferryDetail", () => {
  it("FV-2 停泊中の船: 港の手紙の山・空きの積み荷・行き先", () => {
    const detail = detailOf(worldOf(), "f1");
    expect(detail.name).toBe("ごとう丸");
    expect(detail.state).toBe("docked");
    expect(detail.islandId).toBe(1);
    expect(detail.islandName).toBe("一の島");
    expect(detail.toName).toBeNull();
    expect(detail.etaMs).toBeNull();
    expect(detail.readyInMs).toBe(0);
    expect(detail.port.map((g) => ({ ...g, letters: g.letters.map((l) => l.id) }))).toEqual([
      { to: 2, toName: "二の島", toColor: FERRY_ISLAND_COLORS[2], letters: [2] },
      { to: 3, toName: "三の島", toColor: FERRY_ISLAND_COLORS[3], letters: [5, 9] },
    ]);
    expect(detail.cargo).toEqual([
      { kind: "empty" },
      { kind: "empty" },
      { kind: "empty" },
      { kind: "empty" },
    ]);
    expect(detail.destinations).toEqual([
      { islandId: 2, name: "二の島", routeId: "1-2", blocked: false, forecast: false },
      { islandId: 3, name: "三の島", routeId: "1-3", blocked: false, forecast: false },
    ]);
    // 船の上の手紙 7 は港に入らない。
    expect(detail.port.flatMap((g) => g.letters.map((l) => l.id))).not.toContain(7);
  });

  it("FV-2 readyAt 30500 なら readyInMs 1500", () => {
    const detail = detailOf(worldOf(withFerry1({ readyAt: 30500 })), "f1");
    expect(detail.readyInMs).toBe(1500);
  });

  it("FV-3 積み荷: 手紙・不明な手紙・空き", () => {
    const base = makeFerrySnapshot();
    const snapshot = withFerry1(
      { cargo: [2, 99] },
      {
        letters: base.letters.map((l) =>
          l.id === 2 ? { ...l, loc: { type: "ferry", ferryId: "f1" } } : l,
        ),
      },
    );
    const detail = detailOf(worldOf(snapshot), "f1");
    expect(detail.cargo.map((s) => s.kind)).toEqual(["letter", "unknown", "empty", "empty"]);
    const first = detail.cargo[0];
    expect(first?.kind === "letter" && first.letter.id).toBe(2);
    expect(first?.kind === "letter" && first.unloadable).toBe(true);
    expect(first?.kind === "letter" && first.toName).toBe("二の島");
    expect(first?.kind === "letter" && first.toColor).toBe(FERRY_ISLAND_COLORS[2]);
    expect(detail.cargo[1]).toEqual({ kind: "unknown", letterId: 99 });
    expect(detail.port.flatMap((g) => g.letters.map((l) => l.id))).toEqual([5, 9]);
  });

  it("FV-3 島 2 生まれの手紙を積んで島 1 にいる → unloadable: false", () => {
    const snapshot = withFerry1(
      { cargo: [40] },
      {
        letters: [portLetter(40, "heartbeat", 2, 3, { loc: { type: "ferry", ferryId: "f1" } })],
      },
    );
    const slot = detailOf(worldOf(snapshot), "f1").cargo[0];
    expect(slot?.kind === "letter" && slot.unloadable).toBe(false);
  });

  it("FV-3 cargo が capacity より多いと空きは 0", () => {
    const snapshot = withFerry1({ capacity: 1, cargo: [97, 98] });
    expect(detailOf(worldOf(snapshot), "f1").cargo.map((s) => s.kind)).toEqual([
      "unknown",
      "unknown",
    ]);
  });

  it("FV-3 航行中の船: 行き先・残り時間・港と行き先は空・降ろせない", () => {
    const detail = detailOf(worldOf(), "f2");
    expect(detail.state).toBe("sailing");
    expect(detail.toName).toBe("三の島");
    expect(detail.etaMs).toBe(2000);
    expect(detail.islandId).toBeNull();
    expect(detail.islandName).toBeNull();
    expect(detail.readyInMs).toBe(0);
    expect(detail.port).toEqual([]);
    expect(detail.destinations).toEqual([]);
    const slot = detail.cargo[0];
    expect(slot?.kind === "letter" && slot.letter.id).toBe(7);
    expect(slot?.kind === "letter" && slot.unloadable).toBe(false);
    expect(detail.cargo.length).toBe(4);
  });

  it("FV-3 無い船は null", () => {
    expect(ferryDetail(worldOf(), "nope", 29000)).toBeNull();
  });
});

describe("destinations", () => {
  function blockedSnapshot(until: number): Snapshot {
    return makeFerrySnapshot({
      routesState: [
        { id: "1-2", blockedUntil: null },
        { id: "1-3", blockedUntil: until },
        { id: "2-3", blockedUntil: null },
      ],
    });
  }

  function withForecast(world: World): World {
    return applyEvents(world, [
      { seq: 1, t: 20000, type: "forecast", event: "storm", at: 35000, routeIds: ["1-2"] },
    ]);
  }

  it.each<[string, () => World, number, Record<number, { blocked: boolean; forecast: boolean }>]>([
    [
      "嵐 40000・simNow 29000",
      () => worldOf(blockedSnapshot(40000)),
      29000,
      { 2: { blocked: false, forecast: false }, 3: { blocked: true, forecast: false } },
    ],
    [
      "嵐 40000・simNow 40000",
      () => worldOf(blockedSnapshot(40000)),
      40000,
      { 2: { blocked: false, forecast: false }, 3: { blocked: false, forecast: false } },
    ],
    [
      "予報 35000・simNow 29000",
      () => withForecast(worldOf()),
      29000,
      { 2: { blocked: false, forecast: true }, 3: { blocked: false, forecast: false } },
    ],
    [
      "予報 35000・simNow 35000",
      () => withForecast(worldOf()),
      35000,
      { 2: { blocked: false, forecast: false }, 3: { blocked: false, forecast: false } },
    ],
  ])("FV-4 %s", (_name, make, simNow, want) => {
    const detail = detailOf(make(), "f1", simNow);
    const got: Record<number, { blocked: boolean; forecast: boolean }> = {};
    for (const d of detail.destinations)
      got[d.islandId] = { blocked: d.blocked, forecast: d.forecast };
    expect(got).toEqual(want);
  });

  it("FV-4 stage.routes が空なら []", () => {
    const base = makeFerrySnapshot();
    const snapshot = makeFerrySnapshot({ stage: { ...base.stage, routes: [] } });
    expect(detailOf(worldOf(snapshot), "f1").destinations).toEqual([]);
  });

  it("FV-4 stage に無い島へ向かう航路は「島 9」、同じ島は最初の航路だけ", () => {
    const base = makeFerrySnapshot();
    const snapshot = makeFerrySnapshot({
      stage: {
        ...base.stage,
        routes: [
          { id: "1-9", a: 1, b: 9, travelMs: 6000, provenance: "derived" },
          { id: "2-1", a: 2, b: 1, travelMs: 6000, provenance: "derived" },
          { id: "1-2b", a: 1, b: 2, travelMs: 9000, provenance: "derived" },
        ],
      },
    });
    expect(detailOf(worldOf(snapshot), "f1").destinations).toEqual([
      { islandId: 9, name: "島 9", routeId: "1-9", blocked: false, forecast: false },
      { islandId: 2, name: "二の島", routeId: "2-1", blocked: false, forecast: false },
    ]);
  });
});

describe("autoloadBlocker・departBlocker", () => {
  const stormy = makeFerrySnapshot({
    routesState: [
      { id: "1-2", blockedUntil: 40000 },
      { id: "1-3", blockedUntil: null },
      { id: "2-3", blockedUntil: null },
    ],
  });

  it.each<[string, () => FerryDetail, number | null, FerryBlocker | null, FerryBlocker | null]>([
    ["行き先なし", () => detailOf(worldOf(), "f1"), null, "no_destination", "no_destination"],
    [
      "destinations に無い to",
      () => detailOf(worldOf(), "f1"),
      9,
      "no_destination",
      "no_destination",
    ],
    ["いる島そのもの", () => detailOf(worldOf(), "f1"), 1, "no_destination", "no_destination"],
    [
      "停泊の時間中",
      () => detailOf(worldOf(withFerry1({ readyAt: 30500 })), "f1"),
      2,
      null,
      "not_ready",
    ],
    ["嵐の行き先", () => detailOf(worldOf(stormy), "f1"), 2, null, "route_blocked"],
    [
      "停泊の時間中で嵐",
      () => detailOf(worldOf({ ...stormy, ferries: withFerry1({ readyAt: 30500 }).ferries }), "f1"),
      2,
      null,
      "not_ready",
    ],
    ["嵐でない行き先", () => detailOf(worldOf(stormy), "f1"), 3, null, null],
    ["航行中", () => detailOf(worldOf(), "f2"), 2, "sailing", "sailing"],
    ["航行中で行き先なし", () => detailOf(worldOf(), "f2"), null, "sailing", "sailing"],
    ["すべて満たす", () => detailOf(worldOf(), "f1"), 2, null, null],
  ])("FV-5 %s", (_name, make, to, wantAutoload, wantDepart) => {
    const detail = make();
    expect(autoloadBlocker(detail, to)).toBe(wantAutoload);
    expect(departBlocker(detail, to)).toBe(wantDepart);
  });
});

describe("経由の手紙（#318）", () => {
  function route(a: number, b: number, travelMs: number): Snapshot["stage"]["routes"][number] {
    return { id: `${String(a)}-${String(b)}`, a, b, travelMs, provenance: "derived" };
  }
  function withRoutes(
    routes: Snapshot["stage"]["routes"],
    overrides: Partial<Snapshot> = {},
  ): World {
    const base = makeFerrySnapshot(overrides);
    return worldOf({
      ...base,
      stage: { ...base.stage, routes },
      routesState: routes.map((r) => ({ id: r.id, blockedUntil: null })),
    });
  }

  it("FV-7 viaIsland: 隣でない島へは最短の経路の次の島。隣・同じ島・経路なしは null", () => {
    const line = withRoutes([route(1, 2, 3750), route(2, 3, 5250)]);
    expect(viaIsland(line, 1, 3)).toBe(2);
    expect(viaIsland(line, 3, 1)).toBe(2);
    expect(viaIsland(line, 1, 2)).toBeNull();
    expect(viaIsland(line, 2, 2)).toBeNull();
    expect(viaIsland(line, 1, 9)).toBeNull();
    // 三角形: 直接の航路があれば経由にしない（遠回りの方が速くても）。
    const triangle = withRoutes([route(1, 2, 1), route(2, 3, 1), route(1, 3, 100)]);
    expect(viaIsland(triangle, 1, 3)).toBeNull();
  });

  it("FV-7 viaIsland: 所要時間で決め、同じなら次の島の ID の小さい方（サーバーの NextHop と同じ）", () => {
    // 1-2-4 と 1-3-4 が同じ長さ（10）。1-5-4 は区間が同じでも遠い（21）。
    const islands = makeFerrySnapshot().stage.islands;
    const extra = [4, 5].map((id) => ({ ...islands[0], id, name: `${String(id)}の島` }));
    const world = withRoutes(
      [
        route(1, 3, 4),
        route(3, 4, 6),
        route(1, 2, 5),
        route(2, 4, 5),
        route(1, 5, 1),
        route(4, 5, 20),
      ],
      {
        stage: {
          ...makeFerrySnapshot().stage,
          islands: [...islands, ...extra],
        } as Snapshot["stage"],
      },
    );
    expect(viaIsland(world, 1, 4)).toBe(2);
    expect(viaIsland(world, 5, 3)).toBe(1);
  });

  it("FV-8 港の隣でない島宛ての山に案内、積み荷の経由の手紙に viaName", () => {
    const world = withRoutes([route(1, 2, 3750), route(2, 3, 5250)], {
      ferries: makeFerrySnapshot().ferries.map((f) =>
        f.id === "f1" ? { ...f, cargo: [30, 31] } : f,
      ),
      letters: [
        ...makeFerrySnapshot().letters,
        portLetter(30, "append", 1, 3, { loc: { type: "ferry", ferryId: "f1" } }),
        portLetter(31, "heartbeat", 1, 2, { loc: { type: "ferry", ferryId: "f1" } }),
      ],
    });
    const detail = detailOf(world, "f1");
    expect(detail.viaHints).toEqual([{ to: 3, toName: "三の島", via: 2, viaName: "二の島" }]);
    expect(detail.cargo.map((s) => (s.kind === "letter" ? s.viaName : s.kind))).toEqual([
      "二の島",
      null,
      "empty",
      "empty",
    ]);
    // 航行中の f2（2→3）の、3 宛ての手紙は経由でない。
    const f2 = detailOf(world, "f2");
    expect(f2.viaHints).toEqual([]);
    expect(f2.cargo[0]?.kind === "letter" && f2.cargo[0].viaName).toBeNull();
  });

  it("FV-8 三角形の航路では案内を出さない", () => {
    expect(detailOf(worldOf(), "f1").viaHints).toEqual([]);
  });
});

describe("純粋さ", () => {
  it("FV-6 凍らせた引数で例外が出ず、同じ引数なら同じ結果", () => {
    const world = deepFreeze(
      applyEvents(worldOf(), [
        { seq: 1, t: 20000, type: "forecast", event: "storm", at: 35000, routeIds: ["1-2"] },
      ]),
    );
    const selection = deepFreeze<UiSelection>({ islandId: null, ferryId: "f1" });
    for (const simNow of [29000, 36000]) {
      const tabs = ferryTabs(world, selection, simNow);
      expect(ferryTabs(world, selection, simNow)).toEqual(tabs);
      expect(ferryTabs(world, selection, simNow)).not.toBe(tabs);
      for (const id of ["f1", "f2"]) {
        const detail = ferryDetail(world, id, simNow);
        expect(ferryDetail(world, id, simNow)).toEqual(detail);
        if (detail === null) throw new Error("船が無い");
        expect(autoloadBlocker(detail, 2)).toBe(autoloadBlocker(detail, 2));
        expect(departBlocker(detail, 2)).toBe(departBlocker(detail, 2));
      }
    }
  });
});
