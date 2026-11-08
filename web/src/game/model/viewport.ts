// 島の座標（ステージの layout）を画面の px に写す（T-404 / DesignDoc §13.3）。
// T-426: 島の配置の外接矩形を描画領域いっぱいに収め、島・船・封筒・文字を描画領域の大きさに合わせて拡大する。
import { MAP_STYLE } from "./style.ts";

export type Point = Readonly<{ x: number; y: number }>;
export type Size = Readonly<{ width: number; height: number }>;
export type MapTransform = Readonly<{ scale: number; offsetX: number; offsetY: number }>;
// 描画領域の四辺の余白（px）。
export type Insets = Readonly<{ top: number; right: number; bottom: number; left: number }>;
// transform は島の座標から画面の px への変換、markScale は島・船・封筒・文字の絵の拡大率（MAP_STYLE の px に掛ける）。
export type MapLayout = Readonly<{ transform: MapTransform; markScale: number }>;

// マップの構図の値（T-426）。画面のための値で、ゲームの数値ではない（ステージ定義に置かない）。
export const MAP_VIEW = {
  // 島の配置の拡大の上限。島が近すぎる配置でも、島どうしの間が空きすぎないよう頭打ちにする
  // （MAP_STYLE.maxScale の 1.5 では、大きな画面で島が真ん中の帯に小さく並んでいた）。
  maxScale: 4,
  // 島の配置の外接矩形の周りの余白（絵の拡大率 1 のときの px。絵の拡大率を掛けて使う）。
  // 左右は、端の島の右上に停泊した船とその名前が切れない幅（MAP_STYLE.padding の 56 では切れた）。
  // 上下は、島の上の役のマークと下の名札が切れない幅。
  paddingX: 84,
  paddingY: 60,
  // 下の余白に足す高さ（px。拡大しない）。マップの左下に重ねる島のボタンの列（MapView の .map-view__islands）に
  // 島と名札が隠れないようにする。
  bottomReserve: 44,
  // 絵の拡大率が 1 になる描画領域の大きさ（px）。拡大率は、描画領域の面積の比の平方根（縦と横の比の相乗平均）。
  // 横に長く縦に低いマップ（船を選んでパネルが伸びたとき）でも、縦だけで決めて絵が小さくなりすぎない。
  referenceWidth: 640,
  referenceHeight: 360,
  minMarkScale: 0.8, // 小さな画面でも、島と文字が読めなくならない下限
  maxMarkScale: 2, // プロジェクターなどの大きな画面でも、絵が大きくなりすぎない上限
  // 絵の拡大率 1 のとき、隣り合う島の中心どうしに要る間隔（px）。島の絵・名札・手紙の山が重ならない幅。
  // 島が近い配置では、この間隔に収まるまで絵の拡大率を下げる。
  islandSpacing: 130,
} as const;

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

function finiteOr(v: number, fallback: number): number {
  return Number.isFinite(v) ? v : fallback;
}

function insetsOf(padding: number | Insets): Insets {
  const side = (v: number): number => Math.max(0, finiteOr(v, MAP_STYLE.padding));
  if (typeof padding === "number") {
    const p = side(padding);
    return { top: p, right: p, bottom: p, left: p };
  }
  return {
    top: side(padding.top),
    right: side(padding.right),
    bottom: side(padding.bottom),
    left: side(padding.left),
  };
}

// fitIslands は島の外接矩形が余白の内側に収まり、その中心が余白の内側の中心に来る変換を返す。
// 縦横に同じ倍率を使う（縦横比を保つ）。画面が余白より小さいときは minScale で画面の中心に置く
// （例外を出さない・NaN を返さない）。島が 1 つ・すべて同じ座標のときは、外接矩形の幅と高さを 1 として扱う（0 で割らない）。
export function fitIslands(
  points: readonly Point[],
  size: Size,
  padding: number | Insets = MAP_STYLE.padding,
): MapTransform {
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
  const pad = insetsOf(padding);
  const width = finiteOr(size.width, 0);
  const height = finiteOr(size.height, 0);
  const availW = width - pad.left - pad.right;
  const availH = height - pad.top - pad.bottom;
  const fits = availW > 0 && availH > 0;
  const raw = fits ? Math.min(availW / w, availH / h) : MAP_STYLE.minScale;
  const scale = Number.isFinite(raw)
    ? clamp(raw, MAP_STYLE.minScale, MAP_VIEW.maxScale)
    : MAP_STYLE.minScale;
  const cx = (minX + maxX) / 2;
  const cy = (minY + maxY) / 2;
  const centerX = fits ? pad.left + availW / 2 : width / 2;
  const centerY = fits ? pad.top + availH / 2 : height / 2;
  return { scale, offsetX: centerX - cx * scale, offsetY: centerY - cy * scale };
}

// markScaleFor は描画領域の大きさだけで決まる絵の拡大率（minMarkScale〜maxMarkScale）。
export function markScaleFor(size: Size): number {
  const raw = Math.sqrt(
    (size.width / MAP_VIEW.referenceWidth) * (size.height / MAP_VIEW.referenceHeight),
  );
  if (!Number.isFinite(raw)) return 1;
  return clamp(raw, MAP_VIEW.minMarkScale, MAP_VIEW.maxMarkScale);
}

// mapInsets は絵の拡大率 k のときの、島の配置の外接矩形の周りの余白。
export function mapInsets(k: number): Insets {
  return {
    top: MAP_VIEW.paddingY * k,
    right: MAP_VIEW.paddingX * k,
    bottom: MAP_VIEW.paddingY * k + MAP_VIEW.bottomReserve,
    left: MAP_VIEW.paddingX * k,
  };
}

// 画面の上で、座標が違う島どうしのいちばん近い距離（違う座標の島が 2 つ未満なら null）。
function nearestGap(points: readonly Point[], t: MapTransform): number | null {
  let best: number | null = null;
  for (let i = 0; i < points.length; i++) {
    for (let j = i + 1; j < points.length; j++) {
      const a = points[i];
      const b = points[j];
      if (a === undefined || b === undefined) continue;
      const d = Math.hypot(a.x - b.x, a.y - b.y) * t.scale;
      if (d > 0 && Number.isFinite(d) && (best === null || d < best)) best = d;
    }
  }
  return best;
}

// layoutMap はマップの構図（島の座標の変換と絵の拡大率）を決める。
// 1. 描画領域の大きさから絵の拡大率を決め、余白（mapInsets）もその倍率で広げる（端の島の名札・船が切れない）
// 2. 島の配置の外接矩形を、その余白の内側に縦横比を保って収める
// 3. 島どうしが近くて名札・手紙の山が重なるなら、絵の拡大率を下げる（minMarkScale より下げない）
export function layoutMap(points: readonly Point[], size: Size): MapLayout {
  const base = markScaleFor(size);
  const transform = fitIslands(points, size, mapInsets(base));
  const gap = nearestGap(points, transform);
  const markScale =
    gap === null
      ? base
      : clamp(Math.min(base, gap / MAP_VIEW.islandSpacing), MAP_VIEW.minMarkScale, base);
  return { transform, markScale };
}

export function toScreen(t: MapTransform, p: Point): Point {
  return { x: p.x * t.scale + t.offsetX, y: p.y * t.scale + t.offsetY };
}
