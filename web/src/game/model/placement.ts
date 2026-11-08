// マップの札（島の名札・手紙の山・船とその文字）の位置を決める純粋な関数（T-428）。
// どの札も、決まった順の候補の位置から、すでに置いた札と重ならない最初のものを選ぶ（どれも重なるなら、
// 重なる面積がいちばん小さいもの）。表示する情報は減らさず、位置だけをずらす。
//
// 置く順（先に置いたものは、あとのものに押されて動かない）:
//   1. 島の絵と役のマーク（動かさない）
//   2. 島の名札（下 → 右 → 左 → 上）
//   3. 港にある手紙の山。置き場は島の配置だけで決まり（宛先の島ごとに 1 つ。ほかの置き場とも重ならない）、
//      山が増えても減っても動かない。山がある置き場だけを、あとの札が避ける
//   4. 宛先の島が分からない山
//   5. 停泊中の船（同じ港の船は、島の周りに等間隔に並べる。並びの半径と向き・文字の置き方を候補から選ぶ。
//      一周に並べるとどうしても重なるときだけ、島の周りの弧に等間隔に並べる）
//   6. 航行中の船の文字（船の位置は航路の上で動かさず、文字の置き方だけを選ぶ）
//   7. 手紙の処分・差し替えの文字の札（placeCaptions。島ごと・種類ごとに 1 つにまとめたものを、島の周りの円の
//      候補から選ぶ。1〜5 を避ける。../animations/letterEffects.ts の planCaptions が呼ぶ）
//
// 長さはすべて画面の px（絵の拡大率 k と文字の倍率 ls を掛けたあと）。
// 1〜3 と 4・5 は直前の入力と同じなら直前の結果を使い回す（毎フレーム呼ばれるため。結果は入力だけで決まる）。
import { MAP_ART_STYLE } from "./art/style.ts";
import { MAP_STYLE } from "./style.ts";
import type { Point, Size } from "./viewport.ts";
import { MAP_VIEW } from "./viewport.ts";

const S = MAP_STYLE;
const D = MAP_STYLE.draw;
const P = MAP_STYLE.placement;
const A = MAP_ART_STYLE;

const EPSILON = 1e-6;

export type Box = Readonly<{ left: number; top: number; right: number; bottom: number }>;
// owner は重なりを数えないときの目印（停泊中の船の絵は、自分の島の絵と重なってよい）。
export type Placed = Readonly<{ box: Box; owner: string }>;

export type Candidate<T> = Readonly<{ value: T; boxes: readonly Box[] }>;

// 船の文字（名前・積み荷・到着まで）の置き方。split は名前を船の上、積み荷と到着までを船の下（今までの形）。
// right・left は船の横に縦に並べ、-up・-down はその並びを船の半分の高さだけ上・下にずらす。
export type FerryLabels =
  | "split"
  | "right"
  | "left"
  | "above"
  | "below"
  | "right-up"
  | "right-down"
  | "left-up"
  | "left-down";
export const FERRY_LABELS: readonly FerryLabels[] = [
  "split",
  "right",
  "left",
  "above",
  "below",
  "right-up",
  "right-down",
  "left-up",
  "left-down",
];

export function overlapArea(a: Box, b: Box): number {
  const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
  const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
  // 辺が接するだけ（計算の誤差で、わずかに重なるものを含む）は重ならない。
  return w > EPSILON && h > EPSILON ? w * h : 0;
}

function area(b: Box): number {
  return Math.max(0, b.right - b.left) * Math.max(0, b.bottom - b.top);
}

function outside(b: Box, bounds: Box): number {
  const rest = area(b) - overlapArea(b, bounds);
  return rest > EPSILON ? rest : 0;
}

// boxes が placed と重なる面積と、bounds の外にはみ出す面積の和。ignore の owner の箱とは重なりを数えない。
export function placementCost(
  boxes: readonly Box[],
  placed: readonly Placed[],
  bounds: Box | null,
  ignore: string | null = null,
): number {
  let cost = 0;
  for (const box of boxes) {
    for (const p of placed) {
      if (ignore !== null && p.owner === ignore) continue;
      cost += overlapArea(box, p.box);
    }
    if (bounds !== null) cost += outside(box, bounds);
  }
  return cost;
}

