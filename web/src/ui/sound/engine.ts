// 効果音を鳴らす係（T-434）。ブラウザの自動再生の決まりに従い、最初の操作（クリック・キー）までは
// AudioContext を作らず、音を出さない。音を消していれば鳴らさず、間引き（throttle.ts）を通ったものだけ鳴らす。
import { SOUND_RECIPES, type SoundKind } from "./recipes.ts";
import { playTones, type SynthContext } from "./synth.ts";
import { createSoundThrottle, type SoundThrottle } from "./throttle.ts";
import { soundPreference } from "./preference.ts";

export type EngineContext = SynthContext & {
  readonly state: AudioContextState;
  resume(): Promise<void>;
};

export type SoundEngineDeps = Readonly<{
  createContext: () => EngineContext | null;
  isEnabled: () => boolean;
  now: () => number;
  throttle?: SoundThrottle;
  volume?: number;
}>;

export type SoundEngine = {
  // 利用者の操作の中で呼ぶ。AudioContext を作る（作れないブラウザでは何もしない）。
  unlock(): void;
  unlocked(): boolean;
  // kind を鳴らす。鳴らしたら true（音を消している・まだ操作がない・間引いたときは false）。
  play(kind: SoundKind): boolean;
};

// 全体の音量（画面のための値）。
export const SOUND_VOLUME = 0.6;

export function createSoundEngine(deps: SoundEngineDeps): SoundEngine {
  const throttle = deps.throttle ?? createSoundThrottle();
  const volume = deps.volume ?? SOUND_VOLUME;
  let ctx: EngineContext | null = null;
  let failed = false;

  function resume(context: EngineContext): void {
    if (context.state === "suspended") {
      context.resume().catch(() => undefined);
    }
  }

  function unlock(): void {
    if (ctx !== null) {
      resume(ctx);
      return;
    }
    if (failed) return;
    try {
      ctx = deps.createContext();
    } catch {
      ctx = null;
    }
    if (ctx === null) {
      failed = true;
      return;
    }
    resume(ctx);
  }

  function play(kind: SoundKind): boolean {
    if (ctx === null || !deps.isEnabled()) return false;
    if (!throttle.allow(kind, deps.now())) return false;
    resume(ctx);
    try {
      playTones(ctx, SOUND_RECIPES[kind], volume);
    } catch {
      return false;
    }
    return true;
  }

  return { unlock, unlocked: () => ctx !== null, play };
}

function createBrowserContext(): EngineContext | null {
  if (typeof window === "undefined" || typeof window.AudioContext !== "function") {
    return null;
  }
  return new window.AudioContext();
}

// アプリに 1 つの係。
export const soundEngine: SoundEngine = createSoundEngine({
  createContext: createBrowserContext,
  isEnabled: () => soundPreference.getSnapshot().enabled,
  now: () => performance.now(),
});

// 最初の操作で unlock する。すでにページで操作があった（navigator.userActivation.hasBeenActive）なら、すぐに unlock する。
// 戻り値はリスナーを外す関数。
export function armSoundUnlock(
  engine: SoundEngine,
  target: Pick<EventTarget, "addEventListener" | "removeEventListener">,
  hasBeenActive: boolean,
): () => void {
  if (engine.unlocked()) return () => undefined;
  if (hasBeenActive) {
    engine.unlock();
    return () => undefined;
  }
  const events = ["pointerdown", "keydown"] as const;
  function onGesture(): void {
    engine.unlock();
    remove();
  }
  function remove(): void {
    for (const type of events) target.removeEventListener(type, onGesture, true);
  }
  for (const type of events) target.addEventListener(type, onGesture, true);
  return remove;
}
