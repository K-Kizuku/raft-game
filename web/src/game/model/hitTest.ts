// マップのクリックの当たり判定（T-404 / DesignDoc §13.3）。PixiJS のイベントの仕組みを使わず、
// 最後に描いた MapScene で判定する。
import type { MapScene } from "./scene.ts";
import { MAP_STYLE } from "./style.ts";
import type { Point } from "./viewport.ts";

export type MapHit = Readonly<{ kind: "ferry"; id: string } | { kind: "island"; id: number }>;

function nearest<T extends { at: Point }>(items: readonly T[], p: Point, radius: number): T | null {
  let best: T | null = null;
  let bestDist = Infinity;
  for (const item of items) {
    const d = Math.hypot(item.at.x - p.x, item.at.y - p.y);
    // 同じ距離なら並びで先のもの（< で比べる）。
    if (d <= radius && d < bestDist) {
      best = item;
      bestDist = d;
    }
  }
  return best;
}

// hitTest は船を先に見る（島の上に重なって描くため）。どちらにも当たらなければ null。
export function hitTest(scene: MapScene, p: Point): MapHit | null {
  const ferry = nearest(scene.ferries, p, MAP_STYLE.ferryHitRadius);
  if (ferry !== null) return { kind: "ferry", id: ferry.id };
  const island = nearest(scene.islands, p, MAP_STYLE.islandRadius + MAP_STYLE.islandHitSlop);
  if (island !== null) return { kind: "island", id: island.id };
  return null;
}