// pickCandidate は、placed と重ならず bounds からはみ出さない最初の候補を返す。どれも重なるなら、
// 重なりがいちばん小さい候補（同じなら先のもの）。候補が無ければ undefined。
export function pickCandidate<T>(
  candidates: readonly Candidate<T>[],
  placed: readonly Placed[],
  bounds: Box | null,
): Candidate<T> | undefined {
  let best: Candidate<T> | undefined;
  let bestCost = Infinity;
  for (const c of candidates) {
    const cost = placementCost(c.boxes, placed, bounds);
    if (cost === 0) return c;
    if (cost < bestCost) {
      best = c;
      bestCost = cost;
    }
  }
  return best;
}

// --- 大きさの見積もり（絵の拡大率 1・文字の倍率 1 のときの px） -----------------------------------

// 文字の幅の見積もり。半角は asciiWidth、それ以外は wideWidth を文字の大きさに掛ける。
export function textWidth(text: string, fontSize: number): number {
  let w = 0;
  for (const ch of text) {
    w += (ch.codePointAt(0) ?? 0) < 0x80 ? P.asciiWidth : P.wideWidth;
  }
  return w * fontSize;
}

function textHeight(fontSize: number): number {
  return fontSize * P.lineHeight;
}

function boxAt(cx: number, cy: number, halfW: number, halfH: number): Box {
  return { left: cx - halfW, top: cy - halfH, right: cx + halfW, bottom: cy + halfH };
}

function shift(b: Box, at: Point, k: number): Box {
  return {
    left: at.x + b.left * k,
    top: at.y + b.top * k,
    right: at.x + b.right * k,
    bottom: at.y + b.bottom * k,
  };
}

// 島の絵（砂浜までの円に外接する正方形。周りの浅瀬の輪には札が重なってよい）と、上の役のマーク
// （マークが無いときも場所を空けておく）。名札は、島の絵から labelGap だけ離して置く。
const ISLAND_BODY = S.islandRadius;
const TAG_GAP = S.islandRadius + D.labelGap;
const MARK_HEIGHT = D.markFontSize * 1.1;
const MARK_HALF_WIDTH = D.markFontSize * 0.6;

function islandBoxes(id: number, at: Point, k: number): Placed[] {
  return [
    { box: shift(boxAt(0, 0, ISLAND_BODY, ISLAND_BODY), at, k), owner: islandOwner(id) },
    {
      owner: `mark:${String(id)}`,
      box: shift(
        {
          left: -MARK_HALF_WIDTH,
          right: MARK_HALF_WIDTH,
          top: -S.islandRadius - MARK_HEIGHT,
          bottom: -S.islandRadius,
        },
        at,
        k,
      ),
    },
  ];
}

function islandOwner(id: number): string {
  return `island:${String(id)}`;
}

export type TagSide = "below" | "right" | "left" | "above";
const TAG_SIDES: readonly TagSide[] = ["below", "right", "left", "above"];

// 名札の大きさ（絵の拡大率 1 の px）。
export function nameTagSize(name: string, ls: number): Size {
  return {
    width: textWidth(name, D.labelFontSize) * ls + 2 * A.nameTagPadX + A.nameTagStrokeWidth,
    height: textHeight(D.labelFontSize) * ls + 2 * A.nameTagPadY,
  };
}

// 名札の上辺の中央（島の中心からのずれ。絵の拡大率 1 の px）。
export function nameTagOffset(side: TagSide, size: Size): Point {
  switch (side) {
    case "below":
      return { x: 0, y: TAG_GAP };
    case "right":
      return { x: TAG_GAP + size.width / 2, y: -size.height / 2 };
    case "left":
      return { x: -TAG_GAP - size.width / 2, y: -size.height / 2 };
    case "above":
      return { x: 0, y: -S.islandRadius - MARK_HEIGHT - D.labelGap - size.height };
  }
}

