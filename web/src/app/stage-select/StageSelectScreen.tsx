// ステージ選択（FR-UI-02 の一覧の部分）。
// 選んだステージを onSelect で渡すだけ。Turnstile とセッションの作成は App が行う（T-413）。
// 作成中（busy）は、すべての「<タイトル>を選ぶ」を押せなくする。
// 各ステージに紹介文・出典区分・出典を出す（T-416）。学べることは紹介の画面だけに出す。
// 未解放のステージは鍵つきで押せず、クリア済みのステージには最高の★を出す（T-703・DesignDoc §13.9）。
import { useId, type ReactElement } from "react";

import { formatDuration } from "../format.ts";
import { useStageProgress, type StageProgress } from "../progress/progress.ts";
import { lockStateOf, wantsUnlockAll } from "../progress/unlock.ts";
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
  const unlockAll = wantsUnlockAll();
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
              stages={catalog.stages}
              progress={progress}
              unlockAll={unlockAll}
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
  stages,
  progress,
  unlockAll,
  onSelect,
  busy,
}: {
  stage: StageCatalogEntry;
  stages: readonly StageCatalogEntry[];
  progress: StageProgress;
  unlockAll: boolean;
  onSelect: (stage: StageSummary) => void;
  busy: boolean;
}): ReactElement {
  const lockId = useId();
  const lock = lockStateOf(stage, stages, progress, unlockAll);
  const stars = progress.bestStars[stage.id];
  return (
    <li className="stage-select-item" data-locked={lock.locked ? "true" : undefined}>
      <h2>
        {lock.locked && <span aria-hidden="true">🔒</span>}
        {stage.title}
      </h2>
      <p className="stage-select-region">{stage.region}</p>
      <StageIntroText intro={stage.intro} />
      <p className="stage-select-facts">
        <span className="stage-select-fact">島 {stage.islands}</span>・
        <span className="stage-select-fact">船 {stage.ferries} 隻</span>・
        <span className="stage-select-fact">制限時間 {formatDuration(stage.durationMs)}</span>
      </p>
      {lock.locked ? (
        <p className="stage-select-lock" id={lockId}>
          「{lock.requires.title}」を★1 以上でクリアすると遊べます。
        </p>
      ) : (
        stars !== undefined && (
          <p className="stage-select-cleared" aria-label={`クリア済み、★ ${stars} / 3`}>
            クリア済み ★{stars}
          </p>
        )
      )}
      <button
        type="button"
        className="btn btn-primary stage-select-choose"
        disabled={busy || lock.locked}
        aria-describedby={lock.locked ? lockId : undefined}
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
