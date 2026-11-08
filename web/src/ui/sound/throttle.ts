// 効果音の間引き（T-434）。同じ時刻にたくさんの出来事が来ても鳴らしすぎない。
// - 同じ種類の音は minGapMs の間に 1 回だけ
// - どの種類でも windowMs の間に maxPerWindow 回まで
// 時刻は呼び出し側の performance.now()（ミリ秒）。値は耳のための値で、ゲームの数値ではない。
import type { SoundKind } from "./recipes.ts";

export type ThrottleOptions = Readonly<{
  minGapMs: number;
  windowMs: number;
  maxPerWindow: number;
}>;

export const DEFAULT_THROTTLE: ThrottleOptions = Object.freeze({
  minGapMs: 180,
  windowMs: 1000,
  maxPerWindow: 4,
});

export type SoundThrottle = {
  // nowMs に kind を鳴らしてよいか。よければ鳴らしたものとして数える。
  allow(kind: SoundKind, nowMs: number): boolean;
  reset(): void;
};

export function createSoundThrottle(options: ThrottleOptions = DEFAULT_THROTTLE): SoundThrottle {
  const last = new Map<SoundKind, number>();
  let recent: number[] = [];

  function allow(kind: SoundKind, nowMs: number): boolean {
    const previous = last.get(kind);
    if (previous !== undefined && nowMs - previous < options.minGapMs) {
      return false;
    }
    recent = recent.filter((t) => nowMs - t < options.windowMs);
    if (recent.length >= options.maxPerWindow) {
      return false;
    }
    last.set(kind, nowMs);
    recent.push(nowMs);
    return true;
  }

  function reset(): void {
    last.clear();
    recent = [];
  }

  return { allow, reset };
}
