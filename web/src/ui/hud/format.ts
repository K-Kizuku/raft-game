// HUD・島の詳細の表示用の書式（T-406）。表示の数は整数で計算する。
import { formatDuration } from "../../app/format.ts";

// 残り時間（0 未満は 0）。秒に切り上げて `m:ss` にする（残り 2:30.4 は「2:31」）。
export function formatRemaining(ms: number): string {
  if (!(ms > 0)) return formatDuration(0);
  return formatDuration(Math.ceil(ms / 1000) * 1000);
}

// スコア（1240 →「1,240」。負の値もそのまま）。
export function formatScore(total: number): string {
  return total.toLocaleString("ja-JP");
}

// 速度（「0.5x」「1x」「4x」）。
export function formatSpeed(speed: number): string {
  return `${String(speed)}x`;
}

// フェス準備度（ゲームデザイン §5.4 の min(確定数 ÷ 目標数, 1)）。percent は整数に切り捨てる。
export function festReadiness(
  successes: number,
  target: number,
): { done: number; target: number; percent: number } {
  if (target <= 0) return { done: successes, target: 0, percent: 0 };
  const done = Math.min(successes, target);
  return { done, target, percent: Math.max(0, Math.floor((done * 100) / target)) };
}

// 許された速度（昇順・重複なし）のうち、current より小さい最大の値と大きい最小の値。
// current が allowed に無くても（デモの 8 倍など）同じ規則。
export function adjacentSpeeds(
  allowed: readonly number[],
  current: number,
): { slower: number | null; faster: number | null } {
  let slower: number | null = null;
  let faster: number | null = null;
  for (const speed of allowed) {
    if (speed < current && (slower === null || speed > slower)) slower = speed;
    if (speed > current && (faster === null || speed < faster)) faster = speed;
  }
  return { slower, faster };
}
