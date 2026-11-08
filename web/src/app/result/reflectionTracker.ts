// 振り返りの記録をストアの移り変わりから集める（T-432）。
// ストアの subscribe で前と次の world を受け取り、純粋な関数 stepReflection に渡すだけ。
// ラウンド中（ゲーム画面・リプレイ）から数えるため、アプリに 1 つの記録をモジュールを読んだときに作る
// （ResultScreen は App と ReplayScreen が最初から読むので、ラウンドより前にストアを見始める）。
import { useSyncExternalStore } from "react";

import { gameStore, type GameStore } from "../../state/index.ts";
import {
  EMPTY_REFLECTION_TALLY,
  stepReflection,
  type ReflectionTally,
} from "../../state/reflection.ts";

export type ReflectionTracker = {
  getSnapshot(): ReflectionTally;
  subscribe(listener: () => void): () => void;
  dispose(): void;
};

export function createReflectionTracker(store: GameStore): ReflectionTracker {
  const listeners = new Set<() => void>();
  let tally = stepReflection(EMPTY_REFLECTION_TALLY, null, store.getState().world);
  const unsubscribe = store.subscribe((state, prevState) => {
    if (state.world === prevState.world) return;
    const next = stepReflection(tally, prevState.world, state.world);
    if (next === tally) return;
    tally = next;
    for (const listener of [...listeners]) listener();
  });
  return {
    getSnapshot: () => tally,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    dispose: unsubscribe,
  };
}

export const reflectionTracker: ReflectionTracker = createReflectionTracker(gameStore);

export function useReflectionTally(
  tracker: ReflectionTracker = reflectionTracker,
): ReflectionTally {
  return useSyncExternalStore(tracker.subscribe, tracker.getSnapshot, tracker.getSnapshot);
}
