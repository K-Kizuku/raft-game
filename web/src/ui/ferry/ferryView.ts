// フェリー操作パネル（T-405）に出すものを世界の状態から作る純粋な関数（DesignDoc §13.8）。
// 積み込み・出航の可否はサーバーが決める（ADR-0009）。ここで求める「押せない理由」は、
// 送っても明らかに失敗するものだけ。満載・行き先宛ての手紙が無い・故障はサーバーの ack で知らせる。
// 引数を書き換えず、呼ぶたびに新しい配列を返す（React では useMemo の中で呼ぶ）。時刻は引数の simNow。
import type { Letter, Route } from "../../protocol/index.ts";
import { isAutoFerry, portPiles } from "../../state/index.ts";
import type { FerryView, UiSelection, World } from "../../state/index.ts";

export type FerryTab = Readonly<{
  id: string;
  name: string;
  selected: boolean;
  state: "docked" | "sailing";
  placeName: string | null; // docked: いる島の名前、sailing: 行き先の島の名前（どちらも分からなければ null）
  etaMs: number | null; // sailing: max(0, arriveAt − simNow)、docked: null
  cargoCount: number;
  capacity: number;
  broken: boolean;
  auto: boolean; // 自動航行（cmd.ferry.auto。T-725）の船か
}>;
export type PortGroup = Readonly<{
  to: number;
  toName: string;
  toColor: string;
  letters: readonly Letter[];
}>;
// 停泊中の島の中継倉庫（FR-FERRY-06）の手紙の山。形は PortGroup と同じ。
export type RelayGroup = Readonly<{
  to: number;
  toName: string;
  toColor: string;
  letters: readonly Letter[];
}>;
export type CargoSlot = Readonly<
  | {
      kind: "letter";
      letter: Letter;
      toName: string;
      toColor: string;
      unloadTo: "port" | "relay"; // 降ろし先（この島で生まれた手紙は港、それ以外は倉庫。サーバーが送信元で決める）
      unloadable: boolean;
      unloadNote: string | null; // 押せない理由（押せるときは null）
      viaName: string | null; // 経由して運ぶ手紙なら、経由する島の名前（「豊島経由 → 小豆島」）。直行なら null
    }
  | { kind: "unknown"; letterId: number } // cargo にあって letters に無い ID
  | { kind: "empty" }
>;
export type Destination = Readonly<{
  islandId: number;
  name: string;
  routeId: string;
  blocked: boolean;
  forecast: boolean;
}>;
// 港にある、隣でない島宛ての手紙の案内（「小豆島あての手紙は豊島を経由して運びます。まず豊島へ」）。
export type ViaHint = Readonly<{
  to: number;
  toName: string;
  via: number; // 経由する隣の島（ゲームデザイン §3.4 の最短の経路で次に寄る島）
  viaName: string;
}>;
export type FerryDetail = Readonly<{
  ferry: FerryView;
  name: string;
  state: "docked" | "sailing";
  broken: boolean;
  auto: boolean; // 自動航行（cmd.ferry.auto。T-725）の船か。自動の船は手で操作しない
  islandId: number | null; // docked のときだけ
  islandName: string | null;
  toName: string | null; // sailing のときだけ
  etaMs: number | null;
  readyInMs: number; // docked: max(0, readyAt − simNow)、sailing: 0
  port: readonly PortGroup[]; // docked: いる島の港の手紙。sailing: []
  relayCap: number; // 島ごとの中継倉庫の上限（world.stage.relayCap ?? 0。0 なら積み替えなし）
  relayCount: number; // docked: いる島の倉庫の手紙の数。sailing: 0
  relay: readonly RelayGroup[]; // docked: いる島の倉庫の手紙（宛先の昇順の山・山の中は ID の昇順）。sailing: []
  cargo: readonly CargoSlot[];
  destinations: readonly Destination[]; // docked: 航路でつながる島。sailing: []
  viaHints: readonly ViaHint[]; // docked: 港の手紙の山のうち、隣でない島宛てのもの（山の順）。sailing: []
}>;
export type FerryBlocker = "sailing" | "no_destination" | "not_ready" | "route_blocked";

