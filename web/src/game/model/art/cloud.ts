// 嵐・予報の雲の輪郭（T-421）。3 つの楕円を合わせた形の外周を 1 つの多角形にする。
// 1 つの形として塗るので、半透明にしても楕円の重なりが濃く見えない。
// 楕円はどれも原点を含む凸な形なので、合わせた形は原点から見て星形になり、
// 原点からの各方向で最も遠い交点を結べば外周になる。
import type { Point } from "../viewport.ts";
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

type Ellipse = Readonly<{ x: number; y: number; rx: number; ry: number }>;

/** 雲を作る楕円（原点が雲の中心）。 */
export function cloudEllipses(): readonly Ellipse[] {
  return [-A.cloudSpread, 0, A.cloudSpread].map((dx) => ({
    x: dx,
    y: dx === 0 ? -A.cloudRadiusY / 2 : 0,
    rx: A.cloudRadiusX,
    ry: A.cloudRadiusY,
  }));
}

// 原点から向き (ux, uy) へ進む半直線と楕円の、遠い方の交点までの距離（交わらなければ 0）。
function farHit(e: Ellipse, ux: number, uy: number): number {
  const dx = -e.x;
  const dy = -e.y;
  const a = (ux / e.rx) ** 2 + (uy / e.ry) ** 2;
  const b = 2 * ((dx * ux) / e.rx ** 2 + (dy * uy) / e.ry ** 2);
  const c = (dx / e.rx) ** 2 + (dy / e.ry) ** 2 - 1;
  const disc = b * b - 4 * a * c;
  if (disc < 0) return 0;
  return Math.max(0, (-b + Math.sqrt(disc)) / (2 * a));
}

/** 雲の外周の頂点（原点が雲の中心・points 個）。 */
export function cloudOutline(points: number = A.cloudPoints): Point[] {
  const n = Math.max(8, Math.floor(points));
  const ellipses = cloudEllipses();
  const out: Point[] = [];
  for (let i = 0; i < n; i++) {
    const angle = (i / n) * 2 * Math.PI;
    const ux = Math.cos(angle);
    const uy = Math.sin(angle);
    let r = 0;
    for (const e of ellipses) r = Math.max(r, farHit(e, ux, uy));
    out.push({ x: ux * r, y: uy * r });
  }
  return out;
}
