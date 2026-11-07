// フェリー操作パネル（T-405）に出すものを世界の状態から作る純粋な関数（DesignDoc §13.8）。
// 積み込み・出航の可否はサーバーが決める（ADR-0009）。ここで求める「押せない理由」は、
// 送っても明らかに失敗するものだけ。満載・行き先宛ての手紙が無い・故障はサーバーの ack で知らせる。
// 引数を書き換えず、呼ぶたびに新しい配列を返す（React では useMemo の中で呼ぶ）。時刻は引数の simNow。
import type { Letter } from "../../protocol/index.ts";
import { portPiles } from "../../state/index.ts";
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
}>;
export type PortGroup = Readonly<{
  to: number;
  toName: string;
  toColor: string;
  letters: readonly Letter[];
}>;
export type CargoSlot = Readonly<
  | { kind: "letter"; letter: Letter; toName: string; toColor: string; unloadable: boolean }
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
export type FerryDetail = Readonly<{
  ferry: FerryView;
  name: string;
  state: "docked" | "sailing";
  broken: boolean;
  islandId: number | null; // docked のときだけ
  islandName: string | null;
  toName: string | null; // sailing のときだけ
  etaMs: number | null;
  readyInMs: number; // docked: max(0, readyAt − simNow)、sailing: 0
  port: readonly PortGroup[]; // docked: いる島の港の手紙。sailing: []
  cargo: readonly CargoSlot[];
  destinations: readonly Destination[]; // docked: 航路でつながる島。sailing: []
}>;
export type FerryBlocker = "sailing" | "no_destination" | "not_ready" | "route_blocked";

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
    });
  }
  return tabs;
}

function cargoSlots(world: World, ferry: FerryView, islandId: number | null): CargoSlot[] {
  const slots: CargoSlot[] = [];
  for (const id of ferry.cargo) {
    const letter = Object.hasOwn(world.letters, id) ? world.letters[id] : undefined;
    if (letter === undefined) {
      slots.push({ kind: "unknown", letterId: id });
      continue;
    }
    slots.push({
      kind: "letter",
      letter,
      toName: islandName(world, letter.to),
      toColor: islandColor(world, letter.to),
      unloadable: islandId !== null && letter.from === islandId,
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
  if (ferry.state === "sailing") {
    return {
      ferry,
      name: ferry.name,
      state: "sailing",
      broken,
      islandId: null,
      islandName: null,
      toName: ferry.to === undefined ? null : islandName(world, ferry.to),
      etaMs: etaOf(ferry, simNow),
      readyInMs: 0,
      port: [],
      cargo: cargoSlots(world, ferry, null),
      destinations: [],
    };
  }
  const islandId = ferry.islandId ?? null;
  return {
    ferry,
    name: ferry.name,
    state: "docked",
    broken,
    islandId,
    islandName: islandId === null ? null : islandName(world, islandId),
    toName: null,
    etaMs: null,
    readyInMs: Math.max(0, ferry.readyAt - simNow),
    port:
      islandId === null
        ? []
        : portPiles(world, islandId).map((pile) => ({
            to: pile.to,
            toName: islandName(world, pile.to),
            toColor: islandColor(world, pile.to),
            letters: pile.letters,
          })),
    cargo: cargoSlots(world, ferry, islandId),
    destinations: islandId === null ? [] : destinationsFrom(world, islandId, simNow),
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
