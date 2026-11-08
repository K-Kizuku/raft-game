// MapScene を PixiJS の表示物に写す薄い層（T-404 / DesignDoc §13.3・ADR-0010）。
// pixi.js を import するのはこのディレクトリだけ。MapView が動的 import で読み込む（最初の bundle に入れない）。
// 毎フレーム gameStore.getState() を読み、React の再描画を経由しない。ストアに書くのは選択だけ。
//
// 絵（T-421・docs/11-visual-design.md「温かい絵本・海図風」）: 水彩風の海・丸い島・海図の点線の航路・
// 郵便船・封筒・嵐の雲と雨。性能のため、毎フレーム（tick）は表示物の位置・回転・透明度・表示と、値が
// 変わった文字だけを更新する。Graphics を描き直すのは、鍵（形・色・選択の状態など）が変わったとき
// （updateRoute・updateIsland・updatePile・updateFerry の選択の輪・名札と積み荷の文字の幅）と、
// 画面の大きさが変わったとき（drawSea）だけ。
//
// 構図（T-426）: 島・手紙の山・船は、MAP_STYLE の px で描いた表示物を scene.markScale（描画領域の大きさに
// 合わせた絵の拡大率）で拡大する。文字は拡大してもぼやけないよう、拡大の上限の分だけ高い解像度で作る。
//
// 大きな画面（T-425）: 島の名前と船の文字は、画面の幅から決める倍率（labelScale）でさらに大きくする。
// 倍率は画面の大きさが変わったときだけ計算し、変わった表示物の scale と名札・積み荷の並びだけを直す。
import { Application, Container, Graphics, Text } from "pixi.js";

import { formatDuration } from "../../app/format.ts";
import { estimateSimTime } from "../../state/index.ts";
import type { GameStore } from "../../state/index.ts";
import { dashSegments } from "../model/dash.ts";
import { hitTest } from "../model/hitTest.ts";
import { buildRaftTags } from "../model/raftLabels.ts";
import { buildMapScene } from "../model/scene.ts";
import type { FerrySprite, IslandNode, LetterPile, MapScene, RouteLine } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import { smoothSimTime } from "../model/time.ts";
import { MAP_VIEW } from "../model/viewport.ts";
import type { Point, Size } from "../model/viewport.ts";
import { cloudOutline } from "../model/art/cloud.ts";
import { dotPositions, insetSegment } from "../model/art/dots.ts";
import { islandOutline, scaleOutline, smoothOutline } from "../model/art/islandShape.ts";
import { labelScale } from "../model/art/labelScale.ts";
import { bobOffset, rainOffset, seedOf, waveOffset } from "../model/art/motion.ts";
import { seaBlobs, waveMarks } from "../model/art/sea.ts";
import type { SeaTone } from "../model/art/sea.ts";
import { MAP_ART_STYLE } from "../model/art/style.ts";
import { createCommitEffectsLayer } from "./commitEffects.ts";
import { drawEnvelope } from "./envelope.ts";
import { createLetterEffectsLayer } from "./letterEffects.ts";
import { createPartitionLayer } from "./partition.ts";
import { createRaftOverlay } from "./raftOverlay.ts";

// now は performance.now。engineer はエンジニアモードを開いているか（T-712。省略なら常に偽）。
export type MapDeps = Readonly<{ store: GameStore; now: () => number; engineer?: () => boolean }>;
export type MapHandle = Readonly<{ destroy(): void }>;

const S = MAP_STYLE;
const D = MAP_STYLE.draw;
const A = MAP_ART_STYLE;

// 島の絵は当たり判定の円（islandRadius + islandHitSlop）の中に収める。浅瀬の輪がその円いっぱい。
const ISLAND_OUTER = S.islandRadius + S.islandHitSlop;

// 文字の解像度。絵の拡大率の上限と文字の倍率の上限まで拡大しても、画面の画素より粗くならないようにする。
function textResolution(): number {
  const dpr = typeof window === "undefined" ? 1 : window.devicePixelRatio;
  return (Number.isFinite(dpr) && dpr > 0 ? dpr : 1) * MAP_VIEW.maxMarkScale * A.labelScaleMax;
}

// 今の画面の幅での文字の倍率（画面の大きさが変わったときだけ呼ぶ）。
function currentLabelScale(): number {
  return typeof window === "undefined" ? labelScale(0) : labelScale(window.innerWidth);
}

// 絵の拡大率（buildMapScene はいつも入れる。無ければ 1）。
function markScaleOf(scene: MapScene): number {
  const k = scene.markScale ?? 1;
  return Number.isFinite(k) && k > 0 ? k : 1;
}

// 毎フレームの動きの入力（実時間と、動きを減らす設定）。
type Motion = Readonly<{ now: number; reduced: boolean }>;

