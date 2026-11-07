// 封筒 1 通の絵（T-421）。港の手紙の山（mapRenderer.ts の updatePile）と、手紙の処分・差し替えの
// アニメーション（letterEffects.ts）の両方が使う。紙の色の封筒に、ふたの線と、右上に宛先の島の色の切手。
import type { ColorSource, Graphics } from "pixi.js";

import { MAP_ART_STYLE } from "../model/art/style.ts";
import { MAP_STYLE } from "../model/style.ts";

const S = MAP_STYLE;
const D = MAP_STYLE.draw;
const A = MAP_ART_STYLE;

export type EnvelopeOptions = Readonly<{
  color: ColorSource; // 宛先の島の色（切手）
  alpha?: number; // 縁と切手の濃さ（流れる封筒は少し薄く）
  stroke?: ColorSource; // 縁とふたの線の色（省略すると墨）
  x?: number; // 封筒の中心
  y?: number;
  width?: number;
  height?: number;
}>;

/** g に封筒 1 通を描き足す（clear はしない）。 */
export function drawEnvelope(g: Graphics, options: EnvelopeOptions): Graphics {
  const w = options.width ?? D.envelopeWidth;
  const h = options.height ?? D.envelopeHeight;
  const cx = options.x ?? 0;
  const cy = options.y ?? 0;
  const alpha = options.alpha ?? 1;
  const stroke = options.stroke ?? S.colors.envelopeStroke;
  const left = cx - w / 2;
  const top = cy - h / 2;
  const strokeWidth = Math.min(D.envelopeStrokeWidth, w / 8);

  g.roundRect(left, top, w, h, Math.min(D.envelopeRadius, h / 4));
  g.fill({ color: S.colors.envelope });
  g.stroke({ width: strokeWidth, color: stroke, alpha });

  // ふた（上の 2 つの角から中央へ）
  g.moveTo(left + strokeWidth, top + strokeWidth)
    .lineTo(cx, cy + h * 0.1)
    .lineTo(left + w - strokeWidth, top + strokeWidth);
  g.stroke({ width: A.flapStrokeWidth, color: stroke, alpha });

  // 切手（宛先の島の色）。小さい封筒では大きさを合わせて縮める。
  const k = w / D.envelopeWidth;
  const sw = A.stampWidth * k;
  const sh = A.stampHeight * k;
  const inset = A.stampInset * k;
  g.rect(left + w - inset - sw, top + inset, sw, sh);
  g.fill({ color: options.color, alpha });
  g.stroke({ width: A.flapStrokeWidth * k, color: stroke, alpha });
  return g;
}
