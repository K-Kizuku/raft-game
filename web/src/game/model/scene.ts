// 世界の状態とゲーム内時刻から、マップに描くもの（MapScene）を作る純粋な関数（T-404 / DesignDoc §13.3）。
// ストアに届いた値（node.state・route.*・ferry.*）を描くだけで、Raft の結果をここで決めない。
// 引数を書き換えず、時刻は引数の simNow だけを使う。
import { portPiles } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { ferryProgress, lerp } from "./ferry.ts";
import { partitionOf } from "./partition.ts";
import type { PartitionGroup } from "./partition.ts";
import { MAP_STYLE } from "./style.ts";
import { layoutMap, toScreen } from "./viewport.ts";
import type { MapTransform, Point, Size } from "./viewport.ts";

export type RoleMark = "leader" | "leader_stale" | "candidate" | "pre_candidate" | null;
export type IslandNode = Readonly<{
  id: number;
  name: string;
  color: string;
  at: Point;
  roleMark: RoleMark;
  selected: boolean;
  candidate: boolean; // 選んだ船の行き先の候補
}>;
export type RouteLine = Readonly<{
  id: string;
  a: Point;
  b: Point;
  mid: Point;
  storm: "blocked" | "forecast" | null;
}>;
export type LetterPile = Readonly<{
  islandId: number;
  to: number;
  at: Point;
  count: number;
  color: string;
  hasRequest: boolean;
}>;
export type FerrySprite = Readonly<{
  id: string;
  name: string;
  at: Point;
  heading: number; // ラジアン（停泊中は 0）
  state: "docked" | "sailing";
  cargoCount: number;
  capacity: number;
  etaMs: number | null;
  broken: boolean;
  selected: boolean;
}>;
// 分断のグループの縁取り（T-704・FR-ENG-08）。islands はグループの島の中心、badgeAt は記号（A・B…）の位置。
export type PartitionHalo = Readonly<{
  label: string;
  majority: boolean;
  islands: readonly Point[];
  badgeAt: Point;
}>;
export type MapScene = Readonly<{
  size: Size;
  transform: MapTransform;
  // 島・船・封筒・文字の絵の拡大率（T-426。viewport.ts の layoutMap）。MAP_STYLE の px に掛けて描く。
  // buildMapScene はいつも入れる。省略された MapScene（テストで手で作ったもの）は 1 として扱う。
  markScale?: number;
  routes: readonly RouteLine[];
  islands: readonly IslandNode[];
  piles: readonly LetterPile[];
  ferries: readonly FerrySprite[];
  // 島が分かれていないときは null。航路のつながりを示すだけで、代表・確定を決めない。
  partition: Readonly<{
    total: number;
    groups: readonly PartitionGroup[];
    halos: readonly PartitionHalo[];
  }> | null;
}>;

// roleMarkOf は島の代表のマーク。leader のうち term が leader の中で最大のものを "leader"、
// それより小さいものを "leader_stale" にする（届いた値を見せるだけで、どちらが正しいかを決めない）。
export function roleMarkOf(nodes: World["nodes"], islandId: number): RoleMark {
  const node = nodes[islandId];
  if (node === undefined) return null;
  switch (node.role) {
    case "leader": {
      let maxTerm = node.term;
      for (const other of Object.values(nodes)) {
        if (other.role === "leader" && other.term > maxTerm) maxTerm = other.term;
      }
      return node.term < maxTerm ? "leader_stale" : "leader";
    }
    case "candidate":
      return "candidate";
    case "pre_candidate":
      return "pre_candidate";
    default:
      return null;
  }
}

// 航路の嵐の状態。分断の凡例（PartitionLegend）も同じ規則で通れる航路を求めるので export する。
export function stormOf(world: World, routeId: string, simNow: number): RouteLine["storm"] {
  const blockedUntil = world.routeStates[routeId]?.blockedUntil ?? null;
  if (blockedUntil !== null && blockedUntil > simNow) return "blocked";
  if (world.forecasts.some((f) => f.at > simNow && f.routeIds.includes(routeId))) {
    return "forecast";
  }
  return null;
}