function tagBox(offset: Point, size: Size, at: Point, k: number): Box {
  return shift(
    {
      left: offset.x - size.width / 2,
      right: offset.x + size.width / 2,
      top: offset.y,
      bottom: offset.y + size.height,
    },
    at,
    k,
  );
}

// 手紙の山（封筒・重ねた封筒・数のバッジ・★）の箱。中心は封筒の中心。
const PILE_BOX: Box = {
  left: -D.envelopeWidth / 2 - D.badgeFontSize * 0.7,
  right: D.envelopeWidth / 2 + D.badgeRadius + D.envelopeStrokeWidth / 2,
  top: Math.min(
    -D.envelopeHeight / 2 - (D.envelopeLayers - 1) * D.envelopeShift - D.envelopeStrokeWidth / 2,
    -D.envelopeHeight / 2 - D.badgeFontSize * 0.7,
  ),
  bottom: D.envelopeHeight / 2 + D.badgeRadius + D.envelopeStrokeWidth / 2,
};

function pileBox(at: Point, k: number): Box {
  return shift(PILE_BOX, at, k);
}

// --- 船の文字の並び --------------------------------------------------------------------------

const SHIP_HALF = D.ferryRingRadius + D.ferryRingWidth / 2;

export type TextRow = Readonly<{ x: number; y: number; align: "center" | "left" | "right" }>;

// ferryTextRows は船の文字の行（上から名前・積み荷・到着まで）の位置。y は行の上端、x は align の基準
// （center は中央、left は左端、right は右端）。船の中心が原点で、絵の拡大率 1 の px。描画（pixi/mapRenderer.ts）も同じ値を使う。
export function ferryTextRows(labels: FerryLabels, rows: number, ls: number): TextRow[] {
  const pitch = D.ferryLineHeight * ls;
  const rowH = ferryRowHeight(ls);
  const out: TextRow[] = [];
  const blockH = pitch * (rows - 1) + rowH;
  for (let i = 0; i < rows; i++) {
    switch (labels) {
      case "split":
        out.push({
          x: 0,
          y: i === 0 ? -SHIP_HALF - rowH : SHIP_HALF + (i - 1) * pitch,
          align: "center",
        });
        break;
      case "above":
        out.push({ x: 0, y: -SHIP_HALF - blockH + i * pitch, align: "center" });
        break;
      case "below":
        out.push({ x: 0, y: SHIP_HALF + i * pitch, align: "center" });
        break;
      case "right":
      case "right-up":
      case "right-down":
        out.push({
          x: SHIP_HALF + P.ferryTextGap,
          y: -blockH / 2 + sideShift(labels) + i * pitch,
          align: "left",
        });
        break;
      case "left":
      case "left-up":
      case "left-down":
        out.push({
          x: -SHIP_HALF - P.ferryTextGap,
          y: -blockH / 2 + sideShift(labels) + i * pitch,
          align: "right",
        });
        break;
    }
  }
  return out;
}

function sideShift(labels: FerryLabels): number {
  if (labels.endsWith("-up")) return -SHIP_HALF;
  if (labels.endsWith("-down")) return SHIP_HALF;
  return 0;
}

function ferryRowHeight(ls: number): number {
  return textHeight(D.ferryLabelFontSize) * ls + D.haloWidth;
}

export type FerryText = Readonly<{ name: string; cargo: string; eta: string | null }>;

// 船の文字の各行の幅（絵の拡大率 1 の px）。積み荷の行は封筒の印を含む。
function ferryRowWidths(text: FerryText, ls: number): number[] {
  const halo = D.haloWidth;
  const widths = [
    textWidth(text.name, D.ferryLabelFontSize) * ls + halo,
    A.cargoIconWidth + A.cargoIconGap + textWidth(text.cargo, D.ferryLabelFontSize) * ls + halo,
  ];
  if (text.eta !== null) widths.push(textWidth(text.eta, D.ferryLabelFontSize) * ls + halo);
  return widths;
}

function shipBox(at: Point, k: number): Box {
  return shift(boxAt(0, 0, SHIP_HALF, SHIP_HALF), at, k);
}