type LabelOptions = Readonly<{
  size: number;
  font?: "body" | "number";
  halo?: boolean; // 紙の色の縁（海の上の文字）
  bold?: boolean;
  fill?: number;
}>;

function label(options: LabelOptions): Text {
  const halo = options.halo ?? false;
  const text = new Text({
    text: "",
    resolution: textResolution(),
    style: {
      fontFamily: options.font === "number" ? D.numberFontFamily : D.fontFamily,
      fontSize: options.size,
      fill: options.fill ?? S.colors.text,
      fontWeight: (options.bold ?? false) ? "bold" : "normal",
      ...(halo ? { stroke: { color: S.colors.halo, width: D.haloWidth } } : {}),
    },
  });
  text.anchor.set(0.5);
  return text;
}

// 絵文字（役のマーク・故障の 🔧）は縁を付けない。
function glyph(size: number): Text {
  return label({ size });
}

// 値が変わったときだけ文字を変え、変わったかどうかを返す。
function setText(text: Text, value: string): boolean {
  if (text.text === value) return false;
  text.text = value;
  return true;
}

// 破線の円（候補の輪）。線の形で選択（実線）と区別する（NFR-10）。
function dashedCircle(g: Graphics, r: number): void {
  const step = D.candidateDash + D.candidateDashGap;
  const count = Math.max(1, Math.floor((2 * Math.PI * r) / step));
  const unit = (2 * Math.PI) / count;
  const arc = unit * (D.candidateDash / step);
  for (let i = 0; i < count; i++) {
    const start = i * unit;
    g.moveTo(Math.cos(start) * r, Math.sin(start) * r);
    g.arc(0, 0, r, start, start + arc);
  }
  g.stroke({ width: D.candidateRingWidth, color: S.colors.candidate });
}

// 頂点を制御点にした、なめらかな閉じた曲線。
function smoothShape(g: Graphics, points: readonly Point[]): Graphics {
  const { start, curves } = smoothOutline(points);
  g.moveTo(start.x, start.y);
  for (const [c, p] of curves) g.quadraticCurveTo(c.x, c.y, p.x, p.y);
  g.closePath();
  return g;
}

// --- 海 -----------------------------------------------------------------------------------------

type SeaView = { blobs: Graphics; waves: Container; waveLines: Graphics; size: Size };

const BLOB_COLOR: Record<SeaTone, number> = {
  light: S.colors.seaLight,
  mid: S.colors.seaMidBlob,
  deep: S.colors.seaDeep,
};

function createSea(layers: Layers): SeaView {
  const blobs = new Graphics();
  const waves = new Container();
  const waveLines = new Graphics();
  waves.addChild(waveLines);
  layers.sea.addChild(blobs, waves);
  return { blobs, waves, waveLines, size: { width: -1, height: -1 } };
}

// 画面の大きさが変わったときだけ呼ぶ。にじみと波の記号の位置は大きさだけで決まる。
function drawSea(view: SeaView, size: Size): void {
  view.size = { width: size.width, height: size.height };
  const { blobs, waveLines } = view;
  blobs.clear();
  for (const blob of seaBlobs(size)) {
    // 半径を少しずつ小さくした円を重ね、縁のぼけた水彩のにじみにする。
    for (let k = 0; k < A.seaBlobRings; k++) {
      blobs.circle(blob.x, blob.y, blob.r * (1 - k * A.seaBlobRingShrink));
      blobs.fill({ color: BLOB_COLOR[blob.tone], alpha: A.seaBlobAlpha[blob.tone] });
    }
  }
  waveLines.clear();
  // 海図の波の記号「〜」（上下に交互にふくらむ短い曲線）。
  const half = A.waveHalfLength;
  for (const mark of waveMarks(size)) {
    let x = mark.x - (A.waveHalves * half) / 2;
    waveLines.moveTo(x, mark.y);
    for (let j = 0; j < A.waveHalves; j++) {
      const up = j % 2 === 0 ? -1 : 1;
      waveLines.quadraticCurveTo(x + half / 2, mark.y + up * 2 * A.waveHeight, x + half, mark.y);
      x += half;
    }
  }
  waveLines.stroke({ width: A.waveWidth, color: S.colors.wave, alpha: A.waveAlpha, cap: "round" });
}

// --- 航路と嵐 -----------------------------------------------------------------------------------

type RouteView = {
  line: Graphics;
  storm: Container;
  cloud: Graphics;
  rainWrap: Container;
  rain: Graphics;
  rainMask: Graphics;
  rainDrawn: boolean;
  key: string;
};

// 雨の範囲（嵐の Container の中。原点は航路の中点）。
const RAIN_TOP = -A.cloudLift + A.cloudRadiusY * A.rainTopRatio;
const RAIN_HALF_WIDTH = A.cloudSpread + A.cloudRadiusX * A.rainSpreadRatio;

