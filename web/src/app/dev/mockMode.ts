// 開発用のモック（記録の再生）を使うかどうかの判定と、ステージの一覧への開発用のステージの追加（T-402b）。
// このファイルは静的に import してよい。モックの本体と記録（mockRound.ts）は動的 import でだけ読む。
import type { StageSummary } from "../types.ts";

// MODE が development（vite の開発サーバー。make dev）で、VITE_GAME_MOCK が "off" でないときだけ true。
export function isMockEnabled(env: { MODE: string; VITE_GAME_MOCK?: string | undefined }): boolean {
  return env.MODE === "development" && env.VITE_GAME_MOCK !== "off";
}

export const MOCK_ENABLED: boolean = isMockEnabled(import.meta.env);

// withMockStage は、一覧の読み込みが成功したら、その末尾に開発用のステージを足す（同じ id があれば足さない）。
// 読み込みの失敗はそのまま失敗にする。
// 一覧の 1 件の型（T を StageSummary に紹介文などを足した型にできる）は、load と loadMockStage でそろえる。
export function withMockStage<T extends StageSummary>(
  load: (signal: AbortSignal) => Promise<T[]>,
  loadMockStage: () => Promise<T>,
): (signal: AbortSignal) => Promise<T[]> {
  return async (signal) => {
    const stages = await load(signal);
    const mock = await loadMockStage();
    signal.throwIfAborted();
    if (stages.some((stage) => stage.id === mock.id)) {
      return stages;
    }
    return [...stages, mock];
  };
}