// 船の文字の行の箱（画面の px）。
export function ferryTextBoxes(
  at: Point,
  k: number,
  ls: number,
  text: FerryText,
  labels: FerryLabels,
): Box[] {
  const widths = ferryRowWidths(text, ls);
  const rowH = ferryRowHeight(ls);
  return ferryTextRows(labels, widths.length, ls).map((row, i) => {
    const w = widths[i] ?? 0;
    const left = row.align === "center" ? row.x - w / 2 : row.align === "left" ? row.x : row.x - w;
    return shift({ left, right: left + w, top: row.y, bottom: row.y + rowH }, at, k);
  });
}

// --- 全体 ------------------------------------------------------------------------------------

export type LabelIsland = Readonly<{ id: number; at: Point; name: string }>;
export type LabelPile = Readonly<{ islandId: number; to: number; known: boolean }>;
export type LabelDockedFerry = Readonly<{ id: string; islandId: number; text: FerryText }>;
export type LabelSailingFerry = Readonly<{ id: string; at: Point; text: FerryText }>;

export type LabelInput = Readonly<{
  size: Size;
  k: number; // 絵の拡大率
  ls: number; // 文字の倍率
  islands: readonly LabelIsland[];
  piles: readonly LabelPile[]; // 港にある山（宛先が分かるものは置き場を使い、分からないものは 4. で置く）
  docked: readonly LabelDockedFerry[];
  sailing: readonly LabelSailingFerry[];
}>;

export type LabelLayout = Readonly<{
  tags: ReadonlyMap<number, Point>; // 島の ID → 名札の上辺の中央（島の中心からのずれ。絵の拡大率 1 の px）
  piles: ReadonlyMap<string, Point>; // pileKey → 山の中心（画面の px）
  ferries: ReadonlyMap<string, Readonly<{ at: Point; labels: FerryLabels }>>; // 船の ID → 船の中心と文字の置き方
  // 置いたものの箱と持ち主（island:<島>・mark:<島>・tag・pile・ferry:<船>）。テストで重なりを確かめる。
  placed: readonly Placed[];
  // placed のうち、航行中の船を除いたもの（1〜5。手紙の処分・差し替えの文字の札が避ける。動く船を避けると、
  // 札が出ている間に跳ねるため）。
  fixed: readonly Placed[];
}>;

// mapBounds は札を置いてよい範囲（描画領域から、左下に重ねる島のボタンの列の高さを除いたもの）。
export function mapBounds(size: Size): Box {
  return {
    left: 0,
    top: 0,
    right: size.width,
    bottom: Math.max(0, size.height - MAP_VIEW.bottomReserve),
  };
}

export function pileKey(islandId: number, to: number): string {
  return `${String(islandId)}:${String(to)}`;
}

// 宛先の島の向き（angle）を中心に、近い向き・近い距離の順に並べた手紙の山の候補（画面の px）。
function pileCandidates(center: Point, angle: number, k: number): Candidate<Point>[] {
  const scored: { score: number; at: Point }[] = [];
  const step = (2 * Math.PI) / P.pileAngles;
  for (let ring = 0; ring < P.pileRings; ring++) {
    const r = (S.pileOffset + ring * P.pileRingStep) * k;
    for (let j = 0; j <= P.pileAngles / 2; j++) {
      for (const sign of j === 0 || j === P.pileAngles / 2 ? [1] : [1, -1]) {
        const a = angle + sign * j * step;
        scored.push({
          score: j + ring * P.pileRingCost,
          at: { x: center.x + Math.cos(a) * r, y: center.y + Math.sin(a) * r },
        });
      }
    }
  }
  // 安定な並べ替え（同じ点数なら作った順）。
  scored.sort((a, b) => a.score - b.score);
  return scored.map((s) => ({ value: s.at, boxes: [pileBox(s.at, k)] }));
}

// placed は島の絵・役のマーク・名札だけ（山の置き場は、山があるときだけ layoutLabels が足す）。
type StaticLayout = Readonly<{
  key: string;
  tags: Map<number, Point>;
  slots: Map<string, Readonly<{ at: Point; boxes: readonly Box[] }>>;
  placed: readonly Placed[];
}>;

