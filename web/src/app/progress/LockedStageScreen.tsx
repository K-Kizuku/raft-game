// 未解放のステージ紹介（URL で直接開いた・ブラウザの戻る。T-703・DesignDoc §13.9）。セッションは作らない。
import type { ReactElement } from "react";

import { Link } from "../router/Link.tsx";
import type { StageCatalogEntry } from "../types.ts";

export function LockedStageScreen({
  stage,
  requires,
}: {
  stage: StageCatalogEntry;
  requires: StageCatalogEntry;
}): ReactElement {
  return (
    <div className="app-screen">
      <h1 tabIndex={-1}>このステージはまだ遊べません。</h1>
      <p>
        「{stage.title}」は、「{requires.title}」を★1 以上でクリアすると遊べます。
      </p>
      <Link to={{ name: "stageIntro", stageId: requires.id }}>「{requires.title}」へ</Link>
      <Link to={{ name: "stageSelect" }}>ステージ選択へ</Link>
    </div>
  );
}
