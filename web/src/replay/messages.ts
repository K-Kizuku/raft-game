// リプレイの誤りの文言（ADR-0028 R4・T-720d）。純粋な関数。
// stage_mismatch・diverged の文言は ADR-0028 の「stageHash の不一致」のもの。
import type { ReplayErrorCode, ReplayFailure } from "./response.ts";

const TEXT: Readonly<Record<Exclude<ReplayErrorCode, "unknown_stage">, string>> = {
  too_large:
    "ファイルが大きすぎます（1 MiB まで）。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
  bad_input:
    "入力ログとして読めませんでした。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
  stage_mismatch:
    "この入力ログは、今と違うステージ定義で記録されました。今の版では再生できません。",
  diverged: "版が違うため再現できませんでした。",
  not_started: "このラウンドは始まる前に終わっているため、再生するものがありません。",
  internal: "リプレイの途中で問題が起きました。ページを読み込み直してお試しください。",
  load_failed: "リプレイの部品を読み込めませんでした。回線を確かめて、もう一度お試しください。",
};

export type ReplayErrorText = Readonly<{
  text: string;
  detail: string | null; // 版の欄（stage_mismatch・diverged のときだけ）
}>;

// replayErrorText は失敗の文言と、版の欄（値がない欄は省く。どれも無ければ null）を返す。
export function replayErrorText(outcome: ReplayFailure): ReplayErrorText {
  const text =
    outcome.code === "unknown_stage"
      ? `この入力ログのステージ（${outcome.stageId ?? "不明"}）は、今の版にありません。`
      : TEXT[outcome.code];
  if (outcome.code !== "stage_mismatch" && outcome.code !== "diverged") {
    return { text, detail: null };
  }
  const parts: string[] = [];
  if (outcome.stageId !== null) {
    parts.push(`ステージ: ${outcome.stageId}`);
  }
  if (outcome.logBuild !== null) {
    parts.push(`記録した版: ${outcome.logBuild}`);
  }
  if (outcome.build !== null) {
    parts.push(`今の版: ${outcome.build}`);
  }
  return { text, detail: parts.length > 0 ? parts.join("・") : null };
}