// 島の配置と画面の大きさだけで決まる部分（1〜3）。毎フレーム同じなので、直前の結果を使い回す。
let staticMemo: { key: string; value: StaticLayout } | null = null;

function staticLayout(input: LabelInput, bounds: Box): StaticLayout {
  const { k, ls, islands } = input;
  const key = JSON.stringify([input.size.width, input.size.height, k, ls, islands]);
  if (staticMemo !== null && staticMemo.key === key) return staticMemo.value;

  const placed: Placed[] = [];
  for (const island of islands) {
    placed.push(...islandBoxes(island.id, island.at, k));
  }
  const tags = new Map<number, Point>();
  for (const island of islands) {
    const size = nameTagSize(island.name, ls);
    const chosen = pickCandidate(
      TAG_SIDES.map((side) => {
        const offset = nameTagOffset(side, size);
        return { value: offset, boxes: [tagBox(offset, size, island.at, k)] };
      }),
      placed,
      bounds,
    );
    if (chosen === undefined) continue;
    tags.set(island.id, chosen.value);
    for (const box of chosen.boxes) placed.push({ box, owner: "tag" });
  }
  // 山の置き場は、ほかの置き場とも重ならないように選ぶ（どの山があっても重ならず、山が増えても動かない）。
  const withSlots: Placed[] = [...placed];
  const slots = new Map<string, { at: Point; boxes: readonly Box[] }>();
  for (const island of islands) {
    for (const dest of islands) {
      if (dest.id === island.id) continue;
      const angle = Math.atan2(dest.at.y - island.at.y, dest.at.x - island.at.x);
      const chosen = pickCandidate(pileCandidates(island.at, angle, k), withSlots, bounds);
      if (chosen === undefined) continue;
      slots.set(pileKey(island.id, dest.id), { at: chosen.value, boxes: chosen.boxes });
      for (const box of chosen.boxes) withSlots.push({ box, owner: "pile" });
    }
  }
  const value = { key, tags, slots, placed };
  staticMemo = { key, value };
  return value;
}

// 停泊中の船の、文字の置き方の候補の順。島から外向きの側を先に試す。
function labelOrder(angle: number): FerryLabels[] {
  const c = Math.cos(angle);
  const s = Math.sin(angle);
  const outward: FerryLabels =
    Math.abs(c) >= Math.abs(s) ? (c >= 0 ? "right" : "left") : s < 0 ? "above" : "below";
  return ["split", outward, ...FERRY_LABELS.filter((l) => l !== "split" && l !== outward)];
}

type FerryChoice = Readonly<{ at: Point; labels: FerryLabels; boxes: Box[] }>;

// 1 隻の船の文字の置き方を選ぶ。shipIgnore は船の絵の重なりを数えない owner（停泊中の船の自分の島）。
function chooseFerry(
  at: Point,
  text: FerryText,
  order: readonly FerryLabels[],
  input: LabelInput,
  placed: readonly Placed[],
  bounds: Box,
  shipIgnore: string | null,
): { choice: FerryChoice; cost: number } {
  const ship = shipBox(at, input.k);
  const shipCost = placementCost([ship], placed, bounds, shipIgnore);
  let best: { choice: FerryChoice; cost: number } | null = null;
  for (const labels of order) {
    const boxes = ferryTextBoxes(at, input.k, input.ls, text, labels);
    const cost = shipCost + placementCost(boxes, placed, bounds);
    if (best === null || cost < best.cost) {
      best = { choice: { at, labels, boxes: [ship, ...boxes] }, cost };
      if (cost === 0) break;
    }
  }
  // order は空でない（FERRY_LABELS のどれか）。
  return best ?? { choice: { at, labels: "split", boxes: [ship] }, cost: shipCost };
}

