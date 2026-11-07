// 島の不定形の輪郭（T-421）。島の ID から決定的に決める（同じ島は毎回同じ形）。
// 頂点は中心から radius × islandMinRatio 以上 radius 以下に置くので、頂点を制御点にした
// なめらかな曲線（smoothOutline）も radius の円からはみ出さない（当たり判定の円の中に収まる）。
import type { Point } from "../viewport.ts";
import { createRandom, mixSeed } from "./random.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

/** 島の輪郭の頂点（中心が原点・時計回り）。 */
export function islandOutline(
  id: number,
  radius: number,
  points: number = A.islandPoints,
): Point[] {
  const n = Math.max(3, Math.floor(points));
  const rand = createRandom(mixSeed(id, A.islandSeed));
  // 低い周波数の波を 2 つ重ね、丸みを残したまま島ごとに違うふくらみにする。
  const phase2 = rand() * 2 * Math.PI;
  const phase3 = rand() * 2 * Math.PI;
  const weight2 = 0.5 + rand();
  const weight3 = 0.3 + rand() * 0.7;
  const tilt = rand() * 2 * Math.PI;
  const min = A.islandMinRatio;
  const out: Point[] = [];
  for (let i = 0; i < n; i++) {
    const angle = tilt + (i / n) * 2 * Math.PI;
    const wave =
      (weight2 * Math.sin(2 * angle + phase2) + weight3 * Math.sin(3 * angle + phase3)) /
      (weight2 + weight3); // -1〜1
    const jitter = (rand() - 0.5) * 0.3; // 少しのむら
    const t = Math.min(1, Math.max(0, (wave + jitter + 1) / 2));
    const r = radius * (min + (1 - min) * t);
    out.push({ x: Math.cos(angle) * r, y: Math.sin(angle) * r });
  }
  return out;
}

/** 輪郭を scale 倍にする（砂浜の内側の島の色の部分・浅瀬の輪）。 */
export function scaleOutline(points: readonly Point[], scale: number): Point[] {
  return points.map((p) => ({ x: p.x * scale, y: p.y * scale }));
}

/**
 * 頂点を制御点にした 2 次ベジェの閉じた曲線（隣の頂点との中点を通る）。
 * start は最初の点、curves は [制御点, 終点] の並び。描画の層が moveTo・quadraticCurveTo で描く。
 */
export function smoothOutline(points: readonly Point[]): {
  start: Point;
  curves: readonly (readonly [Point, Point])[];
} {
  const n = points.length;
  if (n === 0) return { start: { x: 0, y: 0 }, curves: [] };
  const mid = (a: Point, b: Point): Point => ({ x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
  const at = (i: number): Point => points[((i % n) + n) % n] as Point;
  const start = mid(at(n - 1), at(0));
  const curves: (readonly [Point, Point])[] = [];
  for (let i = 0; i < n; i++) curves.push([at(i), mid(at(i), at(i + 1))]);
  return { start, curves };
}
