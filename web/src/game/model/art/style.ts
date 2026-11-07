// マップの絵（T-421・docs/11-visual-design.md）の形と動きの定数。画面のための値で、ゲームの数値ではない
// （ステージ定義に置かない）。幾何の値（島の半径など）は MAP_STYLE（../style.ts）にあり、ここでは変えない。
// ほかのファイルに絵の数を直接書かず、ここから読む。
export const MAP_ART_STYLE = Object.freeze({
  // 海のにじみ（水彩風）
  seaBlobCount: 14,
  seaBlobMinRatio: 0.12, // にじみの半径の下限（画面の短い辺に対する割合）
  seaBlobMaxRatio: 0.32, // 上限
  seaBlobSeed: 0x5eab10b, // にじみの位置を決める擬似乱数の種（固定）
  seaBlobRings: 6, // 1 つのにじみを重ねる円の数（縁をぼかす）
  seaBlobRingShrink: 0.13, // 内側の円を小さくする割合（1 段ごと）
  seaBlobAlpha: { light: 0.07, mid: 0.06, deep: 0.03 },

  // 波の記号（海図の短い弧）
  waveSpacing: 96, // 波の記号を置く格子の間隔（px）
  waveJitter: 0.35, // 格子からずらす量（間隔に対する割合）
  waveSeed: 0x3a7e5, // 波の記号の位置を決める擬似乱数の種（固定）
  waveHalfLength: 6, // 「〜」の山 1 つ（半波）の横の長さ（px）
  waveHalves: 3, // 1 つの記号に並べる半波の数
  waveHeight: 2.6, // 山の高さ（px）
  waveWidth: 1.5,
  waveAlpha: 0.35,
  wavePeriodMs: 7000, // 横の揺れの周期（実時間）
  waveAmplitude: 6, // 横の揺れの振幅（px）

  // 島
  islandPoints: 12, // 輪郭の頂点の数
  islandMinRatio: 0.82, // 頂点の中心からの距離の下限（半径に対する割合）
  islandSeed: 0x15a1d, // 島の形を決める擬似乱数の種（島の ID と混ぜる）
  sandWidth: 5, // 砂浜の縁の幅（px）
  innerStrokeWidth: 1.5, // 島の色の部分の線
  innerStrokeAlpha: 0.55,
  shallowAlpha: 0.75, // 島の周りの浅瀬の明るい輪
  pierAngle: -0.5, // 桟橋の向き（ラジアン。右上の停泊する側）
  pierFrom: 0.62, // 桟橋の根元（島の半径に対する割合）
  pierWidth: 5,
  pierPostRadius: 1.6,
  postMarkAt: 0.42, // 〒 の印の位置（島の半径に対する割合。右上の向き）
  postMarkRadius: 6.5,
  postMarkStroke: 1.5,
  postMarkBar: 3.6, // 〒 の横線の半分の長さ
  nameTagPadX: 6,
  nameTagPadY: 2,
  nameTagRadius: 6,
  nameTagStrokeWidth: 1.5,

  // 航路（海図の点線）
  dotSpacing: 11, // 点の間隔（px）
  dotRadius: 2.4,
  blockedDash: 16, // 嵐の航路の長い破線（px）
  blockedGap: 9,
  blockedWidth: 3,
  crossSize: 6, // ✕ の印の半分の大きさ（px）
  crossWidth: 3.5,
  crossHalo: 2, // ✕ の下に敷く紙の色の縁の太さの足し分（px）

  // 嵐
  cloudLift: 30, // 航路の中点から雲の中心までの高さ（px）
  cloudRadiusX: 16,
  cloudRadiusY: 10,
  cloudSpread: 13, // 雲の楕円の横のずらし
  cloudShadowDrop: 4, // 下の濃い雲のずらし
  cloudPoints: 48, // 雲の外周の頂点の数
  forecastAlpha: 0.55,
  rainTopRatio: 0.4, // 雨の上端（雲の中心から cloudRadiusY のこの割合だけ下。雲に隠れる）
  rainSpreadRatio: 0.5, // 雨の横の広がり（両端の楕円の中心から cloudRadiusX のこの割合まで）
  rainWidth: 1.8,
  rainAlpha: 0.8,
  rainSlant: 4, // 雨の線の横の傾き（px。上から下へ）
  rainLength: 7,
  rainPeriod: 12, // 雨の模様の縦の繰り返し（px）
  rainPerPeriod: 5, // 1 つの繰り返しに並べる雨の線の数
  rainMaxLines: 24, // 1 つの嵐の雨の線の上限
  rainSpeed: 0.03, // 雨が落ちる速さ（px / 実時間 ms）

  // 船（郵便船のシルエット。中心が原点・右向き）
  hullHalf: 13, // 船体の幅の半分
  hullTop: 2,
  hullBottom: 8,
  hullBowRise: 3, // 船首の反り
  cabinLeft: -7,
  cabinRight: 3,
  cabinTop: -4,
  mastX: -1,
  mastTop: -15,
  flagLength: 9,
  flagHeight: 5,
  shipStrokeWidth: 1.5,
  hullEnvelopeWidth: 8,
  hullEnvelopeHeight: 4.5,
  bobPeriodMs: 2400, // 航行中の揺れの周期（実時間）
  bobAmplitude: 1.6, // 縦の揺れ（px）
  bobRotation: 0.06, // 傾き（ラジアン）
  cargoIconWidth: 11,
  cargoIconHeight: 7.5,
  cargoIconGap: 3,

  // 封筒（手紙の山・処分のアニメーション）
  stampWidth: 7,
  stampHeight: 8,
  stampInset: 2,
  flapStrokeWidth: 1.2,
  starStrokeWidth: 2.5,

  // 文字の倍率（labelScale。T-425）。画面の幅が labelScaleMinWidth 以下で labelScaleMin、
  // labelScaleMaxWidth 以上で labelScaleMax、その間は直線で補う。大きな画面（プロジェクター）で
  // 島の名前・船の文字を読みやすくする（1920 の幅で島の名前が 16px 以上）。
  labelScaleMinWidth: 1280,
  labelScaleMaxWidth: 1920,
  labelScaleMin: 1,
  labelScaleMax: 1.3,
});
