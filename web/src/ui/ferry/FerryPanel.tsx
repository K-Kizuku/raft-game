import type { ReactElement } from "react";

// ゲーム画面のスロット「フェリー操作」（T-401）。中身は T-405 が書く。
// データは props で受けず、ストア（T-403）から読む（ADR-0010・DesignDoc §13.3）。
export function FerryPanel(): ReactElement {
  return <p>フェリー操作（準備中）</p>;
}
