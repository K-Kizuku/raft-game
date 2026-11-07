// タイトルの絵（T-424a）。水彩風の海に丸い島が 4 つ、郵便船、封筒、海図の点線と方位記号。
// 人の素材（Issue 131）が届くまでの代わりに、コードで描く。飾りなので aria-hidden。
// 1600×900 で描き、画面いっぱいに切り抜いて敷く（preserveAspectRatio="xMidYMid slice"）。
// 中央（タイトルとメニューが載るところ）は明るくし、絵の主役は四隅に置く。
import { useId, type ReactElement } from "react";

import { CompassRose, Envelope, Island, MailShip } from "./parts.tsx";

// 波の印（小さな弧を 2 つ）を置く場所。
const WAVES_A: readonly (readonly [number, number])[] = [
  [140, 360],
  [470, 250],
  [560, 610],
  [1040, 300],
  [1460, 560],
  [980, 690],
  [160, 820],
];
const WAVES_B: readonly (readonly [number, number])[] = [
  [380, 470],
  [700, 160],
  [1180, 470],
  [1500, 120],
  [620, 760],
  [1100, 840],
  [90, 120],
];

function Waves({ points }: { points: readonly (readonly [number, number])[] }): ReactElement {
  return (
    <g>
      {points.map(([x, y]) => (
        <path
          key={`${x}-${y}`}
          className="title-art-wave"
          d={`M${x - 30} ${y} q10 -9 20 0 t20 0 t20 0`}
        />
      ))}
    </g>
  );
}

export function TitleArt({ className }: { className?: string }): ReactElement {
  // 同じ画面に 2 つ描いても参照がぶつからないよう、グラデーション・フィルターの id は useId から作る。
  const id = `art-${useId().replace(/[^A-Za-z0-9_-]/g, "")}`;
  const sea = `${id}-sea`;
  const glow = `${id}-glow`;
  const watercolor = `${id}-wc`;
  const seaClip = `${id}-clip`;
  return (
    <svg
      className={className === undefined ? "art title-art" : `art title-art ${className}`}
      viewBox="0 0 1600 900"
      preserveAspectRatio="xMidYMid slice"
      aria-hidden="true"
      focusable="false"
    >
      <defs>
        <linearGradient id={sea} x1="0" y1="0" x2="0.3" y2="1">
          <stop className="title-art-stop-light" offset="0" />
          <stop className="title-art-stop-mid" offset="1" />
        </linearGradient>
        <radialGradient id={glow} cx="0.5" cy="0.5" r="0.5">
          <stop className="title-art-stop-paper" offset="0" />
          <stop className="title-art-stop-paper" offset="0.55" />
          <stop className="title-art-stop-paper" offset="1" stopOpacity={0} />
        </radialGradient>
        <clipPath id={seaClip}>
          <rect x={36} y={36} width={1528} height={828} rx={40} />
        </clipPath>
        {/* 水彩のにじみ: ノイズで縁をゆらす */}
        <filter id={watercolor} x="-5%" y="-5%" width="110%" height="110%">
          <feTurbulence type="fractalNoise" baseFrequency="0.012" numOctaves={3} seed={7} />
          <feDisplacementMap
            in="SourceGraphic"
            scale={30}
            xChannelSelector="R"
            yChannelSelector="G"
          />
        </filter>
      </defs>

      {/* 紙の地と、にじんだ海 */}
      <rect className="title-art-paper" x={0} y={0} width={1600} height={900} />
      <g filter={`url(#${watercolor})`}>
        <rect x={36} y={36} width={1528} height={828} rx={40} fill={`url(#${sea})`} />
        <g clipPath={`url(#${seaClip})`}>
          <ellipse className="title-art-wash-mid" cx={260} cy={760} rx={360} ry={170} />
          <ellipse className="title-art-wash-mid" cx={1400} cy={180} rx={320} ry={150} />
          <ellipse className="title-art-wash-deep" cx={1300} cy={800} rx={420} ry={120} />
          <ellipse className="title-art-wash-deep" cx={160} cy={140} rx={260} ry={110} />
          <ellipse className="title-art-wash-light" cx={1240} cy={520} rx={220} ry={90} />
          <ellipse className="title-art-wash-light" cx={420} cy={420} rx={200} ry={80} />
        </g>
      </g>

      {/* 海図の経緯線と枠 */}
      <path
        className="title-art-grid"
        d="M320 36 V864 M640 36 V864 M960 36 V864 M1280 36 V864 M36 225 H1564 M36 450 H1564 M36 675 H1564"
      />
      <rect className="title-art-frame" x={20} y={20} width={1560} height={860} rx={28} />
      <rect className="title-art-frame-ticks" x={28} y={28} width={1544} height={844} rx={22} />

      {/* 中央を明るく（タイトルとメニューの下地） */}
      <ellipse
        className="title-art-glow"
        cx={800}
        cy={450}
        rx={520}
        ry={360}
        fill={`url(#${glow})`}
      />

      {/* 航路（海図の点線） */}
      <path className="title-art-route" d="M290 290 C200 420 250 560 320 620" />
      <path className="title-art-route" d="M430 700 C680 860 960 860 1200 740" />
      <path className="title-art-route" d="M1330 650 C1450 540 1460 390 1380 310" />
      <path className="title-art-route" d="M340 190 C660 80 1000 80 1260 190" />

      <g className="title-art-waves-a">
        <Waves points={WAVES_A} />
      </g>
      <g className="title-art-waves-b">
        <Waves points={WAVES_B} />
      </g>

      {/* 島 */}
      <Island x={240} y={230} scale={1.25} variant="house" />
      <Island x={330} y={690} scale={1.55} variant="lighthouse" />
      <Island x={1370} y={240} scale={1.35} variant="trees" />
      <Island x={1290} y={705} scale={1.2} variant="palm" />

      {/* 方位記号 */}
      <CompassRose x={1480} y={790} scale={0.9} rotate={-8} />

      {/* 封筒（ふわふわ浮かぶ） */}
      <g transform="translate(560 120)">
        <g className="title-art-float">
          <Envelope rotate={-12} scale={1.3} />
        </g>
      </g>
      <g transform="translate(1470 450)">
        <g className="title-art-float title-art-float-2">
          <Envelope rotate={14} scale={1.2} />
        </g>
      </g>
      <g transform="translate(150 470)">
        <g className="title-art-float title-art-float-3">
          <Envelope rotate={-6} scale={1.1} />
        </g>
      </g>

      {/* 郵便船（航路の上をゆっくり行き来する） */}
      <g transform="translate(790 812)">
        <g className="title-art-sail">
          <g className="title-art-ship">
            <MailShip scale={0.95} />
          </g>
        </g>
      </g>
    </svg>
  );
}
