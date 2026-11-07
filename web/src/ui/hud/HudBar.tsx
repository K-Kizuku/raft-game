import type { ReactElement } from "react";

// ゲーム画面のスロット「状況」（T-401）。中身は T-406 が書く。
// データは props で受けず、ストア（T-403）から読む（ADR-0010・DesignDoc §13.3）。
export function HudBar(): ReactElement {
  return <p>状況（準備中）</p>;
}
