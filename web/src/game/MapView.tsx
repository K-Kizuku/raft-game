import type { ReactElement } from "react";

// ゲーム画面のスロット「マップ」（T-401）。中身は T-404 が書く。
// データは props で受けず、ストア（T-403）から読む（ADR-0010・DesignDoc §13.3）。
export function MapView(): ReactElement {
  return <p>マップ（準備中）</p>;
}
