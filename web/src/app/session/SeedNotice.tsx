// ステージ紹介の URL の `?seed=` の知らせ（T-705・FR-GAME-06）。App が紹介の画面の後ろに置く。
// 見た目は SessionNotice と同じクラス（session-notice）を使い、CSS を足さない。
import type { ReactElement } from "react";

import type { SeedParam } from "./seedParam.ts";

export function SeedNotice({ param }: { param: SeedParam }): ReactElement | null {
  switch (param.status) {
    case "none":
      return null;
    case "ok":
      return (
        <p role="status" className="session-notice seed-notice">
          シード {param.seed} で遊びます。同じシードなら、依頼と嵐が同じ時刻に起きます。
        </p>
      );
    case "invalid":
      return (
        <p role="status" className="session-notice seed-notice">
          シードの指定（?seed=）が正しくないため、新しいシードで遊びます。
        </p>
      );
  }
}
