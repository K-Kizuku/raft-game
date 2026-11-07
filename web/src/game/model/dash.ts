// 点線の分割（T-404）。PixiJS v8 の Graphics に点線が無いので、線分の列にして描く。
import type { Point } from "./viewport.ts";

// dashSegments は a から b へ、長さ dash の線分を gap の間隔で並べる。最後の線分は b で切る。
// a と b が同じ点・dash ≤ 0 なら []。
export function dashSegments(
  a: Point,
  b: Point,
  dash: number,
  gap: number,
): readonly (readonly [Point, Point])[] {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const length = Math.hypot(dx, dy);
  if (!(length > 0) || !(dash > 0) || !Number.isFinite(length)) return [];
  const step = dash + Math.max(0, gap);
  const ux = dx / length;
  const uy = dy / length;
  const at = (d: number): Point => ({ x: a.x + ux * d, y: a.y + uy * d });
  const segments: (readonly [Point, Point])[] = [];
  for (let i = 0; i * step < length; i++) {
    const start = i * step;
    const end = Math.min(start + dash, length);
    segments.push([at(start), end === length ? b : at(end)]);
  }
  return segments;
}
