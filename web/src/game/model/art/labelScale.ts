// マップの文字の倍率（T-425・docs/11-visual-design.md §12）。画面の幅だけで決まる純粋な関数。
// 値は MAP_ART_STYLE（./style.ts）の labelScale* にある。PixiJS の層（web/src/game/pixi/）が、
// 画面の大きさが変わったときだけ Text の scale に掛ける（毎フレーム作り直さない）。
import { MAP_ART_STYLE } from "./style.ts";

const A = MAP_ART_STYLE;

/**
 * 画面の幅（CSS の px）から文字の倍率を返す。labelScaleMinWidth 以下（0・負・数でない値も）で labelScaleMin、
 * labelScaleMaxWidth 以上で labelScaleMax、その間は直線で補う（幅が広いほど大きい）。
 */
export function labelScale(screenWidth: number): number {
  if (Number.isNaN(screenWidth) || screenWidth <= A.labelScaleMinWidth) return A.labelScaleMin;
  if (screenWidth >= A.labelScaleMaxWidth) return A.labelScaleMax;
  const t = (screenWidth - A.labelScaleMinWidth) / (A.labelScaleMaxWidth - A.labelScaleMinWidth);
  return A.labelScaleMin + (A.labelScaleMax - A.labelScaleMin) * t;
}
