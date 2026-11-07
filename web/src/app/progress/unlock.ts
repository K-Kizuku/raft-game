// ステージの解放の判定（T-703・FR-GAME-08・DesignDoc §13.9）。画面だけの規則で、サーバーは確かめない。
import type { StageCatalogEntry } from "../types.ts";
import type { StageProgress } from "./progress.ts";

export type LockState = { locked: false } | { locked: true; requires: StageCatalogEntry };

const UNLOCKED: LockState = { locked: false };

// unlock が null（省略も同じ）なら解放済み。unlock のステージが一覧に無くても解放済み（遊べなくなるのを
// 避ける）。unlock は 1 段の条件で、前のステージの前提はたどらない（循環はサーバーが検査している）。
export function lockStateOf(
  stage: StageCatalogEntry,
  stages: readonly StageCatalogEntry[],
  progress: StageProgress,
  unlockAll: boolean,
): LockState {
  if (unlockAll || stage.unlock === null || stage.unlock === undefined) {
    return UNLOCKED;
  }
  const requires = stages.find((s) => s.id === stage.unlock);
  if (requires === undefined || Object.hasOwn(progress.bestStars, requires.id)) {
    return UNLOCKED;
  }
  return { locked: true, requires };
}

// clearedStageId をクリアすると解放されるステージ（一覧の順）。
export function newlyUnlocked(
  clearedStageId: string,
  stages: readonly StageCatalogEntry[],
): StageCatalogEntry[] {
  return stages.filter((stage) => stage.unlock === clearedStageId);
}

// 開発のときだけ、URL の ?unlock-all ですべてのステージを解放する（保存はしない）。
// 本番のビルドでは import.meta.env.DEV が false になり、この分岐ごと取り除かれる。
export function wantsUnlockAll(): boolean {
  return (
    import.meta.env.DEV &&
    typeof window !== "undefined" &&
    new URLSearchParams(window.location.search).has("unlock-all")
  );
}
