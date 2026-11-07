// 画面の流れの状態（T-401・T-413・DesignDoc §13.6）。純粋な reducer。
import type { RoundContext, SessionHandle, StageSelection, StageSummary } from "./types.ts";

export type FlowState = { selection: StageSelection | null; round: RoundContext | null };

export type FlowAction =
  | { type: "stageSelected"; selection: StageSelection }
  | { type: "departed"; stage: StageSummary }
  | { type: "roundEnded" }
  | { type: "retried"; session: SessionHandle | null; seed: number | null };

export const initialFlowState: FlowState = { selection: null, round: null };

export function flowReducer(state: FlowState, action: FlowAction): FlowState {
  switch (action.type) {
    case "stageSelected":
      return { selection: action.selection, round: null };
    case "departed": {
      const same = state.selection !== null && state.selection.stageId === action.stage.id;
      const session = same ? (state.selection?.session ?? null) : null;
      const seed = same ? (state.selection?.seed ?? null) : null;
      // 1 つのセッションは 1 ラウンドだけに使う。
      return { selection: null, round: { stage: action.stage, session, seed, finished: false } };
    }
    case "roundEnded":
      if (state.round === null) {
        return state;
      }
      return { ...state, round: { ...state.round, finished: true } };
    case "retried":
      if (state.round === null) {
        return state;
      }
      // 同じ条件で作り直したセッション（App が作る。T-413）。
      return {
        ...state,
        round: {
          stage: state.round.stage,
          session: action.session,
          seed: action.seed,
          finished: false,
        },
      };
  }
}

// `stages` の中で `stageId` の次の要素。最後の要素・見つからないときは null。
export function nextStageOf(stages: readonly StageSummary[], stageId: string): StageSummary | null {
  const index = stages.findIndex((stage) => stage.id === stageId);
  if (index < 0) {
    return null;
  }
  return stages[index + 1] ?? null;
}
