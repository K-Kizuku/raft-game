import { describe, expect, it } from "vitest";

import type { ServerEvent, Snapshot } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import {
  COLORS,
  dockedFerry,
  ferryLetter,
  makeSnapshot,
  node,
  portLetter,
  sailingFerry,
} from "../testing/snapshot.ts";
import { buildMapScene } from "./scene.ts";
import type { MapScene } from "./scene.ts";
import { MAP_STYLE } from "./style.ts";
import { fitIslands, toScreen } from "./viewport.ts";
import type { Point } from "./viewport.ts";

const SIZE = { width: 800, height: 600 };
const NONE: UiSelection = { islandId: null, ferryId: null };
const LAYOUT: Readonly<Record<number, Point>> = {
  1: { x: 0, y: 0 },
  2: { x: 400, y: 0 },
  3: { x: 200, y: 300 },
};
const TRANSFORM = fitIslands([LAYOUT[1], LAYOUT[2], LAYOUT[3]].filter(isPoint), SIZE);

function isPoint(p: Point | undefined): p is Point {
  return p !== undefined;
}

function screenOf(id: number): Point {
  const p = LAYOUT[id];
  if (p === undefined) throw new Error(`島 ${String(id)} の座標が無い`);
  return toScreen(TRANSFORM, p);
}

function world(overrides: Partial<Snapshot> = {}, events: readonly ServerEvent[] = []): World {
  return applyEvents(worldFromSnapshot(makeSnapshot(overrides), 0), events);
}

function scene(w: World, simNow = 0, selection: UiSelection = NONE): MapScene {
  return buildMapScene(w, selection, simNow, SIZE);
}

function expectPoint(actual: Point | undefined, expected: Point): void {
  expect(actual).toBeDefined();
  expect(actual?.x).toBeCloseTo(expected.x);
  expect(actual?.y).toBeCloseTo(expected.y);
}

function islandOf(s: MapScene, id: number) {
  const found = s.islands.find((i) => i.id === id);
  if (found === undefined) throw new Error(`島 ${String(id)} が無い`);
  return found;
}

function ferryOf(s: MapScene, id: string) {
  const found = s.ferries.find((f) => f.id === id);
  if (found === undefined) throw new Error(`船 ${id} が無い`);
  return found;
}

function routeOf(s: MapScene, id: string) {
  const found = s.routes.find((r) => r.id === id);
  if (found === undefined) throw new Error(`航路 ${id} が無い`);
  return found;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
  }
  return value;
}