// 半径 r・最初の向き start で等間隔に並べた船の、文字の置き方を選ぶ。船の絵を先に全部置いてから、
// 1 隻ずつ文字の置き方を選び、もう 1 回、ほかの船の文字を見て選び直す。
function arrangeRing(
  center: Point,
  r: number,
  start: number,
  step: number,
  ferries: readonly LabelDockedFerry[],
  input: LabelInput,
  placed: readonly Placed[],
  bounds: Box,
  owner: string,
): { choices: FerryChoice[]; cost: number } {
  const k = input.k;
  const spots = ferries.map((ferry, i) => {
    const a = start + i * step;
    const at = { x: center.x + Math.cos(a) * r, y: center.y + Math.sin(a) * r };
    return { ferry, a, at, ship: shipBox(at, k) };
  });
  // 船の絵どうしと、ほかの札との重なり（文字の置き方によらない）。
  const ships: Placed[] = spots.map((s) => ({ box: s.ship, owner: `ferry:${s.ferry.id}` }));
  let shipCost = 0;
  for (const s of spots) shipCost += placementCost([s.ship], placed, bounds, owner);
  for (let i = 0; i < ships.length; i++) {
    for (let j = i + 1; j < ships.length; j++) {
      const a = ships[i];
      const b = ships[j];
      if (a !== undefined && b !== undefined) shipCost += overlapArea(a.box, b.box);
    }
  }
  const texts: Box[][] = spots.map(() => []);
  const labels: FerryLabels[] = spots.map(() => "split");
  const costs: number[] = spots.map(() => 0);
  for (let pass = 0; pass < 2; pass++) {
    for (let i = 0; i < spots.length; i++) {
      const spot = spots[i];
      if (spot === undefined) continue;
      if (pass > 0 && costs[i] === 0) continue;
      const others: Placed[] = [...placed, ...ships];
      texts.forEach((boxes, j) => {
        if (j !== i) for (const box of boxes) others.push({ box, owner: "ferry-text" });
      });
      let bestCost = Infinity;
      for (const l of labelOrder(spot.a)) {
        const boxes = ferryTextBoxes(spot.at, k, input.ls, spot.ferry.text, l);
        const cost = placementCost(boxes, others, bounds);
        if (cost < bestCost) {
          bestCost = cost;
          labels[i] = l;
          texts[i] = boxes;
          if (cost === 0) break;
        }
      }
      costs[i] = bestCost;
    }
    if (costs.every((c) => c === 0)) break;
  }
  // 選び直したあとの重なりの合計（文字どうしの重なりは 2 回数えないよう、j > i だけ）。
  let total = shipCost;
  texts.forEach((boxes, i) => {
    const others: Placed[] = [...placed, ...ships];
    texts.forEach((b, j) => {
      if (j > i) for (const box of b) others.push({ box, owner: "ferry-text" });
    });
    total += placementCost(boxes, others, bounds);
  });
  return {
    choices: spots.map((s, i) => ({
      at: s.at,
      labels: labels[i] ?? "split",
      boxes: [s.ship, ...(texts[i] ?? [])],
    })),
    cost: total,
  };
}

// 0, +1, -1, +2, -2 … の順の count 個の回し方。
function rotations(count: number): number[] {
  const out: number[] = [];
  for (let j = 0; j < count; j++) out.push(j === 0 ? 0 : j % 2 === 1 ? (j + 1) / 2 : -j / 2);
  return out;
}

