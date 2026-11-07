// 花火大会の演出（T-408・FR-SCORE-03・ゲームデザイン §5.3）。★2 以上のリザルトで出す。
// 素材（画像・効果音）は人の作業（#131）。届いたら、このコンポーネントの中だけを差し替える
// （ゲームデザイン §9「素材は後から差し替えられる作り」）。今は CSS のアニメーションと絵文字だけで描く。
// 位置・色・遅れは固定の値（毎回同じ見た目で、テストできる）。タイマーを使わず、CSS で 3 回くり返して止まる。
import type { ReactElement } from "react";

import "./Fireworks.css";

export type FireworkBurst = Readonly<{
  x: number; // 画面に対する %（0〜100 の整数）
  y: number; // 画面に対する %（0〜100 の整数）
  delayMs: number; // 0〜1400
  emoji: "🎆" | "🎇";
  color: string;
}>;

export const FIREWORK_BURSTS: readonly FireworkBurst[] = [
  { x: 18, y: 22, delayMs: 0, emoji: "🎆", color: "#F97316" },
  { x: 72, y: 16, delayMs: 200, emoji: "🎇", color: "#FACC15" },
  { x: 45, y: 30, delayMs: 400, emoji: "🎆", color: "#EC4899" },
  { x: 86, y: 40, delayMs: 600, emoji: "🎇", color: "#22D3EE" },
  { x: 10, y: 48, delayMs: 800, emoji: "🎆", color: "#A855F7" },
  { x: 58, y: 12, delayMs: 1000, emoji: "🎇", color: "#4ADE80" },
  { x: 32, y: 60, delayMs: 1200, emoji: "🎆", color: "#F43F5E" },
  { x: 66, y: 54, delayMs: 1400, emoji: "🎇", color: "#60A5FA" },
];

export function Fireworks(): ReactElement {
  return (
    <section className="fireworks" aria-label="花火大会">
      <p className="fireworks-banner">
        <span aria-hidden="true">🎆</span> 花火大会 <span aria-hidden="true">🎆</span>
      </p>
      <div className="fireworks-sky" aria-hidden="true">
        {FIREWORK_BURSTS.map((burst, i) => (
          <span
            key={i}
            className="firework"
            data-burst={i}
            style={{
              left: `${burst.x}%`,
              top: `${burst.y}%`,
              color: burst.color,
              animationDelay: `${burst.delayMs}ms`,
            }}
          >
            {burst.emoji}
          </span>
        ))}
      </div>
    </section>
  );
}
