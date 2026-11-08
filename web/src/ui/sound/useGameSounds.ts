// ゲーム画面（ライブ・リプレイ）で、ストアの世界が変わるたびに効果音を鳴らすフック（T-434）。
import { useEffect } from "react";

import { gameStore, type GameStore } from "../../state/index.ts";
import { soundCues } from "./cues.ts";
import { armSoundUnlock, soundEngine, type SoundEngine } from "./engine.ts";

function pageHasBeenActive(): boolean {
  if (typeof navigator === "undefined") return false;
  const activation = (navigator as { userActivation?: { hasBeenActive?: boolean } }).userActivation;
  return activation?.hasBeenActive === true;
}

export function useGameSounds(
  store: GameStore = gameStore,
  engine: SoundEngine = soundEngine,
): void {
  useEffect(() => {
    if (typeof window === "undefined") return undefined;
    return armSoundUnlock(engine, window, pageHasBeenActive());
  }, [engine]);

  useEffect(() => {
    return store.subscribe((state, prev) => {
      if (state.world === prev.world) return;
      for (const kind of soundCues(prev.world, state.world)) {
        engine.play(kind);
      }
    });
  }, [store, engine]);
}
