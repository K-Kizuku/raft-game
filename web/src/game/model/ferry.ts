// 船の位置の補間（T-404 / FR-FERRY-08・DesignDoc §13.3）。
import type { FerryView } from "../../state/index.ts";
import type { Point } from "./viewport.ts";

// ferryProgress は航行の進み具合（0〜1）。到着の時刻を過ぎても ferry.arrived が届くまでは 1 で待つ。
export function ferryProgress(ferry: FerryView, simNow: number): number {
  if (ferry.state !== "sailing") return 1;
  const span = ferry.arriveAt - ferry.departAt;
  if (span <= 0) return 1;
  const t = (simNow - ferry.departAt) / span;
  return Math.min(1, Math.max(0, t));
}

export function lerp(a: Point, b: Point, t: number): Point {
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
}