export function buildMapScene(
  world: World,
  selection: UiSelection,
  simNow: number,
  size: Size,
): MapScene {
  const { stage } = world;
  const present = stage.islandIds.filter((id) => stage.islands[id] !== undefined);
  const { transform, markScale: k } = layoutMap(
    present.map((id) => stage.islands[id]?.layout ?? { x: 0, y: 0 }),
    size,
  );
  // 絵の拡大率を掛けた、島の周りに物を置く距離（px）。
  const pileOffset = MAP_STYLE.pileOffset * k;
  const pileStackStep = MAP_STYLE.pileStackStep * k;
  const islandRadius = MAP_STYLE.islandRadius * k;
  const dockOffset = MAP_STYLE.dockOffset * k;
  const dockStep = MAP_STYLE.dockStep * k;
  const centers = new Map<number, Point>();
  for (const id of present) {
    const island = stage.islands[id];
    if (island !== undefined) centers.set(id, toScreen(transform, island.layout));
  }

  // 航路（両端の島が stage にあるものだけ）。
  const routes: RouteLine[] = [];
  const routeEnds: { a: number; b: number; storm: RouteLine["storm"] }[] = [];
  for (const id of stage.routeIds) {
    const route = stage.routes[id];
    if (route === undefined) continue;
    const a = centers.get(route.a);
    const b = centers.get(route.b);
    if (a === undefined || b === undefined) continue;
    const storm = stormOf(world, id, simNow);
    routes.push({ id, a, b, mid: lerp(a, b, 0.5), storm });
    routeEnds.push({ a: route.a, b: route.b, storm });
  }

  // 行き先の候補（表示だけ。出航できるかの判定はサーバーと T-405 に任せる）。
  const candidates = new Set<number>();
  const chosen = selection.ferryId === null ? undefined : world.ferries[selection.ferryId];
  if (chosen !== undefined) {
    if (chosen.state === "docked" && chosen.islandId !== undefined) {
      const here = chosen.islandId;
      for (const r of routeEnds) {
        if (r.storm === "blocked") continue;
        if (r.a === here) candidates.add(r.b);
        else if (r.b === here) candidates.add(r.a);
      }
    } else if (chosen.state === "sailing" && chosen.to !== undefined) {
      candidates.add(chosen.to);
    }
  }

  const islands: IslandNode[] = [];
  for (const id of present) {
    const island = stage.islands[id];
    const at = centers.get(id);
    if (island === undefined || at === undefined) continue;
    islands.push({
      id,
      name: island.name,
      color: island.color,
      at,
      roleMark: roleMarkOf(world.nodes, id),
      selected: selection.islandId === id,
      candidate: candidates.has(id),
    });
  }

  // 手紙の山（港の手紙だけ。宛先の島の方向に置く）。
  const piles: LetterPile[] = [];
  for (const id of present) {
    const center = centers.get(id);
    if (center === undefined) continue;
    let stacked = 0;
    for (const pile of portPiles(world, id)) {
      const dest = centers.get(pile.to);
      const destIsland = stage.islands[pile.to];
      const dx = dest === undefined ? 0 : dest.x - center.x;
      const dy = dest === undefined ? 0 : dest.y - center.y;
      const dist = Math.hypot(dx, dy);
      let at: Point;
      if (dest !== undefined && dist > 0) {
        at = {
          x: center.x + (dx / dist) * pileOffset,
          y: center.y + (dy / dist) * pileOffset,
        };
      } else {
        at = {
          x: center.x,
          y: center.y + pileOffset + stacked * pileStackStep,
        };
        stacked++;
      }
      piles.push({
        islandId: id,
        to: pile.to,
        at,
        count: pile.letters.length,
        color:
          dest !== undefined && destIsland !== undefined
            ? destIsland.color
            : MAP_STYLE.colors.unknownIsland,
        hasRequest: pile.letters.some((l) => l.kind === "request"),
      });
    }
  }

  // 船（島が分からない船は描かない）。
  const ferries: FerrySprite[] = [];
  const docked = new Map<number, number>();
  for (const id of world.ferryIds) {
    const ferry = world.ferries[id];
    if (ferry === undefined) continue;
    const common = {
      id,
      name: ferry.name,
      cargoCount: ferry.cargo.length,
      capacity: ferry.capacity,
      broken: ferry.brokenUntil !== null && ferry.brokenUntil > simNow,
      selected: selection.ferryId === id,
    };
    if (ferry.state === "sailing") {
      const from = ferry.from === undefined ? undefined : centers.get(ferry.from);
      const to = ferry.to === undefined ? undefined : centers.get(ferry.to);
      if (from === undefined || to === undefined) continue;
      ferries.push({
        ...common,
        state: "sailing",
        at: lerp(from, to, ferryProgress(ferry, simNow)),
        heading: Math.atan2(to.y - from.y, to.x - from.x),
        etaMs: Math.max(0, ferry.arriveAt - simNow),
      });
    } else {
      const islandId = ferry.islandId;
      const center = islandId === undefined ? undefined : centers.get(islandId);
      if (islandId === undefined || center === undefined) continue;
      const n = docked.get(islandId) ?? 0;
      docked.set(islandId, n + 1);
      ferries.push({
        ...common,
        state: "docked",
        at: {
          x: center.x + islandRadius + dockOffset + n * dockStep,
          y: center.y - islandRadius,
        },
        heading: 0,
        etaMs: null,
      });
    }
  }

  return {
    size: { width: size.width, height: size.height },
    transform,
    markScale: k,
    routes,
    islands,
    piles,
    ferries,
    partition: partitionScene(present, routeEnds, centers),
  };
}

// 分断（T-704）: 嵐で止まっていない航路（storm !== "blocked"）で島をつないだ連結成分。分かれていなければ null。
function partitionScene(
  present: readonly number[],
  routeEnds: readonly { a: number; b: number; storm: RouteLine["storm"] }[],
  centers: ReadonlyMap<number, Point>,
): MapScene["partition"] {
  const view = partitionOf(
    present,
    routeEnds.map((r) => ({ a: r.a, b: r.b, open: r.storm !== "blocked" })),
  );
  if (view === null) return null;
  const halos: PartitionHalo[] = [];
  for (const group of view.groups) {
    const islands: Point[] = [];
    let top: Point | null = null;
    for (const id of group.islandIds) {
      const at = centers.get(id);
      if (at === undefined) continue;
      islands.push(at);
      if (top === null || at.y < top.y || (at.y === top.y && at.x < top.x)) top = at;
    }
    if (top === null) continue;
    halos.push({
      label: group.label,
      majority: group.majority,
      islands,
      badgeAt: { x: top.x, y: top.y - MAP_STYLE.islandRadius * 2 },
    });
  }
  return { total: view.total, groups: view.groups, halos };
}
