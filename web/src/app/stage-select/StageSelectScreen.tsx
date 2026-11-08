// ステージ選択（FR-UI-02 の一覧の部分）。
// 選んだステージを onSelect で渡すだけ。Turnstile とセッションの作成は App が行う（T-413）。
// 作成中（busy）は、すべての「<タイトル>を選ぶ」を押せなくする。
// 各ステージに紹介文・出典区分・出典を出す（T-416）。学べることは紹介の画面だけに出す。
// どのステージも最初から選べる。クリア済みのステージには最高の★を印として出す（T-703b・DesignDoc §13.9）。
import type { ReactElement } from "react";

import { formatDuration } from "../format.ts";
import { useStageProgress, type StageProgress } from "../progress/progress.ts";
import { Link } from "../router/Link.tsx";
import { StageIntroText } from "../stage-intro/StageIntroText.tsx";
import type { StageCatalogEntry, StageCatalogState, StageSummary } from "../types.ts";
import "./StageSelectScreen.css";
import "../progress/progress.css";

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
  const progress = useStageProgress();
  switch (catalog.status) {
    case "loading":
      return (
        <p role="status" className="stage-select-message">
          読み込み中…
        </p>
      );
    case "error":
      return <StageCatalogError onRetry={onRetry} />;
    case "ready":
      if (catalog.stages.length === 0) {
        return <p className="stage-select-message">遊べるステージがありません。</p>;
      }
      return (
        <ul className="stage-select-list">
          {catalog.stages.map((stage) => (
            <StageCard
              key={stage.id}
              stage={stage}
              progress={progress}
              onSelect={onSelect}
              busy={busy}
            />
          ))}
        </ul>
      );
  }
}

function StageCard({
  stage,
  progress,
  onSelect,
  busy,
}: {
  stage: StageCatalogEntry;
  progress: StageProgress;
  onSelect: (stage: StageSummary) => void;
  busy: boolean;
}): ReactElement {
  // 保存した記録が壊れていても選べる（印を出さないだけ。progress.ts が壊れた要素を捨てる）。
  const stars = progress.bestStars[stage.id];
  return (
    <li className="stage-select-item">
      <h2>{stage.title}</h2>
      <p className="stage-select-region">{stage.region}</p>
      <StageIntroText intro={stage.intro} />
      <p className="stage-select-facts">
        <span className="stage-select-fact">島 {stage.islands}</span>・
        <span className="stage-select-fact">船 {stage.ferries} 隻</span>・
        <span className="stage-select-fact">制限時間 {formatDuration(stage.durationMs)}</span>
      </p>
      {stars !== undefined && (
        <p className="stage-select-cleared" aria-label={`クリア済み、★ ${stars} / 3`}>
          クリア済み ★{stars}
        </p>
      )}
      <button
        type="button"
        className="btn btn-primary stage-select-choose"
        disabled={busy}
        onClick={() => onSelect(stage)}
      >
        {stage.title}を選ぶ
      </button>
    </li>
  );
}

function StageCatalogError({ onRetry }: { onRetry: () => void }): ReactElement {
  return (
    <div role="alert" className="stage-select-message stage-select-error">
      <p>ステージの一覧を読み込めませんでした。</p>
      <button type="button" className="btn" onClick={onRetry}>
        もう一度読み込む
      </button>
    </div>
  );
}
