// ステージ選択（FR-UI-02 の一覧の部分）。
// Turnstile とセッションの作成は T-413 がこの中に足し、`session` を渡す。
import type { ReactElement } from "react";

import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import type { StageCatalogState, StageSelection } from "../types.ts";

export type StageSelectScreenProps = {
  catalog: StageCatalogState;
  onRetry: () => void;
  onSelect: (selection: StageSelection) => void;
};

export function StageSelectScreen({
  catalog,
  onRetry,
  onSelect,
}: StageSelectScreenProps): ReactElement {
  return (
    <div className="app-screen stage-select">
      <h1 tabIndex={-1}>ステージ選択</h1>
      <StageCatalogBody catalog={catalog} onRetry={onRetry} onSelect={onSelect} />
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}

function StageCatalogBody({ catalog, onRetry, onSelect }: StageSelectScreenProps): ReactElement {
  switch (catalog.status) {
    case "loading":
      return <p role="status">読み込み中…</p>;
    case "error":
      return <StageCatalogError onRetry={onRetry} />;
    case "ready":
      if (catalog.stages.length === 0) {
        return <p>遊べるステージがありません。</p>;
      }
      return (
        <ul className="stage-select-list">
          {catalog.stages.map((stage) => (
            <li key={stage.id} className="stage-select-item">
              <h2>{stage.title}</h2>
              <p>{stage.region}</p>
              <p>
                <span>島 {stage.islands}</span>・<span>船 {stage.ferries} 隻</span>・
                <span>制限時間 {formatDuration(stage.durationMs)}</span>
              </p>
              <button type="button" onClick={() => onSelect({ stageId: stage.id, session: null })}>
                {stage.title}を選ぶ
              </button>
            </li>
          ))}
        </ul>
      );
  }
}

function StageCatalogError({ onRetry }: { onRetry: () => void }): ReactElement {
  return (
    <div role="alert">
      <p>ステージの一覧を読み込めませんでした。</p>
      <button type="button" onClick={onRetry}>
        もう一度読み込む
      </button>
    </div>
  );
}
