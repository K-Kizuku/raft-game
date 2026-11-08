// マップの描画の定数（T-404 / DesignDoc §13.3）。画面のための値で、ゲームの数値ではない
// （ステージ定義に置かない）。ほかのファイルに描画の数を直接書かず、ここから読む。
import { palette, tokens } from "../../ui/theme/tokens.ts";
import { mixColor } from "./art/color.ts";

// トークンに無い色（T-421）。どれもトークンの値を混ぜて作る（T-425 がトークンに入れるかを決める）。
export const MAP_DERIVED_COLORS = Object.freeze({
  // --color-sea-mid に --color-sea-deep を 30% 混ぜる（地の色よりわずかに濃い水彩のにじみ）
  seaMidBlob: mixColor(palette.seaMid, palette.seaDeep, 0.3),
  // --color-storm に --color-paper を 45% 混ぜる（予報の薄い灰色の雲）
  forecastCloud: mixColor(palette.storm, palette.paper, 0.45),
});

export const MAP_STYLE = {
  padding: 56, // 島の外接矩形の周りの余白（px）
  maxScale: 1.5, // 島が少ないときに拡大しすぎない
  minScale: 0.1,
  islandRadius: 22, // 島の円の半径（px）
  islandHitSlop: 8, // 島の当たり判定の余裕（px）
  ferryHitRadius: 18,
  pileOffset: 52, // 島の中心から手紙の山までの距離（px）
  pileStackStep: 20, // 宛先の島が分からない山を縦に並べる間隔（px）
  dockOffset: 20, // 停泊中の船を島の右上に並べる余白（px。積み荷の文字が島の絵に重ならない幅。T-428）
  dockStep: 40, // 同じ島に停泊する 2 隻目以降のずらし（px）
  dash: 10, // 嵐の航路の点線（px）
  dashGap: 8,
  timeHoldMs: 250, // 推定時刻が少し戻ったときに止めておく幅（ゲーム内ミリ秒）
  // 色はデザイントークン（web/src/ui/theme/tokens.ts）から取る。16 進の色をここに直接書かない。
  // 島の色はステージ定義（island.color）を使う。unknownIsland は scene.ts が島の色（文字列）の代わりに使う。
  colors: {
    sea: palette.seaMid, // 海の地の色
    seaLight: palette.seaLight, // 明るいにじみ
    seaMidBlob: MAP_DERIVED_COLORS.seaMidBlob, // 地の色よりわずかに濃いにじみ
    seaDeep: palette.seaDeep, // 濃いにじみ
    wave: palette.seaDeep, // 波の記号
    shallow: palette.seaLight, // 島の周りの浅瀬
    route: palette.seaDeep, // 航路の点線
    routeBlocked: palette.stormDeep, // 嵐の航路の破線と ✕
    unknownIsland: tokens["--color-ink-muted"],
    text: palette.ink,
    selected: palette.ink,
    candidate: palette.gold,
    islandStroke: palette.ink,
    sand: palette.land, // 砂浜の縁
    pier: palette.inkMuted, // 桟橋
    postal: palette.postal, // 〒・船の旗
    postMark: palette.paper, // 〒 の地
    nameTag: palette.paper, // 島の名札
    nameTagStroke: palette.ink,
    cloud: palette.storm, // 嵐の雲
    cloudDeep: palette.stormDeep, // 嵐の雲の下の濃い部分
    rain: palette.stormDeep,
    forecastCloud: MAP_DERIVED_COLORS.forecastCloud, // 予報の薄い雲
    badge: palette.paper,
    badgeStroke: palette.ink,
    envelope: palette.paper,
    envelopeStroke: palette.ink,
    star: palette.gold,
    starStroke: palette.ink,
    ferryRing: palette.ink,
    hull: palette.ink, // 船体
    cabin: palette.paper, // 船室
    shipStroke: palette.ink,
    hullEnvelope: palette.paper, // 船体の封筒の印
    hullEnvelopeFlap: palette.postal,
    halo: palette.paper, // 船の文字の縁（海・島・封筒の上でも読めるように）
  },
  // PixiJS の層（web/src/game/pixi/）が使う線の幅・文字の大きさ・透明度。
  // 形と動きの数（島の形・点線・雨・船の形など）は MAP_ART_STYLE（./art/style.ts）にある。
  draw: {
    islandStrokeWidth: 3,
    selectedRingGap: 6,
    selectedRingWidth: 4,
    candidateRingGap: 10,
    candidateRingWidth: 3,
    candidateDash: 6,
    candidateDashGap: 5,
    labelGap: 4, // 島と名札の間（px）
    fontFamily: tokens["--font-body"],
    // --font-number は var(--font-body) を含むので、canvas の文字に使えるよう展開する。
    numberFontFamily: tokens["--font-number"].replace("var(--font-body)", tokens["--font-body"]),
    labelFontSize: 13,
    haloWidth: 3,
    markFontSize: 20,
    staleLeaderAlpha: 0.4,
    envelopeWidth: 22,
    envelopeHeight: 15,
    envelopeRadius: 3,
    envelopeShift: 3, // 封筒を重ねるずらし
    envelopeLayers: 3,
    envelopeStrokeWidth: 2,
    badgeRadius: 9,
    badgeFontSize: 11,
    ferryLabelFontSize: 11,
    ferryRingRadius: 15,
    ferryLineHeight: 13, // 船の下に並べる文字（積み荷・到着まで）の行の高さ
    ferryRingWidth: 3,
    brokenFontSize: 13,
  },
  // 札（島の名札・手紙の山・船とその文字）の位置の候補（T-428。./placement.ts）。px は絵の拡大率 1 のときの値。
  placement: {
    // 文字の幅と高さの見積もり（文字の大きさに対する割合）。半角は asciiWidth、それ以外（かな・漢字）は wideWidth。
    asciiWidth: 0.62,
    wideWidth: 1,
    lineHeight: 1.3,
    // 船の文字を船の左右に置くときの、船の絵との間（px）。
    ferryTextGap: 3,
    // 手紙の山の候補: 島の中心からの距離を pileOffset から pileRingStep ずつ pileRings 段、
    // 向きを宛先の島の向きから 2π / pileAngles ずつ回す。
    pileRingStep: 30,
    pileRings: 3,
    pileAngles: 16,
    // 停泊中の船の候補: 島の中心からの距離を、右上の桟橋の位置から dockRingStep ずつ dockRings 段、
    // 並びの向きを dockAngles 通り（1 隻のとき。n 隻なら 2π / n の間を dockAngles / n 通り）回す。
    dockRingStep: 18,
    dockRings: 9,
    dockAngles: 32,
    // 一周に並べると重なるときの、弧の角度（一周に対する割合。大きい順に試す）。
    dockArcs: [0.75, 0.5, 0.3],
    // 停泊中の船どうしの中心の、最低限の間（px）。
    dockMinGap: 36,
  },
} as const;
