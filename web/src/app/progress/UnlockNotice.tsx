// 解放の知らせ（初めてのクリアで新しいステージが開いたとき、リザルトに出す。T-703・DesignDoc §13.9）。
import type { ReactElement } from "react";

import type { StageCatalogEntry } from "../types.ts";
import "./progress.css";

export function UnlockNotice({
  stages,
}: {
  stages: readonly StageCatalogEntry[];
}): ReactElement | null {
  if (stages.length === 0) {
    return null;
  }
  const titles = stages.map((stage) => `「${stage.title}」`).join("、");
  return (
    <p role="status" className="unlock-notice">
      🎉{titles}が遊べるようになりました！
    </p>
  );
}
