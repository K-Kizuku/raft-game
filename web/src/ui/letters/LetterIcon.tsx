// 手紙 1 通のアイコン（T-406・ゲームデザイン §2.1）。形・塗り・破線・印で種類を示し（NFR-10）、
// 縁（外側の輪）の色が宛先の島の色。append は記録の件数を右下に出す。
import type { ReactElement } from "react";

import type { Letter } from "../../protocol/index.ts";
import { letterRaftTerm, withRaftTerm } from "../terms/glossary.ts";
import { useShowRaftTerms } from "../terms/preference.ts";
import { letterKind } from "./letterKinds.ts";
import type { LetterKindStyle, LetterMark } from "./letterKinds.ts";

// 白の塗りでも見えるように、塗りつぶしの形の線は濃い色にする。
const STROKE = "#374151";
const WHITE = "#FFFFFF";

function Shape({ kind }: { kind: LetterKindStyle }): ReactElement {
  const props = {
    fill: kind.fill === "solid" ? kind.color : WHITE,
    stroke: kind.fill === "solid" ? STROKE : kind.color,
    strokeWidth: kind.fill === "solid" ? 1 : 1.6,
    strokeDasharray: kind.dashed ? "2 1.5" : undefined,
    strokeLinejoin: "round" as const,
  };
  switch (kind.shape) {
    case "circle":
      return <circle cx={10} cy={10} r={6} {...props} />;
    case "square":
      return <rect x={4} y={4} width={12} height={12} {...props} />;
    case "triangle":
      return <polygon points="10,3.5 16.5,15.5 3.5,15.5" {...props} />;
    case "star":
      return (
        <polygon
          points="10,3 11.8,7.6 16.7,7.8 12.9,10.9 14.2,15.7 10,13 5.8,15.7 7.1,10.9 3.3,7.8 8.2,7.6"
          {...props}
        />
      );
    case "diamond":
      return <polygon points="10,3 17,10 10,17 3,10" {...props} />;
  }
}

function Mark({
  mark,
  color,
  dy,
}: {
  mark: LetterMark;
  color: string;
  dy: number;
}): ReactElement | null {
  const line = {
    fill: "none",
    stroke: color,
    strokeWidth: 1.6,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };
  switch (mark) {
    case "none":
      return null;
    case "dot":
      return <circle cx={10} cy={10 + dy} r={1.8} fill={color} />;
    case "check":
      return <path d={`M7 ${10.2 + dy} L9.2 ${12.4 + dy} L13 ${7.8 + dy}`} {...line} />;
    case "cross":
      return (
        <path
          d={`M7.5 ${7.5 + dy} L12.5 ${12.5 + dy} M12.5 ${7.5 + dy} L7.5 ${12.5 + dy}`}
          {...line}
        />
      );
    case "reply":
      // ↩（返事の印）
      return (
        <path
          d={`M12.5 ${12.5 + dy} V${10 + dy} H7.5 M9.5 ${8 + dy} L7.5 ${10 + dy} L9.5 ${12 + dy}`}
          {...line}
        />
      );
  }
}

export function LetterIcon({
  letter,
  borderColor,
}: {
  letter: Letter;
  borderColor?: string;
}): ReactElement {
  const show = useShowRaftTerms();
  const kind = letterKind(letter.label);
  const label = withRaftTerm(kind.name, letterRaftTerm(letter.label), show);
  const count = kind.countBadge && letter.raft !== undefined ? letter.raft.entries.length : 0;
  // 三角は重心が下にあるので、印を少し下げる。
  const dy = kind.shape === "triangle" ? 2 : 0;
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox="0 0 20 20"
      width={20}
      height={20}
      className="letter-icon"
      data-label={letter.label}
      data-shape={kind.shape}
      data-mark={kind.mark}
    >
      <title>{label}</title>
      {borderColor !== undefined && (
        <circle
          cx={10}
          cy={10}
          r={9.1}
          fill="none"
          stroke={borderColor}
          strokeWidth={1.6}
          data-part="border"
        />
      )}
      <Shape kind={kind} />
      <Mark mark={kind.mark} color={kind.fill === "solid" ? WHITE : kind.color} dy={dy} />
      {count >= 1 && (
        <g data-part="count">
          <circle cx={15.5} cy={15.5} r={4.3} fill={STROKE} stroke={WHITE} strokeWidth={0.8} />
          <text x={15.5} y={17.9} fontSize={6.5} fontWeight="bold" textAnchor="middle" fill={WHITE}>
            {count}
          </text>
        </g>
      )}
    </svg>
  );
}