// 積み荷の手紙を降ろせない理由（DesignDoc §13.8）。
export const UNLOAD_NOTE_ORIGIN_ONLY = "この島で生まれた手紙だけ降ろせます";
export const UNLOAD_NOTE_RELAY_FULL = "倉庫がいっぱいです";

// 島がステージに無いときの色。
const UNKNOWN_ISLAND_COLOR = "#9CA3AF";

export function islandName(world: World, id: number): string {
  const island = Object.hasOwn(world.stage.islands, id) ? world.stage.islands[id] : undefined;
  return island?.name ?? `島 ${String(id)}`;
}

export function islandColor(world: World, id: number): string {
  const island = Object.hasOwn(world.stage.islands, id) ? world.stage.islands[id] : undefined;
  return island?.color ?? UNKNOWN_ISLAND_COLOR;
}

function isBroken(ferry: FerryView, simNow: number): boolean {
  return ferry.brokenUntil !== null && ferry.brokenUntil > simNow;
}

function etaOf(ferry: FerryView, simNow: number): number {
  return Math.max(0, ferry.arriveAt - simNow);
}

function getFerry(world: World, ferryId: string): FerryView | undefined {
  return Object.hasOwn(world.ferries, ferryId) ? world.ferries[ferryId] : undefined;
}

export function ferryTabs(world: World, selection: UiSelection, simNow: number): FerryTab[] {
  const tabs: FerryTab[] = [];
  for (const id of world.ferryIds) {
    const ferry = getFerry(world, id);
    if (ferry === undefined) continue;
    const sailing = ferry.state === "sailing";
    const place = sailing ? ferry.to : ferry.islandId;
    tabs.push({
      id,
      name: ferry.name,
      selected: selection.ferryId === id,
      state: sailing ? "sailing" : "docked",
      placeName: place === undefined ? null : islandName(world, place),
      etaMs: sailing ? etaOf(ferry, simNow) : null,
      cargoCount: ferry.cargo.length,
      capacity: ferry.capacity,
      broken: isBroken(ferry, simNow),
      auto: isAutoFerry(world, id),
    });
  }
  return tabs;
}

function routesOf(world: World): Route[] {
  const routes: Route[] = [];
  for (const routeId of world.stage.routeIds) {
    const route = Object.hasOwn(world.stage.routes, routeId)
      ? world.stage.routes[routeId]
      : undefined;
    if (route !== undefined) routes.push(route);
  }
  return routes;
}

function otherEnd(route: Route, islandId: number): number | null {
  if (route.a === islandId) return route.b;
  if (route.b === islandId) return route.a;
  return null;
}

// viaIsland は、from から to へ経由で運ぶときに次に寄る隣の島を返す（ゲームデザイン §3.4。サーバーの
// おまかせ積み込みと同じ決め方）。最短の経路は航路（嵐で止まっているものを含む）の travelMs の和で決め、
// 同じ長さなら次の島の ID の小さい方。同じ島・直接の航路がある・経路がないときは null。
export function viaIsland(world: World, from: number, to: number): number | null {
  if (from === to) return null;
  const routes = routesOf(world);
  if (routes.some((r) => otherEnd(r, from) === to)) return null;
  // to からの最短の所要時間（Dijkstra 法。島の数が少ないので配列で探す）。
  const dist = new Map<number, number>([[to, 0]]);
  const done = new Set<number>();
  for (;;) {
    let cur: number | null = null;
    let curDist = Infinity;
    for (const [id, d] of dist) {
      if (!done.has(id) && d < curDist) {
        cur = id;
        curDist = d;
      }
    }
    if (cur === null) break;
    done.add(cur);
    for (const r of routes) {
      const n = otherEnd(r, cur);
      if (n === null) continue;
      const nd = curDist + r.travelMs;
      const old = dist.get(n);
      if (old === undefined || nd < old) dist.set(n, nd);
    }
  }
  let next: number | null = null;
  let best = Infinity;
  for (const r of routes) {
    const n = otherEnd(r, from);
    if (n === null) continue;
    const d = dist.get(n);
    if (d === undefined) continue;
    const cost = r.travelMs + d;
    if (cost < best || (cost === best && next !== null && n < next)) {
      best = cost;
      next = n;
    }
  }
  return next;
}

