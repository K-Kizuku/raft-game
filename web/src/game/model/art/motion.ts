// マップの小さな動きの量（T-421）。時刻は引数の nowMs（実時間）だけを使う。
// 動きを減らす設定（prefers-reduced-motion: reduce）では 0 を返し、静止した絵にする。
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

/** 波の記号の横のずれ（px）。-waveAmplitude〜waveAmplitude。 */
export function waveOffset(nowMs: number, reducedMotion: boolean): number {
  if (reducedMotion || !Number.isFinite(nowMs)) return 0;
  return A.waveAmplitude * Math.sin((2 * Math.PI * nowMs) / A.wavePeriodMs);
}

/** 雨の線の縦のずれ（px）。0 以上 period 未満で、period ごとに巻き戻る。 */
export function rainOffset(nowMs: number, period: number, reducedMotion: boolean): number {
  if (reducedMotion || !(period > 0) || !Number.isFinite(nowMs)) return 0;
  const v = (nowMs * A.rainSpeed) % period;
  const wrapped = v < 0 ? v + period : v;
  return wrapped >= period ? 0 : wrapped;
}

/** 航行中の船の揺れ。seed（船ごと）で揺れの位相をずらす。 */
export function bobOffset(
  nowMs: number,
  seed: number,
  reducedMotion: boolean,
): { y: number; rotation: number } {
  if (reducedMotion || !Number.isFinite(nowMs)) return { y: 0, rotation: 0 };
  const phase = ((seed >>> 0) % 997) * 0.37;
  const t = (2 * Math.PI * nowMs) / A.bobPeriodMs + phase;
  return { y: A.bobAmplitude * Math.sin(t), rotation: A.bobRotation * Math.sin(t + Math.PI / 2) };
}

/** 文字列（船の ID など）から bobOffset の種を作る。 */
export function seedOf(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}
