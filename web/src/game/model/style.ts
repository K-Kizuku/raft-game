// マップの描画の定数（T-404 / DesignDoc §13.3）。画面のための値で、ゲームの数値ではない
// （ステージ定義に置かない）。ほかのファイルに描画の数を直接書かず、ここから読む。
export const MAP_STYLE = {
  padding: 56, // 島の外接矩形の周りの余白（px）
  maxScale: 1.5, // 島が少ないときに拡大しすぎない
  minScale: 0.1,
  islandRadius: 22, // 島の円の半径（px）
  islandHitSlop: 8, // 島の当たり判定の余裕（px）
  ferryHitRadius: 18,
  pileOffset: 52, // 島の中心から手紙の山までの距離（px）
  pileStackStep: 20, // 宛先の島が分からない山を縦に並べる間隔（px）
  dockOffset: 12, // 停泊中の船を島の右上に並べる余白（px）
  dockStep: 40, // 同じ島に停泊する 2 隻目以降のずらし（px）
  dash: 10, // 嵐の航路の点線（px）
  dashGap: 8,
  timeHoldMs: 250, // 推定時刻が少し戻ったときに止めておく幅（ゲーム内ミリ秒）
  colors: {
    sea: "#BFE3F2",
    route: "#5B7083",
    routeBlocked: "#374151",
    unknownIsland: "#9CA3AF",
    text: "#1F2937",
    selected: "#111827",
    candidate: "#F59E0B",
    islandStroke: "#1F2937",
    cloud: "#6B7280",
    badge: "#FFFFFF",
    envelope: "#FFFDF5",
    star: "#B45309",
    ferryRing: "#111827",
    halo: "#FFFFFF", // 文字の縁（海・島・封筒の上でも読めるように）
  },
  // PixiJS の層（web/src/game/pixi/）が使う線の幅・文字の大きさ・透明度。
  draw: {
    routeWidth: 3,
    islandStrokeWidth: 3,
    selectedRingGap: 6,
    selectedRingWidth: 4,
    candidateRingGap: 10,
    candidateRingWidth: 3,
    candidateDash: 6,
    candidateDashGap: 5,
    labelGap: 4, // 島の円と名前の文字の間（px）
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif",
    labelFontSize: 13,
    haloWidth: 3,
    markFontSize: 20,
    stormFontSize: 22,
    forecastAlpha: 0.6,
    staleLeaderAlpha: 0.4,
    cloudRadiusX: 14,
    cloudRadiusY: 9,
    cloudSpread: 12, // 雲の楕円の横のずらし
    envelopeWidth: 22,
    envelopeHeight: 15,
    envelopeRadius: 3,
    envelopeShift: 3, // 封筒を重ねるずらし
    envelopeLayers: 3,
    envelopeStrokeWidth: 2,
    badgeRadius: 9,
    badgeFontSize: 11,
    ferryFontSize: 20,
    ferryLabelFontSize: 11,
    ferryRingRadius: 15,
    ferryLineHeight: 13, // 船の下に並べる文字（積み荷・到着まで）の行の高さ
    ferryRingWidth: 3,
    brokenFontSize: 13,
  },
} as const;