// 積み荷の手紙を経由して運ぶなら、経由する島。停泊中は今いる島からの viaIsland、航行中は宛先が向かっている島で
// なければ向かっている島。
function cargoVia(world: World, ferry: FerryView, letter: Letter): number | null {
  if (ferry.state === "sailing") {
    return ferry.to !== undefined && letter.to !== ferry.to ? ferry.to : null;
  }
  return ferry.islandId === undefined ? null : viaIsland(world, ferry.islandId, letter.to);
}

type RelayRoom = Readonly<{ cap: number; count: number }>;

function unloadState(
  letter: Letter,
  islandId: number | null,
  relay: RelayRoom,
): Pick<Extract<CargoSlot, { kind: "letter" }>, "unloadTo" | "unloadable" | "unloadNote"> {
  const unloadTo = islandId !== null && letter.from === islandId ? "port" : "relay";
  if (islandId === null) return { unloadTo, unloadable: false, unloadNote: null };
  if (unloadTo === "port") return { unloadTo, unloadable: true, unloadNote: null };
  if (relay.cap <= 0) {
    return { unloadTo, unloadable: false, unloadNote: UNLOAD_NOTE_ORIGIN_ONLY };
  }
  if (relay.count >= relay.cap) {
    return { unloadTo, unloadable: false, unloadNote: UNLOAD_NOTE_RELAY_FULL };
  }
  return { unloadTo, unloadable: true, unloadNote: null };
}

// islandId の中継倉庫にある手紙を、宛先の昇順の山（山の中は ID の昇順）にまとめる。
function relayGroups(world: World, islandId: number): RelayGroup[] {
  const byTo = new Map<number, Letter[]>();
  for (const letter of Object.values(world.letters)) {
    if (letter.loc.type !== "relay" || letter.loc.islandId !== islandId) continue;
    const pile = byTo.get(letter.to);
    if (pile === undefined) byTo.set(letter.to, [letter]);
    else pile.push(letter);
  }
  return [...byTo.keys()]
    .sort((a, b) => a - b)
    .map((to) => ({
      to,
      toName: islandName(world, to),
      toColor: islandColor(world, to),
      letters: [...(byTo.get(to) ?? [])].sort((a, b) => a.id - b.id),
    }));
}

function cargoSlots(
  world: World,
  ferry: FerryView,
  islandId: number | null,
  relay: RelayRoom,
): CargoSlot[] {
  const slots: CargoSlot[] = [];
  for (const id of ferry.cargo) {
    const letter = Object.hasOwn(world.letters, id) ? world.letters[id] : undefined;
    if (letter === undefined) {
      slots.push({ kind: "unknown", letterId: id });
      continue;
    }
    const via = cargoVia(world, ferry, letter);
    slots.push({
      kind: "letter",
      letter,
      toName: islandName(world, letter.to),
      toColor: islandColor(world, letter.to),
      ...unloadState(letter, islandId, relay),
      viaName: via === null ? null : islandName(world, via),
    });
  }
  const empty = Math.max(0, ferry.capacity - ferry.cargo.length);
  for (let i = 0; i < empty; i++) slots.push({ kind: "empty" });
  return slots;
}