function createRoute(layers: Layers): RouteView {
  const line = new Graphics();
  const storm = new Container();
  const cloud = new Graphics();
  const rainWrap = new Container();
  const rain = new Graphics();
  const rainMask = new Graphics();
  rainWrap.addChild(rain);
  rainWrap.mask = rainMask;
  storm.addChild(rainWrap, rainMask, cloud);
  storm.visible = false;
  layers.routes.addChild(line);
  layers.storms.addChild(storm);
  return {
    line,
    storm,
    cloud,
    rainWrap,
    rain,
    rainMask,
    rainDrawn: false,
    key: "",
  };
}

// 雨の線（嵐ごとに 1 回だけ描く）。縦に rainPeriod で繰り返す模様にし、毎フレームは y を動かして巻き戻すだけ。
function drawRain(view: RouteView): void {
  view.rainDrawn = true;
  const height = -RAIN_TOP;
  const rows = Math.ceil(height / A.rainPeriod) + 1;
  const width = 2 * RAIN_HALF_WIDTH;
  const g = view.rain;
  let lines = 0;
  for (let row = 0; row < rows && lines < A.rainMaxLines; row++) {
    for (let i = 0; i < A.rainPerPeriod && lines < A.rainMaxLines; i++) {
      const x = -RAIN_HALF_WIDTH + ((i + 0.5) * width) / A.rainPerPeriod;
      const stagger = (((i * 3) % A.rainPerPeriod) / A.rainPerPeriod) * A.rainPeriod;
      const y = RAIN_TOP - A.rainPeriod + row * A.rainPeriod + stagger;
      g.moveTo(x + A.rainSlant / 2, y).lineTo(x - A.rainSlant / 2, y + A.rainLength);
      lines++;
    }
  }
  g.stroke({ width: A.rainWidth, color: S.colors.rain, alpha: A.rainAlpha, cap: "round" });
  view.rainMask
    .rect(-RAIN_HALF_WIDTH - A.rainSlant, RAIN_TOP, width + 2 * A.rainSlant, height)
    .fill({ color: S.colors.rain });
}

// 雲の外周（楕円を合わせた 1 つの形）を、中点から cloudLift だけ上に描く。1 つの形なので半透明でも重なりが出ない。
const CLOUD_OUTLINE = cloudOutline();

function cloudShape(g: Graphics, dy: number): Graphics {
  return g.poly(CLOUD_OUTLINE.map((p) => ({ x: p.x, y: p.y - A.cloudLift + dy })));
}

// k は絵の拡大率（線の太さ・点の大きさと間隔・島の絵の分だけ縮める長さ・雲に掛ける）。
function updateRoute(view: RouteView, r: RouteLine, k: number): void {
  const key = `${r.a.x},${r.a.y},${r.b.x},${r.b.y},${r.storm ?? ""},${k}`;
  if (view.key === key) return;
  view.key = key;
  const { line, cloud } = view;
  line.clear();
  if (r.storm === "blocked") {
    // 通れない航路: 長い破線と ✕ の印（色だけでなく形で区別する。NFR-10）。
    const seg = insetSegment(r.a, r.b, ISLAND_OUTER * k);
    if (seg !== null) {
      for (const [p, q] of dashSegments(seg[0], seg[1], A.blockedDash * k, A.blockedGap * k)) {
        line.moveTo(p.x, p.y).lineTo(q.x, q.y);
      }
      line.stroke({ width: A.blockedWidth * k, color: S.colors.routeBlocked, cap: "round" });
    }
    const c = A.crossSize * k;
    line
      .moveTo(r.mid.x - c, r.mid.y - c)
      .lineTo(r.mid.x + c, r.mid.y + c)
      .moveTo(r.mid.x + c, r.mid.y - c)
      .lineTo(r.mid.x - c, r.mid.y + c);
    line.stroke({ width: (A.crossWidth + A.crossHalo) * k, color: S.colors.halo, cap: "round" });
    line
      .moveTo(r.mid.x - c, r.mid.y - c)
      .lineTo(r.mid.x + c, r.mid.y + c)
      .moveTo(r.mid.x + c, r.mid.y - c)
      .lineTo(r.mid.x - c, r.mid.y + c);
    line.stroke({ width: A.crossWidth * k, color: S.colors.routeBlocked, cap: "round" });
  } else {
    // 海図風の点線（丸い点を等間隔）。
    for (const p of dotPositions(r.a, r.b, A.dotSpacing * k, ISLAND_OUTER * k)) {
      line.circle(p.x, p.y, A.dotRadius * k);
    }
    line.fill({ color: S.colors.route });
  }
  view.storm.position.set(r.mid.x, r.mid.y);
  view.storm.scale.set(k);
  cloud.clear();
  if (r.storm === "blocked") {
    // 暗い雲（下に濃い雲を重ねる）と、雲から航路へ降る雨。
    cloudShape(cloud, A.cloudShadowDrop).fill({ color: S.colors.cloudDeep });
    cloudShape(cloud, 0).fill({ color: S.colors.cloud });
    if (!view.rainDrawn) drawRain(view);
    cloud.alpha = 1;
    view.rainWrap.visible = true;
    view.storm.visible = true;
  } else if (r.storm === "forecast") {
    // 予報: 薄い灰色の雲だけ（雨なし・半透明）。
    cloudShape(cloud, 0).fill({ color: S.colors.forecastCloud });
    cloud.alpha = A.forecastAlpha;
    view.rainWrap.visible = false;
    view.storm.visible = true;
  } else {
    view.rainWrap.visible = false;
    view.storm.visible = false;
  }
}

