// 水彩風の海のにじみと、海図の波の記号の位置（T-421）。画面の大きさだけから決定的に決める
// （固定の種の擬似乱数）。同じ大きさなら同じ結果なので、描き直すのは画面の大きさが変わったときだけでよい。
import type { Point, Size } from "../viewport.ts";
import { createRandom } from "./random.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

export type SeaTone = "light" | "mid" | "deep";
export type SeaBlob = Readonly<{ x: number; y: number; r: number; tone: SeaTone }>;

const TONES: readonly SeaTone[] = ["light", "mid", "deep", "light"];

function validSize(size: Size): boolean {
  return (
    Number.isFinite(size.width) && Number.isFinite(size.height) && size.width > 0 && size.height > 0
  );
}

/** 海のにじみ。中心は画面の中、半径は画面の短い辺の seaBlobMinRatio〜seaBlobMaxRatio 倍。 */
export function seaBlobs(size: Size, count: number = A.seaBlobCount): SeaBlob[] {
  if (!validSize(size) || !(count > 0)) return [];
  const rand = createRandom(A.seaBlobSeed);
  const short = Math.min(size.width, size.height);
  const out: SeaBlob[] = [];
  for (let i = 0; i < Math.floor(count); i++) {
    const x = rand() * size.width;
    const y = rand() * size.height;
    const r = short * (A.seaBlobMinRatio + (A.seaBlobMaxRatio - A.seaBlobMinRatio) * rand());
    const tone = TONES[Math.floor(rand() * TONES.length)] ?? "light";
    out.push({ x, y, r, tone });
  }
  return out;
}

/** 波の記号の位置（格子から少しずらす）。画面の外へ揺れても欠けないよう、端の 1 列分だけ外にも置く。 */
export function waveMarks(size: Size): Point[] {
  if (!validSize(size)) return [];
  const rand = createRandom(A.waveSeed);
  const step = A.waveSpacing;
  const jitter = step * A.waveJitter;
  const out: Point[] = [];
  let row = 0;
  for (let y = step / 2; y < size.height; y += step, row++) {
    const shift = row % 2 === 0 ? 0 : step / 2; // 段ごとに半分ずらして格子に見せない
    for (let x = -step / 2 + shift; x < size.width + step / 2; x += step) {
      out.push({ x: x + (rand() - 0.5) * jitter, y: y + (rand() - 0.5) * jitter });
    }
  }
  return out;
}