function destinationsFrom(world: World, islandId: number, simNow: number): Destination[] {
  const destinations: Destination[] = [];
  const seen = new Set<number>();
  for (const routeId of world.stage.routeIds) {
    const route = Object.hasOwn(world.stage.routes, routeId)
      ? world.stage.routes[routeId]
      : undefined;
    if (route === undefined) continue;
    let other: number;
    if (route.a === islandId) other = route.b;
    else if (route.b === islandId) other = route.a;
    else continue;
    if (seen.has(other)) continue;
    seen.add(other);
    const state = Object.hasOwn(world.routeStates, routeId)
      ? world.routeStates[routeId]
      : undefined;
    const blockedUntil = state?.blockedUntil ?? null;
    destinations.push({
      islandId: other,
      name: islandName(world, other),
      routeId,
      blocked: blockedUntil !== null && blockedUntil > simNow,
      forecast: world.forecasts.some((f) => f.at > simNow && f.routeIds.includes(routeId)),
    });
  }
  return destinations;
}

export function ferryDetail(world: World, ferryId: string, simNow: number): FerryDetail | null {
  const ferry = getFerry(world, ferryId);
  if (ferry === undefined) return null;
  const broken = isBroken(ferry, simNow);
  const auto = isAutoFerry(world, ferryId);
  const relayCap = world.stage.relayCap ?? 0;
  if (ferry.state === "sailing") {
    return {
      ferry,
      name: ferry.name,
      state: "sailing",
      broken,
      auto,
      islandId: null,
      islandName: null,
      toName: ferry.to === undefined ? null : islandName(world, ferry.to),
      etaMs: etaOf(ferry, simNow),
      readyInMs: 0,
      port: [],
      relayCap,
      relayCount: 0,
      relay: [],
      cargo: cargoSlots(world, ferry, null, { cap: relayCap, count: 0 }),
      destinations: [],
      viaHints: [],
    };
  }
  const islandId = ferry.islandId ?? null;
  const piles = islandId === null ? [] : portPiles(world, islandId);
  const relay = islandId === null ? [] : relayGroups(world, islandId);
  const relayCount = relay.reduce((n, group) => n + group.letters.length, 0);
  const viaHints: ViaHint[] = [];
  if (islandId !== null) {
    for (const pile of piles) {
      const via = viaIsland(world, islandId, pile.to);
      if (via === null) continue;
      viaHints.push({
        to: pile.to,
        toName: islandName(world, pile.to),
        via,
        viaName: islandName(world, via),
      });
    }
  }
  return {
    ferry,
    name: ferry.name,
    state: "docked",
    broken,
    auto,
    islandId,
    islandName: islandId === null ? null : islandName(world, islandId),
    toName: null,
    etaMs: null,
    readyInMs: Math.max(0, ferry.readyAt - simNow),
    port: piles.map((pile) => ({
      to: pile.to,
      toName: islandName(world, pile.to),
      toColor: islandColor(world, pile.to),
      letters: pile.letters,
    })),
    relayCap,
    relayCount,
    relay,
    cargo: cargoSlots(world, ferry, islandId, { cap: relayCap, count: relayCount }),
    destinations: islandId === null ? [] : destinationsFrom(world, islandId, simNow),
    viaHints,
  };
}

function findDestination(detail: FerryDetail, to: number | null): Destination | undefined {
  if (to === null) return undefined;
  return detail.destinations.find((d) => d.islandId === to);
}

// おまかせ積み込み（ゲームデザイン §3.4）。嵐の航路の行き先でも積める。
export function autoloadBlocker(detail: FerryDetail, to: number | null): FerryBlocker | null {
  if (detail.state === "sailing") return "sailing";
  if (findDestination(detail, to) === undefined) return "no_destination";
  return null;
}

// 出航（FR-FERRY-07・ゲームデザイン §3.2）。おまかせの条件に加えて、停泊の時間と嵐を調べる。
export function departBlocker(detail: FerryDetail, to: number | null): FerryBlocker | null {
  const blocker = autoloadBlocker(detail, to);
  if (blocker !== null) return blocker;
  if (detail.readyInMs > 0) return "not_ready";
  if (findDestination(detail, to)?.blocked === true) return "route_blocked";
  return null;
}