// 毎フレーム: 雨の模様を下へずらし、rainPeriod で巻き戻す（動きを減らす設定では止める）。
function animateRoute(view: RouteView, motion: Motion): void {
  if (!view.storm.visible || !view.rainWrap.visible) return;
  view.rain.y = rainOffset(motion.now, A.rainPeriod, motion.reduced);
}

function destroyRoute(view: RouteView): void {
  view.line.destroy();
  view.storm.destroy({ children: true });
}

// --- 島 -----------------------------------------------------------------------------------------

type IslandView = {
  root: Container;
  body: Graphics;
  tag: Graphics;
  name: Text;
  mark: Text;
  key: string;
};

function createIsland(layers: Layers): IslandView {
  const root = new Container();
  const body = new Graphics();
  const tag = new Graphics();
  tag.y = S.islandRadius + D.labelGap;
  const name = label({ size: D.labelFontSize, bold: true, fill: S.colors.nameTagStroke });
  name.anchor.set(0.5, 0);
  name.y = tag.y + A.nameTagPadY;
  const mark = glyph(D.markFontSize);
  mark.anchor.set(0.5, 1);
  mark.y = -S.islandRadius;
  root.addChild(body, tag, name, mark);
  layers.islands.addChild(root);
  return { root, body, tag, name, mark, key: "" };
}

const ROLE_GLYPH: Record<NonNullable<IslandNode["roleMark"]>, string> = {
  leader: "👑",
  leader_stale: "👑",
  candidate: "📣",
  pre_candidate: "💭",
};

// 島の形・浅瀬・砂浜・港（桟橋と 〒）と、選択・候補の輪。鍵が変わったときだけ呼ぶ。
function drawIsland(g: Graphics, island: IslandNode): void {
  const R = S.islandRadius;
  const outline = islandOutline(island.id, R);
  g.clear();
  smoothShape(g, scaleOutline(outline, ISLAND_OUTER / R)).fill({
    color: S.colors.shallow,
    alpha: A.shallowAlpha,
  });
  if (island.candidate) dashedCircle(g, R + D.candidateRingGap);
  if (island.selected) {
    g.circle(0, 0, R + D.selectedRingGap);
    g.stroke({ width: D.selectedRingWidth, color: S.colors.selected });
  }
  // 桟橋（停泊する船が並ぶ右上へ。根元は砂浜の下に隠れる）。
  const ux = Math.cos(A.pierAngle);
  const uy = Math.sin(A.pierAngle);
  const pierEnd = ISLAND_OUTER - A.pierWidth / 2;
  g.moveTo(ux * R * A.pierFrom, uy * R * A.pierFrom).lineTo(ux * pierEnd, uy * pierEnd);
  g.stroke({ width: A.pierWidth, color: S.colors.pier, cap: "round" });
  for (const d of [R + A.pierWidth / 2, pierEnd]) {
    g.circle(ux * d, uy * d + A.pierWidth / 2, A.pierPostRadius);
  }
  g.fill({ color: S.colors.islandStroke });
  // 砂浜の縁と、島の色の部分。
  smoothShape(g, outline)
    .fill({ color: S.colors.sand })
    .stroke({ width: D.islandStrokeWidth, color: S.colors.islandStroke });
  smoothShape(g, scaleOutline(outline, (R - A.sandWidth) / R))
    .fill({ color: island.color })
    .stroke({
      width: A.innerStrokeWidth,
      color: S.colors.islandStroke,
      alpha: A.innerStrokeAlpha,
    });
  // 郵便の印（〒）。どの島の色の上でも読めるよう、紙の色の丸に載せる。
  const mx = Math.cos(-Math.PI / 4) * R * A.postMarkAt;
  const my = Math.sin(-Math.PI / 4) * R * A.postMarkAt;
  const b = A.postMarkBar;
  g.circle(mx, my, A.postMarkRadius)
    .fill({ color: S.colors.postMark })
    .stroke({ width: A.postMarkStroke, color: S.colors.postal });
  g.moveTo(mx - b, my - b * 0.7)
    .lineTo(mx + b, my - b * 0.7)
    .moveTo(mx - b, my - b * 0.1)
    .lineTo(mx + b, my - b * 0.1)
    .moveTo(mx, my - b * 0.1)
    .lineTo(mx, my + b);
  g.stroke({ width: A.postMarkStroke, color: S.colors.postal });
}

