// ステージ選択（FR-UI-02 の一覧の部分）。
// 選んだステージを onSelect で渡すだけ。Turnstile とセッションの作成は App が行う（T-413）。
// 作成中（busy）は、すべての「<タイトル>を選ぶ」を押せなくする。
// 各ステージに紹介文・出典区分・出典を出す（T-416）。学べることは紹介の画面だけに出す。
import type { ReactElement } from "react";

import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import { StageIntroText } from "../stage-intro/StageIntroText.tsx";
import type { StageCatalogState, StageSummary } from "../types.ts";

export type StageSelectScreenProps = {
  catalog: StageCatalogState;
  onRetry: () => void;
  onSelect: (stage: StageSummary) => void;
  busy: boolean;
};

export function StageSelectScreen(props: StageSelectScreenProps): ReactElement {
  return (
    <div className="app-screen stage-select">
      <h1 tabIndex={-1}>ステージ選択</h1>
      <StageCatalogBody {...props} />
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}

function StageCatalogBody({
  catalog,
  onRetry,
  onSelect,
  busy,
}: StageSelectScreenProps): ReactElement {
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
              <StageIntroText intro={stage.intro} />
              <p>
                <span>島 {stage.islands}</span>・<span>船 {stage.ferries} 隻</span>・
                <span>制限時間 {formatDuration(stage.durationMs)}</span>
              </p>
              <button type="button" disabled={busy} onClick={() => onSelect(stage)}>
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
