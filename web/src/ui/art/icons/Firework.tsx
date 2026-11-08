// 打ち上げ花火のアイコン（T-437）。花火の絵文字の代わりに、文字の中に置く小さな SVG。
// 線（光の筋）と点（火の粉・打ち上げの跡）だけで描く。色は currentColor（隣の文字と同じ色）で、
// tone に色のトークンを渡すと、その色にする。大きさは文字に合わせて 1em。
// 飾りなので aria-hidden にし、支援技術には読ませない（意味は隣の文字が伝える）。
import type { CSSProperties, ReactElement } from "react";

import type { ColorTokenName } from "../../theme/tokens.ts";

// 中心（12, 9）から 8 方向の光の筋。真下は打ち上げの跡と重なるので描かない。
const RAYS: readonly (readonly [number, number, number, number])[] = [
  [12, 6.5, 12, 3.5],
  [13.77, 7.23, 15.89, 5.11],
  [14.5, 9, 17.5, 9],
  [13.77, 10.77, 15.89, 12.89],
  [10.23, 10.77, 8.11, 12.89],
  [9.5, 9, 6.5, 9],
  [10.23, 7.23, 8.11, 5.11],
];

// 筋の間に散る火の粉。
const SPARKS: readonly (readonly [number, number])[] = [
  [18.47, 6.32],
  [14.68, 2.53],
  [9.32, 2.53],
  [5.53, 6.32],
  [5.53, 11.68],
  [18.47, 11.68],
];

// 下から昇ってきた跡（下ほど小さい）。
const TRAIL: readonly (readonly [number, number, number])[] = [
  [12, 14.5, 1],
  [12, 18, 0.8],
  [12, 21.2, 0.6],
];

export function FireworkIcon({
  tone,
  className,
}: {
  tone?: ColorTokenName;
  className?: string;
}): ReactElement {
  const style: CSSProperties = { verticalAlign: "-0.125em", flex: "none" };
  if (tone !== undefined) {
    style.color = `var(${tone})`;
  }
  return (
    <svg
      className={className === undefined ? "firework-icon" : `firework-icon ${className}`}
      viewBox="0 0 24 24"
      width="1em"
      height="1em"
      fill="currentColor"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      style={style}
      aria-hidden="true"
      focusable="false"
      data-icon="firework"
    >
      <g fill="none">
        {RAYS.map(([x1, y1, x2, y2], i) => (
          <line key={i} x1={x1} y1={y1} x2={x2} y2={y2} />
        ))}
      </g>
      <g stroke="none">
        <circle cx={12} cy={9} r={1.2} />
        {SPARKS.map(([cx, cy], i) => (
          <circle key={`s${String(i)}`} cx={cx} cy={cy} r={0.9} />
        ))}
        {TRAIL.map(([cx, cy, r], i) => (
          <circle key={`t${String(i)}`} cx={cx} cy={cy} r={r} />
        ))}
      </g>
    </svg>
  );
}