// 紙の色の角丸の名札。名前が変わったときだけ大きさを測って描き直す。
function drawNameTag(view: IslandView): void {
  const w = view.name.width + 2 * A.nameTagPadX;
  const h = view.name.height + 2 * A.nameTagPadY;
  view.tag.clear();
  view.tag.visible = view.name.text !== "";
  view.tag
    .roundRect(-w / 2, 0, w, h, A.nameTagRadius)
    .fill({ color: S.colors.nameTag })
    .stroke({ width: A.nameTagStrokeWidth, color: S.colors.nameTagStroke });
}

function updateIsland(view: IslandView, island: IslandNode, k: number, ls: number): void {
  view.root.position.set(island.at.x, island.at.y);
  view.root.scale.set(k);
  const key = `${island.id}|${island.color}|${island.selected}|${island.candidate}`;
  if (view.key !== key) {
    view.key = key;
    drawIsland(view.body, island);
  }
  // 文字の倍率が変わったとき（画面の大きさが変わったとき）だけ、名前を拡大して名札を描き直す。
  const rescaled = view.name.scale.x !== ls;
  if (rescaled) view.name.scale.set(ls);
  if (setText(view.name, island.name) || rescaled) drawNameTag(view);
  setText(view.mark, island.roleMark === null ? "" : ROLE_GLYPH[island.roleMark]);
  view.mark.alpha = island.roleMark === "leader_stale" ? D.staleLeaderAlpha : 1;
}

// --- 手紙の山 -----------------------------------------------------------------------------------

type PileView = { root: Container; envelope: Graphics; count: Text; star: Text; key: string };

function createPile(layers: Layers): PileView {
  const root = new Container();
  const envelope = new Graphics();
  // 数のバッジは右下（右上は宛先の島の色の切手）。
  const badge = new Graphics()
    .circle(D.envelopeWidth / 2, D.envelopeHeight / 2, D.badgeRadius)
    .fill({ color: S.colors.badge })
    .stroke({ width: D.envelopeStrokeWidth, color: S.colors.badgeStroke });
  const count = label({ size: D.badgeFontSize, font: "number", bold: true });
  count.position.set(D.envelopeWidth / 2, D.envelopeHeight / 2);
  const star = new Text({
    text: "★",
    resolution: textResolution(),
    style: {
      fontFamily: D.fontFamily,
      fontSize: D.badgeFontSize,
      fill: S.colors.star,
      fontWeight: "bold",
      stroke: { color: S.colors.starStroke, width: A.starStrokeWidth },
    },
  });
  star.anchor.set(0.5);
  star.position.set(-D.envelopeWidth / 2, -D.envelopeHeight / 2);
  root.addChild(envelope, badge, count, star);
  layers.piles.addChild(root);
  return { root, envelope, count, star, key: "" };
}

function updatePile(view: PileView, pile: LetterPile, k: number): void {
  view.root.position.set(pile.at.x, pile.at.y);
  view.root.scale.set(k);
  const layers = pile.count >= D.envelopeLayers ? D.envelopeLayers : Math.max(1, pile.count);
  const key = `${pile.color}|${layers}`;
  if (view.key !== key) {
    view.key = key;
    const g = view.envelope;
    g.clear();
    for (let i = layers - 1; i >= 0; i--) {
      const shift = i * D.envelopeShift;
      drawEnvelope(g, { color: pile.color, x: shift, y: -shift });
    }
  }
  setText(view.count, String(pile.count));
  view.star.visible = pile.hasRequest;
}

// --- 船 -----------------------------------------------------------------------------------------

type FerryView = {
  root: Container;
  ring: Graphics;
  hull: Container; // 揺れ（y・rotation）
  ship: Graphics; // 郵便船のシルエット（左右の反転は scale.x）
  name: Text;
  cargoIcon: Graphics;
  cargo: Text;
  eta: Text;
  broken: Text;
  selected: boolean | null;
  seed: number;
};

