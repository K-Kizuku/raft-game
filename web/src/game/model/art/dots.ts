// 航路の点線（海図風）の点の位置（T-421）。両端は島に隠れるので inset だけ空け、残りの真ん中にそろえる。
import type { Point } from "../viewport.ts";

/** a から b への航路の点の位置。両端から inset 以上空け、点の間隔は spacing。 */
export function dotPositions(a: Point, b: Point, spacing: number, inset: number): Point[] {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const length = Math.hypot(dx, dy);
  const usable = length - 2 * inset;
  if (!(spacing > 0) || !(usable >= 0) || length === 0) return [];
  const count = Math.floor(usable / spacing) + 1;
  const start = inset + (usable - (count - 1) * spacing) / 2;
  const ux = dx / length;
  const uy = dy / length;
  const out: Point[] = [];
  for (let i = 0; i < count; i++) {
    const d = start + i * spacing;
    out.push({ x: a.x + ux * d, y: a.y + uy * d });
  }
  return out;
}

/** a から b の線分の両端を inset ずつ縮める。短すぎるときは null。 */
export function insetSegment(a: Point, b: Point, inset: number): readonly [Point, Point] | null {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const length = Math.hypot(dx, dy);
  if (length <= 2 * inset || length === 0) return null;
  const ux = dx / length;
  const uy = dy / length;
  return [
    { x: a.x + ux * inset, y: a.y + uy * inset },
    { x: b.x - ux * inset, y: b.y - uy * inset },
  ];
}
