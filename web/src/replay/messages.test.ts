import { describe, expect, it } from "vitest";

import { replayErrorText } from "./messages.ts";
import { replayFailure, type ReplayErrorCode } from "./response.ts";

describe("replayErrorText", () => {
  it.each<[ReplayErrorCode, string]>([
    [
      "too_large",
      "ファイルが大きすぎます（1 MiB まで）。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
    ],
    [
      "bad_input",
      "入力ログとして読めませんでした。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
    ],
    ["not_started", "このラウンドは始まる前に終わっているため、再生するものがありません。"],
    ["internal", "リプレイの途中で問題が起きました。ページを読み込み直してお試しください。"],
    [
      "load_failed",
      "リプレイの部品を読み込めませんでした。回線を確かめて、もう一度お試しください。",
    ],
  ])("%s の文言（版の欄なし）", (code, text) => {
    expect(
      replayErrorText(replayFailure(code, "x", { build: "b", stageId: "s", logBuild: "l" })),
    ).toEqual({ text, detail: null });
  });

  it("unknown_stage はステージの ID を入れる", () => {
    expect(replayErrorText(replayFailure("unknown_stage", "x", { stageId: "stage9" }))).toEqual({
      text: "この入力ログのステージ（stage9）は、今の版にありません。",
      detail: null,
    });
  });

  it("stage_mismatch は版の欄を添える", () => {
    expect(
      replayErrorText(
        replayFailure("stage_mismatch", "x", {
          build: "git:1234567",
          stageId: "stage2",
          logBuild: "git:abcdef0",
        }),
      ),
    ).toEqual({
      text: "この入力ログは、今と違うステージ定義で記録されました。今の版では再生できません。",
      detail: "ステージ: stage2・記録した版: git:abcdef0・今の版: git:1234567",
    });
  });

  it("diverged は値がない欄を省く", () => {
    expect(replayErrorText(replayFailure("diverged", "x", { build: "git:1234567" }))).toEqual({
      text: "版が違うため再現できませんでした。",
      detail: "今の版: git:1234567",
    });
    expect(replayErrorText(replayFailure("diverged", "x"))).toEqual({
      text: "版が違うため再現できませんでした。",
      detail: null,
    });
  });
});