// 郵便船のシルエット（右向き・中心が原点）。船ごとに 1 回だけ描く。
function drawShip(g: Graphics): Graphics {
  const w = A.shipStrokeWidth;
  // マストと郵便の赤の旗（後ろへなびく）。
  g.moveTo(A.mastX, A.cabinTop)
    .lineTo(A.mastX, A.mastTop)
    .stroke({ width: w, color: S.colors.shipStroke, cap: "round" });
  g.poly([
    A.mastX,
    A.mastTop,
    A.mastX - A.flagLength,
    A.mastTop + A.flagHeight / 2,
    A.mastX,
    A.mastTop + A.flagHeight,
  ])
    .fill({ color: S.colors.postal })
    .stroke({ width: w * 0.7, color: S.colors.shipStroke, join: "round" });
  // 船室。
  g.rect(A.cabinLeft, A.cabinTop, A.cabinRight - A.cabinLeft, A.hullTop - A.cabinTop)
    .fill({ color: S.colors.cabin })
    .stroke({ width: w, color: S.colors.shipStroke });
  // 船体（船首が少し反る）。
  g.poly([
    -A.hullHalf,
    A.hullTop,
    A.hullHalf,
    A.hullTop - A.hullBowRise,
    A.hullHalf - A.hullBowRise - 1,
    A.hullBottom,
    -A.hullHalf + 2,
    A.hullBottom,
  ])
    .fill({ color: S.colors.hull })
    .stroke({ width: w, color: S.colors.shipStroke, join: "round" });
  // 船体の封筒の印。
  const ew = A.hullEnvelopeWidth;
  const eh = A.hullEnvelopeHeight;
  const ex = -ew / 2 - 1;
  const ey = (A.hullTop + A.hullBottom) / 2 - eh / 2;
  g.rect(ex, ey, ew, eh).fill({ color: S.colors.hullEnvelope });
  g.moveTo(ex, ey)
    .lineTo(ex + ew / 2, ey + eh * 0.6)
    .lineTo(ex + ew, ey)
    .stroke({ width: 1, color: S.colors.hullEnvelopeFlap });
  return g;
}

function createFerry(layers: Layers, id: string): FerryView {
  const root = new Container();
  const ring = new Graphics();
  const hull = new Container();
  const ship = drawShip(new Graphics());
  hull.addChild(ship);
  const name = label({ size: D.ferryLabelFontSize, bold: true, halo: true });
  name.anchor.set(0.5, 1);
  name.y = -D.ferryRingRadius;
  const cargoIcon = drawEnvelope(new Graphics(), {
    color: S.colors.postal,
    width: A.cargoIconWidth,
    height: A.cargoIconHeight,
  });
  const cargo = label({ size: D.ferryLabelFontSize, font: "number", bold: true, halo: true });
  cargo.anchor.set(0, 0);
  cargo.y = D.ferryRingRadius;
  cargoIcon.y = D.ferryRingRadius + D.ferryLineHeight / 2;
  const eta = label({ size: D.ferryLabelFontSize, font: "number", bold: true, halo: true });
  eta.anchor.set(0.5, 0);
  eta.y = D.ferryRingRadius + D.ferryLineHeight;
  const broken = glyph(D.brokenFontSize);
  broken.position.set(D.ferryRingRadius, -D.ferryRingRadius / 2);
  broken.text = "🔧";
  root.addChild(ring, hull, name, cargoIcon, cargo, eta, broken);
  layers.ferries.addChild(root);
  return {
    root,
    ring,
    hull,
    ship,
    name,
    cargoIcon,
    cargo,
    eta,
    broken,
    selected: null,
    seed: seedOf(id),
  };
}

// 積み荷の封筒の印と「3/5」を、まとめて船の真下にそろえる（文字が変わったときだけ）。
function layoutCargo(view: FerryView): void {
  const total = A.cargoIconWidth + A.cargoIconGap + view.cargo.width;
  const left = -total / 2;
  view.cargoIcon.x = left + A.cargoIconWidth / 2;
  view.cargo.x = left + A.cargoIconWidth + A.cargoIconGap;
}

// 船の文字（名前・積み荷・到着まで）を文字の倍率で拡大し、行の間隔もその分だけ広げる。
function scaleFerryText(view: FerryView, ls: number): void {
  for (const text of [view.name, view.cargo, view.eta]) text.scale.set(ls);
  view.cargoIcon.y = D.ferryRingRadius + (D.ferryLineHeight * ls) / 2;
  view.eta.y = D.ferryRingRadius + D.ferryLineHeight * ls;
}

