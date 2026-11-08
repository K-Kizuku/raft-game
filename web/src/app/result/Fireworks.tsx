// 花火大会の演出（T-408・T-437・FR-SCORE-03・ゲームデザイン §5.3）。★2 以上のリザルトで出す。
// 素材（画像・効果音）は人の作業（#131）。届いたら、このコンポーネントの中だけを差し替える
// （ゲームデザイン §9「素材は後から差し替えられる作り」）。今は絵文字を使わず、CSS だけで描く
// （打ち上げ → 開く → 散る。Fireworks.css）。見出しの両側の印は SVG のアイコン。
// 位置・色・遅れは固定の値（毎回同じ見た目で、テストできる）。タイマーを使わず、CSS で 3 回くり返して止まる。
import type { ReactElement } from "react";

import { FireworkIcon } from "../../ui/art/icons/Firework.tsx";
import type { ColorTokenName } from "../../ui/theme/tokens.ts";
import "./Fireworks.css";

// トークンにない火花の色。値は Fireworks.css の .fireworks-sky のローカルの変数に置く。
export type FireworkLocalColor =
  "--firework-rose" | "--firework-orange" | "--firework-violet" | "--firework-amber";

export type FireworkBurst = Readonly<{
  x: number; // 画面に対する %（0〜100 の整数）
  y: number; // 画面に対する %（0〜100 の整数）
  delayMs: number; // 0〜1400
  tone: ColorTokenName | FireworkLocalColor; // 火花の色（CSS の変数の名前）
}>;

// 火花の色は、紙の地の上でも映える温かい色（郵便の赤・封筒の金を中心に、区別のための数色）。
// トークンにある色はトークンを使う。
export const FIREWORK_BURSTS: readonly FireworkBurst[] = [
  { x: 18, y: 22, delayMs: 0, tone: "--color-postal" },
  { x: 72, y: 16, delayMs: 200, tone: "--color-gold" },
  { x: 45, y: 30, delayMs: 400, tone: "--firework-rose" },
  { x: 86, y: 40, delayMs: 600, tone: "--color-sea-deep" },
  { x: 10, y: 48, delayMs: 800, tone: "--firework-orange" },
  { x: 58, y: 12, delayMs: 1000, tone: "--color-success" },
  { x: 32, y: 60, delayMs: 1200, tone: "--firework-violet" },
  { x: 66, y: 54, delayMs: 1400, tone: "--firework-amber" },
];

export function Fireworks(): ReactElement {
  return (
    <section className="fireworks" aria-label="花火大会">
      <p className="fireworks-banner">
        <FireworkIcon /> 花火大会 <FireworkIcon />
      </p>
      <div className="fireworks-sky" aria-hidden="true">
        {FIREWORK_BURSTS.map((burst, i) => (
          <span
            key={i}
            className="firework"
            data-burst={i}
            style={{
              left: `${String(burst.x)}%`,
              top: `${String(burst.y)}%`,
              color: `var(${burst.tone})`,
              animationDelay: `${String(burst.delayMs)}ms`,
            }}
          >
            <span className="firework-trail" />
            <span className="firework-bloom" />
          </span>
        ))}
      </div>
    </section>
  );
}