// 同じ港の n 隻の船を、島の周りに等間隔に並べる。半径と向きを候補から選ぶ。
function placeDocked(
  center: Point,
  islandId: number,
  ferries: readonly LabelDockedFerry[],
  input: LabelInput,
  placed: Placed[],
  bounds: Box,
): FerryChoice[] {
  const n = ferries.length;
  const k = input.k;
  const owner = islandOwner(islandId);
  const baseX = S.islandRadius + S.dockOffset;
  const baseY = -S.islandRadius;
  const r0 = Math.hypot(baseX, baseY);
  const angle0 = Math.atan2(baseY, baseX);
  // 並べ方: まず島を一周する等間隔（1 隻なら右上の桟橋から回す）。どの半径・向きでも重なるときだけ、
  // 島の周りの弧（P.dockArcs の割合の角度）に等間隔に並べ、弧の向きを回す（描画領域の端や隣の島を避ける）。
  type Spread = Readonly<{ step: number; starts: readonly number[] }>;
  const spreads: Spread[] = [];
  {
    const step = (2 * Math.PI) / Math.max(1, n);
    const count = Math.max(2, Math.ceil(P.dockAngles / Math.max(1, n)));
    spreads.push({ step, starts: rotations(count).map((m) => angle0 + (m * step) / count) });
  }
  if (n >= 2) {
    for (const ratio of P.dockArcs) {
      const span = 2 * Math.PI * ratio;
      const step = span / (n - 1);
      spreads.push({
        step,
        starts: rotations(P.dockAngles).map(
          (m) => angle0 + (m * 2 * Math.PI) / P.dockAngles - span / 2,
        ),
      });
    }
  }
  let best: { choices: FerryChoice[]; cost: number } | null = null;
  search: for (const spread of spreads) {
    // 隣の船と重ならない半径（弦の長さが dockMinGap 以上）。
    const minR = n <= 1 ? 0 : P.dockMinGap / (2 * Math.sin(spread.step / 2));
    for (let ring = 0; ring < P.dockRings; ring++) {
      const r = Math.max(r0 + ring * P.dockRingStep, minR) * k;
      for (const start of spread.starts) {
        const got = arrangeRing(
          center,
          r,
          start,
          spread.step,
          ferries,
          input,
          placed,
          bounds,
          owner,
        );
        if (best === null || got.cost < best.cost) best = got;
        if (got.cost === 0) break search;
      }
    }
  }
  const choices = best?.choices ?? [];
  choices.forEach((choice, i) => {
    const id = ferries[i]?.id ?? "";
    for (const box of choice.boxes) placed.push({ box, owner: `ferry:${id}` });
  });
  return choices;
}

type PortLayout = Readonly<{
  piles: ReadonlyMap<string, Point>;
  ferries: ReadonlyMap<string, Readonly<{ at: Point; labels: FerryLabels }>>;
  placed: readonly Placed[];
}>;

let dockedMemo: { key: string; value: PortLayout } | null = null;

// 4（港にある山）と 5（停泊中の船）。
function portLayout(input: LabelInput, base: StaticLayout, bounds: Box): PortLayout {
  const k = input.k;
  const placed: Placed[] = [...base.placed];
  const centers = new Map(input.islands.map((i) => [i.id, i.at]));

  // 4. 港にある山。宛先が分かる山は置き場、分からない山は島の真下から並べる（ほかの候補へずらす）。
  const piles = new Map<string, Point>();
  const stacked = new Map<number, number>();
  const unknown: LabelPile[] = [];
  for (const pile of input.piles) {
    const key = pileKey(pile.islandId, pile.to);
    const slot = pile.known ? base.slots.get(key) : undefined;
    if (slot === undefined) {
      unknown.push(pile);
      continue;
    }
    piles.set(key, slot.at);
    for (const box of slot.boxes) placed.push({ box, owner: "pile" });
  }
  for (const pile of unknown) {
    const key = pileKey(pile.islandId, pile.to);
    const center = centers.get(pile.islandId);
    if (center === undefined) continue;
    const n = stacked.get(pile.islandId) ?? 0;
    stacked.set(pile.islandId, n + 1);
    const below = {
      x: center.x,
      y: center.y + (S.pileOffset + n * S.pileStackStep) * k,
    };
    const candidates = [
      { value: below, boxes: [pileBox(below, k)] },
      ...pileCandidates(center, Math.PI / 2, k),
    ];
    const chosen = pickCandidate(candidates, placed, bounds);
    const at = chosen?.value ?? below;
    piles.set(key, at);
    for (const box of chosen?.boxes ?? []) placed.push({ box, owner: "pile" });
  }

  // 5. 停泊中の船（港ごと。島の並びの順）。
  const ferries = new Map<string, { at: Point; labels: FerryLabels }>();
  for (const island of input.islands) {
    const here = input.docked.filter((f) => f.islandId === island.id);
    if (here.length === 0) continue;
    const choices = placeDocked(island.at, island.id, here, input, placed, bounds);
    choices.forEach((choice, i) => {
      const id = here[i]?.id;
      if (id !== undefined) ferries.set(id, { at: choice.at, labels: choice.labels });
    });
  }

  return { piles, ferries, placed };
}