function updateFerry(
  view: FerryView,
  ferry: FerrySprite,
  motion: Motion,
  k: number,
  ls: number,
): void {
  view.root.position.set(ferry.at.x, ferry.at.y);
  view.root.scale.set(k);
  // 文字の倍率が変わったとき（画面の大きさが変わったとき）だけ、文字の大きさと並びを直す。
  const rescaled = view.name.scale.x !== ls;
  if (rescaled) scaleFerryText(view, ls);
  if (view.selected !== ferry.selected) {
    view.selected = ferry.selected;
    view.ring.clear();
    if (ferry.selected) {
      view.ring.circle(0, 0, D.ferryRingRadius);
      view.ring.stroke({ width: D.ferryRingWidth, color: S.colors.ferryRing });
    }
  }
  // 左へ向かうときはシルエットだけを左右反転する（文字は反転しない）。
  const facing = Math.cos(ferry.heading) < 0 ? -1 : 1;
  if (view.ship.scale.x !== facing) view.ship.scale.x = facing;
  // 航行中だけ小さく揺らす（動きを減らす設定では揺らさない）。
  const bob =
    ferry.state === "sailing"
      ? bobOffset(motion.now, view.seed, motion.reduced)
      : { y: 0, rotation: 0 };
  view.hull.y = bob.y;
  view.hull.rotation = bob.rotation;
  setText(view.name, ferry.name);
  if (setText(view.cargo, `${ferry.cargoCount}/${ferry.capacity}`) || rescaled) layoutCargo(view);
  const eta =
    ferry.state === "sailing" && ferry.etaMs !== null
      ? `あと ${formatDuration(Math.ceil(ferry.etaMs / 1000) * 1000)}`
      : "";
  setText(view.eta, eta);
  view.broken.visible = ferry.broken;
}

// --- 全体 ---------------------------------------------------------------------------------------

type Layers = Readonly<{
  sea: Container;
  routes: Container;
  storms: Container;
  partition: Container; // 分断のグループの縁取り（T-704、./partition.ts）
  islands: Container;
  piles: Container;
  ferries: Container;
  overlay: Container; // エンジニアモードのメッセージ種別の札（T-712、./raftOverlay.ts）
  celebrations: Container; // 依頼の確定の光る線と紙吹雪（T-435、./commitEffects.ts）
  effects: Container;
}>;

// syncViews は scene の並びに合わせて、ID ごとの表示物を使い回す（無くなったものだけ壊し、新しいものだけ作る）。
function syncViews<K, V, T>(
  views: Map<K, V>,
  items: readonly T[],
  keyOf: (item: T) => K,
  create: (key: K) => V,
  update: (view: V, item: T) => void,
  destroy: (view: V) => void,
): void {
  const seen = new Set<K>();
  for (const item of items) {
    const key = keyOf(item);
    seen.add(key);
    let view = views.get(key);
    if (view === undefined) {
      view = create(key);
      views.set(key, view);
    }
    update(view, item);
  }
  for (const [key, view] of views) {
    if (!seen.has(key)) {
      destroy(view);
      views.delete(key);
    }
  }
}

// 当たり判定（hitTest）の半径は MAP_STYLE の px なので、絵の拡大率 k で拡大した絵に合わせるため、
// 島と船の位置とクリックの位置を k で割って判定する（距離が 1/k になり、半径を k 倍したのと同じ）。
function hitScene(scene: MapScene): MapScene {
  const k = markScaleOf(scene);
  if (k === 1) return scene;
  const shrink = (p: Point): Point => ({ x: p.x / k, y: p.y / k });
  return {
    ...scene,
    islands: scene.islands.map((i) => ({ ...i, at: shrink(i.at) })),
    ferries: scene.ferries.map((f) => ({ ...f, at: shrink(f.at) })),
  };
}

