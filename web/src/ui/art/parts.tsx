// SVG の絵の部品（T-424a・docs/11-visual-design.md §7）。島・郵便船・封筒・方位記号・★。
// どれも <svg> の中に置く <g> で、原点を中心に描く（置き場所は x・y、大きさは scale）。
// 色は art.css のクラス（トークン）で塗り、ここには色を書かない。
import type { ReactElement } from "react";

import "./art.css";

export type Placement = {
  x?: number;
  y?: number;
  scale?: number;
  rotate?: number;
};

function placement({ x = 0, y = 0, scale = 1, rotate = 0 }: Placement): string {
  return `translate(${x} ${y}) rotate(${rotate}) scale(${scale})`;
}

export type IslandVariant = "house" | "lighthouse" | "trees" | "palm";

// 丸い島。浅瀬・波打ち際・砂浜・緑の丘に、目印（郵便局の家・灯台・木）を載せる。幅はおよそ 160。
export function Island({
  variant = "house",
  ...place
}: Placement & { variant?: IslandVariant }): ReactElement {
  return (
    <g transform={placement(place)}>
      <ellipse className="art-shoal" cx={0} cy={6} rx={80} ry={40} />
      <ellipse className="art-foam" cx={0} cy={6} rx={68} ry={33} />
      <path
        className="art-sand"
        d="M-58 6 C-60 -10 -36 -22 -4 -22 C30 -22 58 -12 58 4 C58 20 30 28 0 28 C-30 28 -56 22 -58 6 Z"
      />
      <path
        className="art-green"
        d="M-42 2 C-42 -22 -24 -40 -2 -42 C22 -44 42 -26 42 -4 C40 8 20 12 0 12 C-22 12 -40 10 -42 2 Z"
      />
      <IslandLandmark variant={variant} />
    </g>
  );
}

function IslandLandmark({ variant }: { variant: IslandVariant }): ReactElement {
  switch (variant) {
    case "house":
      // 郵便局（赤い屋根の小さな家）と木
      return (
        <g>
          <circle className="art-tree" cx={-24} cy={-12} r={9} />
          <circle className="art-tree" cx={26} cy={-8} r={7} />
          <rect className="art-paper" x={-10} y={-30} width={22} height={18} rx={2} />
          <path className="art-hull" d="M-14 -29 L1 -42 L16 -29 Z" />
          <rect className="art-sea-deep" x={-3} y={-22} width={6} height={10} rx={1} />
        </g>
      );
    case "lighthouse":
      // 赤と紙の縞の灯台
      return (
        <g>
          <circle className="art-tree" cx={-26} cy={-6} r={8} />
          <path className="art-paper" d="M-6 -10 L-3 -52 L5 -52 L8 -10 Z" />
          <path className="art-hull" d="M-5 -24 L-4 -36 L6 -36 L7 -24 Z" />
          <rect className="art-gold" x={-6} y={-62} width={12} height={10} rx={2} />
          <path className="art-hull" d="M-8 -62 L0 -70 L8 -62 Z" />
        </g>
      );
    case "trees":
      // 木の茂み
      return (
        <g>
          <circle className="art-tree" cx={-18} cy={-18} r={11} />
          <circle className="art-tree" cx={2} cy={-26} r={13} />
          <circle className="art-tree" cx={22} cy={-14} r={10} />
        </g>
      );
    case "palm":
      // ヤシの木と小さな桟橋
      return (
        <g>
          <path className="art-line" d="M2 -8 C0 -26 6 -42 14 -54" strokeWidth={5} />
          <path
            className="art-tree"
            d="M14 -54 C2 -66 -18 -62 -24 -48 C-10 -54 2 -54 14 -54 C24 -68 44 -66 50 -52 C36 -58 24 -58 14 -54 Z"
          />
          <path
            className="art-tree"
            d="M14 -54 C6 -50 -2 -40 -4 -28 C2 -38 8 -46 14 -54 C24 -50 32 -42 34 -30 C28 -40 22 -48 14 -54 Z"
          />
          <circle className="art-sand" cx={12} cy={-50} r={4} />
          <path className="art-paper" d="M38 8 L66 14 L64 20 L36 14 Z" />
        </g>
      );
  }
}

// 郵便船（赤い船体・帆に封筒の印・金の旗）。幅はおよそ 130、船底が y=22。
export function MailShip(place: Placement): ReactElement {
  return (
    <g transform={placement(place)}>
      <path
        className="art-wake"
        d="M-70 18 C-90 14 -104 20 -122 16 M-66 26 C-84 28 -96 34 -112 32"
      />
      <path className="art-line" d="M-6 0 L-6 -86" />
      <path className="art-paper" d="M-2 -82 C26 -66 38 -36 36 -10 L-2 -10 Z" />
      <path className="art-paper" d="M-10 -74 C-30 -58 -38 -34 -36 -10 L-10 -10 Z" />
      <rect className="art-paper" x={6} y={-48} width={20} height={14} rx={2} />
      <path className="art-line" d="M6 -48 L16 -40 L26 -48" />
      <path className="art-gold" d="M-6 -86 L14 -80 L-6 -74 Z" />
      <path className="art-hull" d="M-60 -8 L62 -8 L48 18 C20 24 -20 24 -50 18 Z" />
      <path className="art-line" d="M-54 4 L56 4" />
      <circle className="art-paper" cx={-30} cy={-1} r={3.5} />
      <circle className="art-paper" cx={-16} cy={-1} r={3.5} />
    </g>
  );
}

// 封筒（赤い封蝋）。幅はおよそ 44。
export function Envelope(place: Placement): ReactElement {
  return (
    <g transform={placement(place)}>
      <rect className="art-paper" x={-22} y={-15} width={44} height={30} rx={4} />
      <path className="art-line" d="M-21 -13 L0 3 L21 -13" />
      <circle className="art-hull" cx={0} cy={3} r={5} />
    </g>
  );
}

// 方位記号（北が郵便の赤、斜めの 4 方向が封筒の金）。半径はおよそ 60。
export function CompassRose(place: Placement): ReactElement {
  const main = "0,-54 9,-9 0,0 -9,-9";
  const minor = "0,-34 6,-6 0,0 -6,-6";
  return (
    <g transform={placement(place)}>
      <circle className="art-paper" cx={0} cy={0} r={60} />
      <circle className="art-line" cx={0} cy={0} r={48} strokeDasharray="3 7" />
      {[45, 135, 225, 315].map((angle) => (
        <polygon key={angle} className="art-gold" points={minor} transform={`rotate(${angle})`} />
      ))}
      {[0, 90, 180, 270].map((angle) => (
        <polygon
          key={angle}
          className={angle === 0 ? "art-hull" : "art-paper"}
          points={main}
          transform={`rotate(${angle})`}
        />
      ))}
      <circle className="art-gold" cx={0} cy={0} r={6} />
      {/* 北の印（N の字を線で描く。文字にすると支援技術やテストの本文に混ざるため） */}
      <path className="art-line" d="M-6 -66 L-6 -80 L6 -66 L6 -80" />
    </g>
  );
}

// ★（封筒の金）。半径はおよそ 20。
export function Star(place: Placement): ReactElement {
  return (
    <g transform={placement(place)}>
      <polygon
        className="art-gold"
        points="0,-20 5.9,-8.1 19,-6.2 9.5,3.1 11.8,16.2 0,10 -11.8,16.2 -9.5,3.1 -19,-6.2 -5.9,-8.1"
      />
    </g>
  );
}
