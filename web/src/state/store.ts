// 世界の状態の zustand のストア（T-403 / DesignDoc §13.2・ADR-0010）。
// 届いた snapshot・events を写すだけで、WS・コマンド・ブラウザの保存領域に触れない。
// 操作の関数は this を使わず、ストアの寿命の間ずっと同じ関数（そのまま WS クライアントのハンドラーに渡せる）。
// PixiJS（React の外）は gameStore.getState() と gameStore.subscribe() で読む。
import { useStore } from "zustand";
import { createStore } from "zustand/vanilla";
import type { StoreApi } from "zustand";

import type { Clock, NodeDetail, RoundResult, ServerEvent, Snapshot } from "../protocol/index.ts";
import { applyEvents, withClock, worldFromSnapshot } from "./applyEvent.ts";
import type { World } from "./world.ts";

export type UiSelection = Readonly<{ islandId: number | null; ferryId: string | null }>;

export type GameState = Readonly<{
  world: World | null; // snapshot を受けるまで null
  roundResult: RoundResult | null; // round.result（T-408 が読む）
  nodeDetail: NodeDetail | null; // node.detail（T-407 が読む）
  selection: UiSelection; // マップ・島の詳細・フェリー操作が共有する選択
  applySnapshot(snapshot: Snapshot, receivedAt: number): void;
  applyEvents(clock: Clock, events: readonly ServerEvent[], receivedAt: number): void;
  setRoundResult(result: RoundResult): void;
  setNodeDetail(detail: NodeDetail | null): void;
  selectIsland(islandId: number | null): void;
  selectFerry(ferryId: string | null): void;
  reset(): void;
}>;
export type GameStore = StoreApi<GameState>;

type DataState = Pick<GameState, "world" | "roundResult" | "nodeDetail" | "selection">;

function initialData(): DataState {
  return {
    world: null,
    roundResult: null,
    nodeDetail: null,
    selection: { islandId: null, ferryId: null },
  };
}

function hasIsland(world: World | null, islandId: number): boolean {
  return (
    world !== null &&
    (world.stage.islands[islandId] !== undefined || world.nodes[islandId] !== undefined)
  );
}

function hasFerry(world: World | null, ferryId: string): boolean {
  return world !== null && world.ferries[ferryId] !== undefined;
}

export function createGameStore(): GameStore {
  return createStore<GameState>()((set, get) => ({
    ...initialData(),

    applySnapshot(snapshot, receivedAt) {
      const world = worldFromSnapshot(snapshot, receivedAt);
      const { selection } = get();
      const islandId =
        selection.islandId !== null && hasIsland(world, selection.islandId)
          ? selection.islandId
          : null;
      const ferryId =
        selection.ferryId !== null && hasFerry(world, selection.ferryId) ? selection.ferryId : null;
      const keep = islandId === selection.islandId && ferryId === selection.ferryId;
      set({ world, selection: keep ? selection : { islandId, ferryId } });
    },

    applyEvents(clock, events, receivedAt) {
      const { world } = get();
      if (world === null) return;
      set({ world: withClock(applyEvents(world, events), clock, receivedAt) });
    },

    setRoundResult(result) {
      set({ roundResult: result });
    },

    setNodeDetail(detail) {
      set({ nodeDetail: detail });
    },

    selectIsland(islandId) {
      const { world, selection } = get();
      if (islandId !== null && !hasIsland(world, islandId)) return;
      if (selection.islandId === islandId) return;
      set({ selection: { ...selection, islandId } });
    },

    selectFerry(ferryId) {
      const { world, selection } = get();
      if (ferryId !== null && !hasFerry(world, ferryId)) return;
      if (selection.ferryId === ferryId) return;
      set({ selection: { ...selection, ferryId } });
    },

    reset() {
      set(initialData());
    },
  }));
}

// アプリに 1 つのストア。
export const gameStore: GameStore = createGameStore();

// useGameStore は React からストアを読むフック。selector は新しい配列・オブジェクトを作らないものにする。
export function useGameStore<T>(selector: (state: GameState) => T): T {
  return useStore(gameStore, selector);
}