export async function mountMap(host: HTMLElement, deps: MapDeps): Promise<MapHandle> {
  const app = new Application();
  try {
    await app.init({
      resizeTo: host,
      antialias: true,
      autoDensity: true,
      resolution: window.devicePixelRatio,
      background: S.colors.sea,
    });
  } catch (error) {
    try {
      app.destroy({ removeView: true }, { children: true });
    } catch {
      // 作りかけの Application は壊せないことがある（renderer が無い）。元の失敗を返す。
    }
    throw error;
  }
  host.appendChild(app.canvas);

  // 下から sea・routes・storms・partition・islands・piles・ferries・overlay・celebrations・effects。overlay はエンジニアモードの札。海は地の色（background）の上に、
  // にじみと波の記号を描く。celebrations は依頼の確定の光る線と紙吹雪（T-435、./commitEffects.ts）、
  // effects は手紙の処分・差し替えのアニメーション（T-410、./letterEffects.ts）を描く。
  const layers: Layers = {
    sea: new Container(),
    routes: new Container(),
    storms: new Container(),
    partition: new Container(),
    islands: new Container(),
    piles: new Container(),
    ferries: new Container(),
    overlay: new Container(),
    celebrations: new Container(),
    effects: new Container(),
  };
  app.stage.addChild(
    layers.sea,
    layers.routes,
    layers.storms,
    layers.partition,
    layers.islands,
    layers.piles,
    layers.ferries,
    layers.overlay,
    layers.celebrations,
    layers.effects,
  );
  // 動きを減らす設定。マップの動き（波・雨・船の揺れ）は設定の変化に追従する。
  const motionQuery = window.matchMedia?.("(prefers-reduced-motion: reduce)") ?? null;
  let reducedMotion = motionQuery?.matches ?? false;
  const onMotionChange = (event: MediaQueryListEvent): void => {
    reducedMotion = event.matches;
  };
  motionQuery?.addEventListener?.("change", onMotionChange);
  const effects = createLetterEffectsLayer(layers.effects, { reducedMotion });
  // 確定の演出は、動きを減らす設定の変化にも追従する（毎フレーム読む）。
  const celebrations = createCommitEffectsLayer(layers.celebrations, {
    reducedMotion: () => reducedMotion,
  });
  const partition = createPartitionLayer(layers.partition);
  const overlay = createRaftOverlay(layers.overlay);

  const sea = createSea(layers);
  const routeViews = new Map<string, RouteView>();
  const islandViews = new Map<number, IslandView>();
  const pileViews = new Map<string, PileView>();
  const ferryViews = new Map<string, FerryView>();
  let shown: number | null = null;
  let lastScene: MapScene | null = null;
  let textScale = currentLabelScale();

  const tick = (): void => {
    const now = deps.now();
    const motion: Motion = { now, reduced: reducedMotion };
    const size = { width: app.screen.width, height: app.screen.height };
    if (size.width !== sea.size.width || size.height !== sea.size.height) {
      drawSea(sea, size);
      textScale = currentLabelScale();
    }
    sea.waves.x = waveOffset(now, reducedMotion);

    const { world, selection } = deps.store.getState();
    if (world === null) return;
    shown = smoothSimTime(shown, estimateSimTime(world.clock, now));
    const scene = buildMapScene(world, selection, shown, size);
    lastScene = scene;
    const k = markScaleOf(scene);
    syncViews(
      routeViews,
      scene.routes,
      (r) => r.id,
      () => createRoute(layers),
      (v, r) => {
        updateRoute(v, r, k);
        animateRoute(v, motion);
      },
      destroyRoute,
    );
    partition.update(scene.partition);
    syncViews(
      islandViews,
      scene.islands,
      (i) => i.id,
      () => createIsland(layers),
      (v, i) => {
        updateIsland(v, i, k, textScale);
      },
      (v) => v.root.destroy({ children: true }),
    );
    syncViews(
      pileViews,
      scene.piles,
      (p) => `${p.islandId}:${p.to}`,
      () => createPile(layers),
      (v, p) => {
        updatePile(v, p, k);
      },
      (v) => v.root.destroy({ children: true }),
    );
    syncViews(
      ferryViews,
      scene.ferries,
      (f) => f.id,
      (id) => createFerry(layers, id),
      (v, f) => {
        updateFerry(v, f, motion, k, textScale);
      },
      (v) => v.root.destroy({ children: true }),
    );
    overlay.update(deps.engineer?.() === true ? buildRaftTags(world, scene) : null);
    celebrations.update(world, scene, now);
    effects.update(world, scene, now, shown);
  };

  const onPointerDown = (event: PointerEvent): void => {
    if (lastScene === null) return;
    const rect = app.canvas.getBoundingClientRect();
    const sx = rect.width > 0 ? app.screen.width / rect.width : 1;
    const sy = rect.height > 0 ? app.screen.height / rect.height : 1;
    const hit = hitTest(hitScene(lastScene), {
      x: ((event.clientX - rect.left) * sx) / markScaleOf(lastScene),
      y: ((event.clientY - rect.top) * sy) / markScaleOf(lastScene),
    });
    if (hit === null) return;
    const state = deps.store.getState();
    if (hit.kind === "ferry") state.selectFerry(hit.id);
    else state.selectIsland(hit.id);
  };

  app.ticker.add(tick);
  app.canvas.addEventListener("pointerdown", onPointerDown);
  // resizeTo は window の resize しか見ないので、マップの領域だけが変わったとき（パネルの中身が増えて
  // マップの行が縮んだときなど）も、次のフレームで canvas の大きさを合わせる（T-426）。
  // 大きさが変わると、次の tick で構図（buildMapScene の layoutMap）と海が作り直される。
  const resizeObserver =
    typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver(() => {
          app.queueResize();
        });
  resizeObserver?.observe(host);

  let destroyed = false;
  return {
    destroy() {
      if (destroyed) return;
      destroyed = true;
      app.canvas.removeEventListener("pointerdown", onPointerDown);
      resizeObserver?.disconnect();
      app.ticker.remove(tick);
      motionQuery?.removeEventListener?.("change", onMotionChange);
      routeViews.clear();
      islandViews.clear();
      pileViews.clear();
      ferryViews.clear();
      effects.destroy();
      celebrations.destroy();
      partition.destroy();
      overlay.destroy();
      app.destroy({ removeView: true }, { children: true });
    },
  };
}