describe("buildMapScene", () => {
  it("SC-1 島は stage.islandIds の順で、座標は toScreen(fitIslands(...))、色は島の色", () => {
    // stage に無い島（4）のノードは描かない。
    const s = scene(
      world({
        nodes: [
          node(1, "leader", 2),
          node(2, "follower", 2),
          node(3, "follower", 2),
          node(4, "leader", 2),
        ],
      }),
    );
    expect(s.transform).toEqual(TRANSFORM);
    expect(s.islands.map((i) => i.id)).toEqual([1, 2, 3]);
    for (const id of [1, 2, 3] as const) {
      const island = islandOf(s, id);
      expectPoint(island.at, screenOf(id));
      expect(island.color).toBe(COLORS[id]);
    }
    expect(islandOf(s, 1).name).toBe("一の島");
    expect(s.routes.map((r) => r.id)).toEqual(["1-2", "1-3", "2-3"]);
    const r12 = routeOf(s, "1-2");
    expectPoint(r12.a, screenOf(1));
    expectPoint(r12.b, screenOf(2));
    expectPoint(r12.mid, { x: (screenOf(1).x + screenOf(2).x) / 2, y: screenOf(1).y });
  });

  it("SC-1 両端の島が stage に無い航路は描かない・stage が空でも例外を出さない", () => {
    const base = makeSnapshot();
    const s = scene(
      world({
        stage: {
          ...base.stage,
          routes: [
            ...base.stage.routes,
            { id: "1-9", a: 1, b: 9, travelMs: 1, provenance: "creative" },
          ],
        },
      }),
    );
    expect(s.routes.map((r) => r.id)).toEqual(["1-2", "1-3", "2-3"]);

    const empty = scene(
      world({
        stage: { title: "空", islands: [], routes: [], resources: [] },
        ferries: [dockedFerry("f1", 1)],
        letters: [portLetter(1, 1, 2)],
      }),
    );
    expect(empty).toMatchObject({ routes: [], islands: [], piles: [], ferries: [] });
  });

  it.each([
    {
      name: "島 1 が leader・term 2 → leader",
      nodes: [node(1, "leader", 2), node(2, "follower", 2), node(3, "follower", 2)],
      want: { 1: "leader", 2: null, 3: null },
    },
    {
      name: "term 2 と term 3 の leader → 大きいほうが leader、小さいほうが leader_stale",
      nodes: [node(1, "leader", 2), node(2, "leader", 3), node(3, "follower", 3)],
      want: { 1: "leader_stale", 2: "leader", 3: null },
    },
    {
      name: "同じ term の leader が 2 つ → 両方 leader",
      nodes: [node(1, "leader", 3), node(2, "leader", 3), node(3, "follower", 3)],
      want: { 1: "leader", 2: "leader", 3: null },
    },
    {
      name: "candidate・pre_candidate・ノードなし",
      nodes: [node(1, "candidate", 4), node(2, "pre_candidate", 3)],
      want: { 1: "candidate", 2: "pre_candidate", 3: null },
    },
  ])("SC-2 $name", ({ nodes, want }) => {
    const s = scene(world({ nodes }));
    expect(Object.fromEntries(s.islands.map((i) => [i.id, i.roleMark]))).toEqual(want);
  });

  it("SC-3 航行中の船は from と to の島を結ぶ線の上を補間し、到着の時刻を過ぎたら行き先で止まる", () => {
    const w = world({ ferries: [sailingFerry("f1", 1, 2, 3000, 7000)] });
    const a = screenOf(1);
    const b = screenOf(2);

    const mid = ferryOf(scene(w, 5000), "f1");
    expectPoint(mid.at, { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
    expect(mid.heading).toBeCloseTo(Math.atan2(b.y - a.y, b.x - a.x));
    expect(mid.etaMs).toBe(2000);
    expect(mid.state).toBe("sailing");

    const late = ferryOf(scene(w, 8000), "f1");
    expectPoint(late.at, b);
    expect(late.etaMs).toBe(0);

    // 斜めの航路でも角度は from → to。
    const diag = ferryOf(scene(world({ ferries: [sailingFerry("f2", 3, 1, 0, 1000)] }), 0), "f2");
    const c = screenOf(3);
    expect(diag.heading).toBeCloseTo(Math.atan2(a.y - c.y, a.x - c.x));
    expectPoint(diag.at, c);
  });

  it("SC-4 同じ島に停泊する船を dockStep ずつずらし、島が分からない船は描かない", () => {
    const w = world({
      ferries: [
        dockedFerry("f1", 1, [7, 8]),
        dockedFerry("f9", 9),
        dockedFerry("f2", 1),
        dockedFerry("f3", 2),
      ],
    });
    const s = scene(w);
    expect(s.ferries.map((f) => f.id)).toEqual(["f1", "f2", "f3"]);
    const c1 = screenOf(1);
    const first = ferryOf(s, "f1");
    expectPoint(first.at, {
      x: c1.x + MAP_STYLE.islandRadius + MAP_STYLE.dockOffset,
      y: c1.y - MAP_STYLE.islandRadius,
    });
    expect(first).toMatchObject({
      heading: 0,
      etaMs: null,
      state: "docked",
      cargoCount: 2,
      capacity: 4,
      name: "f1丸",
    });
    expectPoint(ferryOf(s, "f2").at, {
      x: c1.x + MAP_STYLE.islandRadius + MAP_STYLE.dockOffset + MAP_STYLE.dockStep,
      y: c1.y - MAP_STYLE.islandRadius,
    });
    const c2 = screenOf(2);
    expectPoint(ferryOf(s, "f3").at, {
      x: c2.x + MAP_STYLE.islandRadius + MAP_STYLE.dockOffset,
      y: c2.y - MAP_STYLE.islandRadius,
    });
  });

  it.each([
    { simNow: 15000, broken: true },
    { simNow: 16000, broken: false },
    { simNow: 17000, broken: false },
  ])("SC-4 brokenUntil 16000・simNow $simNow → broken $broken", ({ simNow, broken }) => {
    const w = world({ ferries: [dockedFerry("f1", 1)] }, [
      { seq: 1, t: 14000, type: "ferry.broken", ferryId: "f1", until: 16000 },
    ]);
    expect(ferryOf(scene(w, simNow), "f1").broken).toBe(broken);
  });

  it.each([
    {
      name: "止まっている（終わり前）",
      blockedUntil: 20000,
      forecastAt: null,
      simNow: 15000,
      want: "blocked",
    },
    {
      name: "止まっていた（終わりの時刻）",
      blockedUntil: 20000,
      forecastAt: null,
      simNow: 20000,
      want: null,
    },
    { name: "予報（前）", blockedUntil: null, forecastAt: 10000, simNow: 5000, want: "forecast" },
    {
      name: "予報（時刻を過ぎた）",
      blockedUntil: null,
      forecastAt: 10000,
      simNow: 10000,
      want: null,
    },
    {
      name: "止まっていて予報もある",
      blockedUntil: 20000,
      forecastAt: 18000,
      simNow: 15000,
      want: "blocked",
    },
    { name: "何もない", blockedUntil: null, forecastAt: null, simNow: 0, want: null },
  ])("SC-5 $name → $want", ({ blockedUntil, forecastAt, simNow, want }) => {
    const events: ServerEvent[] = [];
    if (forecastAt !== null) {
      events.push({
        seq: 1,
        t: 0,
        type: "forecast",
        event: "storm",
        at: forecastAt,
        routeIds: ["2-3"],
      });
    }
    const w = world(
      {
        routesState: [
          { id: "1-2", blockedUntil: null },
          { id: "1-3", blockedUntil: null },
          { id: "2-3", blockedUntil },
        ],
      },
      events,
    );
    const s = scene(w, simNow);
    expect(routeOf(s, "2-3").storm).toBe(want);
    expect(routeOf(s, "1-2").storm).toBeNull();
    expect(routeOf(s, "1-3").storm).toBeNull();
  });

  it("SC-6 港の手紙を島ごと・宛先ごとの山にし、船の上の手紙は数えない", () => {
    const w = world({
      ferries: [dockedFerry("f1", 1, [20])],
      letters: [
        portLetter(5, 1, 3),
        portLetter(2, 1, 2),
        portLetter(9, 1, 3, "request"),
        ferryLetter(20, "f1", 2),
      ],
    });
    const s = scene(w);
    expect(
      s.piles.map(({ islandId, to, count, hasRequest, color }) => ({
        islandId,
        to,
        count,
        hasRequest,
        color,
      })),
    ).toEqual([
      { islandId: 1, to: 2, count: 1, hasRequest: false, color: COLORS[2] },
      { islandId: 1, to: 3, count: 2, hasRequest: true, color: COLORS[3] },
    ]);
    const c1 = screenOf(1);
    for (const pile of s.piles) {
      const dest = screenOf(pile.to);
      const d = Math.hypot(dest.x - c1.x, dest.y - c1.y);
      expectPoint(pile.at, {
        x: c1.x + ((dest.x - c1.x) / d) * MAP_STYLE.pileOffset,
        y: c1.y + ((dest.y - c1.y) / d) * MAP_STYLE.pileOffset,
      });
    }
  });

  it("SC-6 宛先の島が stage に無い山は島の真下に並べ、色は unknownIsland", () => {
    const w = world({ letters: [portLetter(1, 2, 8), portLetter(2, 2, 9), portLetter(3, 2, 9)] });
    const s = scene(w);
    const c2 = screenOf(2);
    expect(s.piles).toHaveLength(2);
    const [first, second] = s.piles;
    expect(first).toMatchObject({
      islandId: 2,
      to: 8,
      count: 1,
      color: MAP_STYLE.colors.unknownIsland,
    });
    expect(second).toMatchObject({
      islandId: 2,
      to: 9,
      count: 2,
      color: MAP_STYLE.colors.unknownIsland,
    });
    expectPoint(first?.at, { x: c2.x, y: c2.y + MAP_STYLE.pileOffset });
    expectPoint(second?.at, { x: c2.x, y: c2.y + MAP_STYLE.pileOffset + MAP_STYLE.pileStackStep });
  });

  it("SC-7 選択と行き先の候補", () => {
    const ferries = [dockedFerry("f1", 1), sailingFerry("f2", 3, 2, 0, 9000)];
    const w = world({ ferries });
    const candidates = (s: MapScene) => s.islands.filter((i) => i.candidate).map((i) => i.id);

    const byIsland = scene(w, 0, { islandId: 2, ferryId: null });
    expect(byIsland.islands.map((i) => i.selected)).toEqual([false, true, false]);
    expect(candidates(byIsland)).toEqual([]);
    expect(byIsland.ferries.map((f) => f.selected)).toEqual([false, false]);

    const docked = scene(w, 0, { islandId: null, ferryId: "f1" });
    expect(candidates(docked)).toEqual([2, 3]);
    expect(docked.ferries.map((f) => f.selected)).toEqual([true, false]);
    expect(docked.islands.every((i) => !i.selected)).toBe(true);

    const stormy = world({
      ferries,
      routesState: [
        { id: "1-2", blockedUntil: null },
        { id: "1-3", blockedUntil: 50000 },
        { id: "2-3", blockedUntil: null },
      ],
    });
    expect(candidates(scene(stormy, 0, { islandId: null, ferryId: "f1" }))).toEqual([2]);
    // 嵐が終わった時刻なら、また候補になる。
    expect(candidates(scene(stormy, 50000, { islandId: null, ferryId: "f1" }))).toEqual([2, 3]);

    const sailing = scene(w, 0, { islandId: 1, ferryId: "f2" });
    expect(candidates(sailing)).toEqual([2]);
    expect(sailing.ferries.map((f) => f.selected)).toEqual([false, true]);
    expect(sailing.islands.map((i) => i.selected)).toEqual([true, false, false]);

    expect(candidates(scene(w, 0, NONE))).toEqual([]);
    expect(candidates(scene(w, 0, { islandId: null, ferryId: "nope" }))).toEqual([]);
  });

  it("SC-8 記録の通し（loadMiniRound）", () => {
    const { snapshot, events } = loadMiniRound();
    const base = worldFromSnapshot(snapshot, 0);
    const upTo = (seq: number) =>
      applyEvents(
        base,
        events.filter((e) => e.seq <= seq),
      );
    const stage = snapshot.stage;
    const layout = (id: number) => {
      const island = stage.islands.find((i) => i.id === id);
      if (island === undefined) throw new Error(`島 ${String(id)} が無い`);
      return island.layout;
    };
    const t = fitIslands(
      stage.islands.map((i) => i.layout),
      SIZE,
    );
    const p1 = toScreen(t, layout(1));
    const p2 = toScreen(t, layout(2));

    const s11 = scene(upTo(11), 5000);
    expectPoint(ferryOf(s11, "f1").at, { x: (p1.x + p2.x) / 2, y: (p1.y + p2.y) / 2 });
    expect(islandOf(s11, 1).roleMark).toBe("leader");
    // 予報（seq 12）の前。
    expect(routeOf(s11, "2-3").storm).toBeNull();

    const s12 = scene(upTo(12), 6000);
    expect(routeOf(s12, "2-3").storm).toBe("forecast");

    const s17 = scene(upTo(17), 15000);
    expect(routeOf(s17, "2-3").storm).toBe("blocked");

    const s30 = scene(upTo(30), 60000);
    expect(islandOf(s30, 1).roleMark).toBeNull();
    const f1 = ferryOf(s30, "f1");
    expect(f1.state).toBe("docked");
    expectPoint(f1.at, {
      x: p2.x + MAP_STYLE.islandRadius + MAP_STYLE.dockOffset,
      y: p2.y - MAP_STYLE.islandRadius,
    });
    expect(routeOf(s30, "2-3").storm).toBeNull();
  });

  it("SC-9 引数を書き換えず、同じ引数なら同じ結果", () => {
    const w = deepFreeze(
      world(
        {
          ferries: [dockedFerry("f1", 1, [20]), sailingFerry("f2", 2, 3, 0, 4000)],
          letters: [portLetter(1, 1, 2), portLetter(2, 1, 9, "request"), ferryLetter(20, "f1", 3)],
          routesState: [
            { id: "1-2", blockedUntil: 9000 },
            { id: "1-3", blockedUntil: null },
            { id: "2-3", blockedUntil: null },
          ],
        },
        [{ seq: 1, t: 0, type: "forecast", event: "storm", at: 5000, routeIds: ["1-3"] }],
      ),
    );
    const selection = deepFreeze<UiSelection>({ islandId: 1, ferryId: "f1" });
    const size = deepFreeze({ ...SIZE });
    const first = buildMapScene(w, selection, 1000, size);
    const second = buildMapScene(w, selection, 1000, size);
    expect(second).toEqual(first);
  });

  describe("分断（T-704）", () => {
    // 1-3 と 2-3 が 20000 まで止まる → 三の島が孤立する。
    const split = (): World =>
      world({
        routesState: [
          { id: "1-2", blockedUntil: null },
          { id: "1-3", blockedUntil: 20000 },
          { id: "2-3", blockedUntil: 20000 },
        ],
      });

    it("SC-P1 嵐が無い・止まった航路があっても迂回できる → partition: null", () => {
      expect(scene(world()).partition).toBeNull();
      const detour = world({
        routesState: [
          { id: "1-2", blockedUntil: 20000 },
          { id: "1-3", blockedUntil: null },
          { id: "2-3", blockedUntil: null },
        ],
      });
      expect(scene(detour, 1000).partition).toBeNull();
    });

    it("SC-P2 2 本の航路が止まって島が分かれる → グループ 2 つ・縁取りの位置と記号の位置", () => {
      const p = scene(split(), 15000).partition;
      expect(p).not.toBeNull();
      expect(p?.total).toBe(3);
      expect(p?.groups).toEqual([
        { label: "A", islandIds: [1, 2], size: 2, majority: true },
        { label: "B", islandIds: [3], size: 1, majority: false },
      ]);
      expect(p?.halos.map((h) => [h.label, h.majority])).toEqual([
        ["A", true],
        ["B", false],
      ]);
      const [a, b] = p?.halos ?? [];
      expect(a?.islands).toHaveLength(2);
      expectPoint(a?.islands[0], screenOf(1));
      expectPoint(a?.islands[1], screenOf(2));
      expect(b?.islands).toHaveLength(1);
      expectPoint(b?.islands[0], screenOf(3));
      // 一の島と二の島は同じ y → x が小さい一の島の真上。
      expectPoint(a?.badgeAt, {
        x: screenOf(1).x,
        y: screenOf(1).y - MAP_STYLE.islandRadius * 2,
      });
      expectPoint(b?.badgeAt, {
        x: screenOf(3).x,
        y: screenOf(3).y - MAP_STYLE.islandRadius * 2,
      });
    });

    it("SC-P3 推定時刻が blockedUntil を過ぎる → partition: null", () => {
      expect(scene(split(), 19999).partition).not.toBeNull();
      expect(scene(split(), 20000).partition).toBeNull();
      expect(scene(split(), 25000).partition).toBeNull();
    });
  });
});
