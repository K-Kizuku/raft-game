// 出港の絵（T-436）。出航を押してからゲームが始まるまでの待ち時間に、ゲーム画面のマップの上に出す。
// 海図の小さな窓に、郵便局のある港の島と、港を出ていく郵便船を描く（部品は parts.tsx のもの）。
// 飾りなので aria-hidden。動きは art.css の .departure-art-*（動きを減らす設定のときは動かさない）。
import type { ReactElement } from "react";

import { Island, MailShip } from "./parts.tsx";

// 波の印（小さな弧）を置く場所。
const WAVES: readonly (readonly [number, number])[] = [
  [150, 42],
  [250, 66],
  [196, 132],
  [286, 120],
];

export function DepartureArt({ className }: { className?: string }): ReactElement {
  return (
    <svg
      className={className === undefined ? "art departure-art" : `art departure-art ${className}`}
      viewBox="0 0 320 160"
      aria-hidden="true"
      focusable="false"
    >
      {/* 海図の窓（淡い海と経緯線） */}
      <rect className="departure-art-sea" x={4} y={4} width={312} height={152} rx={18} />
      <path className="departure-art-grid" d="M108 4 V156 M212 4 V156 M4 80 H316" />

      <g className="departure-art-waves">
        {WAVES.map(([x, y]) => (
          <path
            key={`${x}-${y}`}
            className="departure-art-wave"
            d={`M${x - 14} ${y} q5 -5 10 0 t10 0 t10 0`}
          />
        ))}
      </g>

      {/* 港から沖への航路（海図の点線） */}
      <path className="departure-art-route" d="M86 108 C140 120 210 112 300 92" />

      {/* 港の島（郵便局） */}
      <Island x={58} y={92} scale={0.62} variant="house" />

      {/* 港を出ていく郵便船 */}
      <g transform="translate(124 104)">
        <g className="departure-art-sail">
          <g className="departure-art-ship">
            <MailShip scale={0.42} />
          </g>
        </g>
      </g>
    </svg>
  );
}
