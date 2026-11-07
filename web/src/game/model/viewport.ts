// 島の座標（ステージの layout）を画面の px に写す（T-404 / DesignDoc §13.3）。
import { MAP_STYLE } from "./style.ts";

export type Point = Readonly<{ x: number; y: number }>;
export type Size = Readonly<{ width: number; height: number }>;
export type MapTransform = Readonly<{ scale: number; offsetX: number; offsetY: number }>;

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

// fitIslands は島の外接矩形が余白を残して画面に収まり、その中心が画面の中心に来る変換を返す。
// 画面が余白より小さいときは minScale を使う（例外を出さない・NaN を返さない）。
export function fitIslands(points: readonly Point[], size: Size): MapTransform {
  const first = points[0];
  if (first === undefined) return { scale: 1, offsetX: 0, offsetY: 0 };
  let minX = first.x;
  let maxX = first.x;
  let minY = first.y;
  let maxY = first.y;
  for (const p of points) {
    minX = Math.min(minX, p.x);
    maxX = Math.max(maxX, p.x);
    minY = Math.min(minY, p.y);
    maxY = Math.max(maxY, p.y);
  }
  const w = maxX - minX > 0 ? maxX - minX : 1;
  const h = maxY - minY > 0 ? maxY - minY : 1;
  const availW = size.width - 2 * MAP_STYLE.padding;
  const availH = size.height - 2 * MAP_STYLE.padding;
  const raw = availW > 0 && availH > 0 ? Math.min(availW / w, availH / h) : MAP_STYLE.minScale;
  const scale = Number.isFinite(raw)
    ? clamp(raw, MAP_STYLE.minScale, MAP_STYLE.maxScale)
    : MAP_STYLE.minScale;
  const cx = (minX + maxX) / 2;
  const cy = (minY + maxY) / 2;
  const width = Number.isFinite(size.width) ? size.width : 0;
  const height = Number.isFinite(size.height) ? size.height : 0;
  return { scale, offsetX: width / 2 - cx * scale, offsetY: height / 2 - cy * scale };
}

export function toScreen(t: MapTransform, p: Point): Point {
  return { x: p.x * t.scale + t.offsetX, y: p.y * t.scale + t.offsetY };
}
