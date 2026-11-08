// 依頼の進み具合のステッパー（T-431）。5 つの丸（受付・提案・記録・確定・返事）と、止まっている理由の一言。
// 丸は色だけでなく印（✓・●・✕）と線の太さでも状態を示す。compact（HUD）では段階の名前を読み上げだけにする。
import type { ReactElement } from "react";

import { WithRaftTerm } from "../terms/RaftTerm.tsx";
import { PROGRESS_STEPS, requestProgress, stepState } from "./requestProgress.ts";
import type { ProgressStepState } from "./requestProgress.ts";
import "./RequestStepper.css";

const STATE_MARK: Readonly<Record<ProgressStepState, string>> = {
  done: "✓",
  current: "●",
  todo: "",
  failed: "✕",
};

const STATE_TEXT: Readonly<Record<ProgressStepState, string>> = {
  done: "済み",
  current: "いまここ",
  todo: "まだ",
  failed: "失敗",
};

export function RequestStepper({
  status,
  compact = false,
}: {
  status: string;
  compact?: boolean;
}): ReactElement {
  const progress = requestProgress(status);
  return (
    <div
      className={compact ? "request-progress is-compact" : "request-progress"}
      data-outcome={progress.outcome}
    >
      <ol className="request-stepper" aria-label="依頼の進み具合">
        {PROGRESS_STEPS.map((step, index) => {
          const state = stepState(progress, index);
          return (
            <li
              key={step.id}
              className="request-step"
              data-step={step.id}
              data-state={state}
              aria-current={state === "current" ? "step" : undefined}
            >
              <span aria-hidden="true" className="request-step-dot">
                {STATE_MARK[state]}
              </span>
              <span className={compact ? "visually-hidden" : "request-step-label"}>
                <WithRaftTerm raft={step.raft}>{step.game}</WithRaftTerm>
              </span>
              <span className="visually-hidden">（{STATE_TEXT[state]}）</span>
            </li>
          );
        })}
      </ol>
      <p className="request-progress-reason">{progress.reason}</p>
    </div>
  );
}
