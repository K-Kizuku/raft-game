// リザルト（仮。ボタンだけ）。スコア・統計・お話は T-408 がストアから読んで書く（ゲームデザイン §7.5）。
import type { ReactElement } from "react";

import type { StageSummary } from "../types.ts";

export type ResultScreenProps = {
  stage: StageSummary;
  nextStage: StageSummary | null;
  onRetry: () => void;
  onNextStage: () => void;
  onStageSelect: () => void;
};

export function ResultScreen({
  stage,
  nextStage,
  onRetry,
  onNextStage,
  onStageSelect,
}: ResultScreenProps): ReactElement {
  return (
    <div className="app-screen result">
      <h1 tabIndex={-1}>リザルト</h1>
      <p>{stage.title}</p>
      <p>結果は準備中です。</p>
      <div className="result-actions">
        <button type="button" onClick={onRetry}>
          同じ条件でもう一度
        </button>
        {nextStage !== null && (
          <button type="button" onClick={onNextStage}>
            次のステージ
          </button>
        )}
        <button type="button" onClick={onStageSelect}>
          ステージ選択
        </button>
      </div>
    </div>
  );
}