// layoutLabels は札の位置を決める。同じ入力なら同じ結果（入力を書き換えない）。
export function layoutLabels(input: LabelInput): LabelLayout {
  const bounds = mapBounds(input.size);
  const base = staticLayout(input, bounds);
  // 4・5 は、港の山と停泊中の船が変わったときだけ作り直す（停泊中の船の候補は多いので、毎フレームは探さない）。
  const dynKey = JSON.stringify([base.key, input.piles, input.docked]);
  if (dockedMemo === null || dockedMemo.key !== dynKey) {
    dockedMemo = { key: dynKey, value: portLayout(input, base, bounds) };
  }
  const { piles } = dockedMemo.value;
  const placed: Placed[] = [...dockedMemo.value.placed];
  const ferries = new Map(dockedMemo.value.ferries);

  // 6. 航行中の船の文字。
  for (const ferry of input.sailing) {
    const { choice } = chooseFerry(ferry.at, ferry.text, FERRY_LABELS, input, placed, bounds, null);
    ferries.set(ferry.id, { at: ferry.at, labels: choice.labels });
    for (const box of choice.boxes) placed.push({ box, owner: `ferry:${ferry.id}` });
  }

  return {
    tags: base.tags,
    piles,
    ferries,
    placed,
    fixed: dockedMemo.value.placed,
  };
}

// --- 手紙の処分・差し替えの文字の札（T-428） ---------------------------------------------------

export type CaptionItem = Readonly<{
  key: string;
  center: Point; // 札を出す島の中心（画面の px）
  text: string;
  fontSize: number; // 絵の拡大率 1 の px（描画は k を掛ける）
}>;

// 文字の札の大きさ（画面の px）。縁取りの幅を含む。
export function captionSize(text: string, fontSize: number, k: number): Size {
  return {
    width: (textWidth(text, fontSize) + D.haloWidth) * k,
    height: (textHeight(fontSize) + D.haloWidth) * k,
  };
}

// 島の周りの円の候補（近い円から。同じ円では真上から左右へ交互に回す）。札の中心を返す。
function captionCandidates(center: Point, size: Size, k: number): Candidate<Point>[] {
  const out: Candidate<Point>[] = [];
  const step = (2 * Math.PI) / P.captionAngles;
  for (let ring = 0; ring < P.captionRings; ring++) {
    const r = (S.islandRadius + ring * P.captionRingStep) * k;
    for (const m of rotations(P.captionAngles)) {
      const a = -Math.PI / 2 + m * step;
      const at = {
        x: center.x + Math.cos(a) * (r + size.width / 2),
        y: center.y + Math.sin(a) * (r + size.height / 2),
      };
      out.push({ value: at, boxes: [boxAt(at.x, at.y, size.width / 2, size.height / 2)] });
    }
  }
  return out;
}

// placeCaptions は文字の札を、島の周りの円の候補から、placed（島・名札・山・船など）とほかの札に重ならず
// bounds からはみ出さない位置に置く（どれも重なるなら重なりがいちばん小さい位置）。items の順に置く。
// 返すのは key → 札の中心（画面の px）と、置いた札の箱（持ち主は caption:<key>）。
export function placeCaptions(
  items: readonly CaptionItem[],
  placed: readonly Placed[],
  bounds: Box | null,
  k: number,
): { at: ReadonlyMap<string, Point>; placed: readonly Placed[] } {
  const all: Placed[] = [...placed];
  const at = new Map<string, Point>();
  const out: Placed[] = [];
  for (const item of items) {
    const size = captionSize(item.text, item.fontSize, k);
    const chosen = pickCandidate(captionCandidates(item.center, size, k), all, bounds);
    if (chosen === undefined) continue;
    at.set(item.key, chosen.value);
    for (const box of chosen.boxes) {
      const p = { box, owner: `caption:${item.key}` };
      all.push(p);
      out.push(p);
    }
  }
  return { at, placed: out };
}
