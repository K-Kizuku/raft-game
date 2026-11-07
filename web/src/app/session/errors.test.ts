import { describe, expect, it } from "vitest";

import {
  SESSION_ERROR_MESSAGES,
  SessionCreateError,
  sessionErrorKindOf,
  type SessionErrorKind,
} from "./errors.ts";

describe("SESSION_ERROR_MESSAGES", () => {
  it("E-1 8 つの種類の文言", () => {
    expect(SESSION_ERROR_MESSAGES).toEqual({
      budget_exhausted: "本日の受付は終了しました。",
      sessions_disabled: "ただいま受付を停止しています。",
      rate_limited: "少し待ってからお試しください。",
      turnstile_failed: "確認できませんでした。もう一度お試しください。",
      challenge_failed: "確認の画面を表示できませんでした。もう一度お試しください。",
      forbidden_origin:
        "このページからはステージを始められません。ページを読み込み直してください。",
      bad_request: "このステージは始められませんでした。ステージの一覧を読み込み直してください。",
      unavailable: "ステージを始められませんでした。もう一度お試しください。",
    });
  });

  it.each<[SessionErrorKind, string]>([
    ["budget_exhausted", "本日の受付は終了しました"],
    ["sessions_disabled", "ただいま受付を停止しています"],
    ["rate_limited", "少し待ってからお試しください"],
    ["turnstile_failed", "もう一度お試しください"],
  ])("E-1 %s はプロトコル §2 の「画面の表示」の文を含む", (kind, text) => {
    expect(SESSION_ERROR_MESSAGES[kind]).toContain(text);
  });
});

describe("sessionErrorKindOf", () => {
  it.each<[number, string | undefined, SessionErrorKind]>([
    [503, "budget_exhausted", "budget_exhausted"],
    [503, "sessions_disabled", "sessions_disabled"],
    [429, "rate_limited", "rate_limited"],
    [429, undefined, "rate_limited"],
    [403, "turnstile_failed", "turnstile_failed"],
    [403, "forbidden_origin", "forbidden_origin"],
    [400, "bad_request", "bad_request"],
    [403, "challenge_failed", "unavailable"],
    [429, "something_else", "unavailable"],
    [500, "internal_error", "unavailable"],
    [500, "constructor", "unavailable"],
    [502, undefined, "unavailable"],
  ])("%i %s → %s", (status, code, kind) => {
    expect(sessionErrorKindOf(status, code)).toBe(kind);
  });
});

describe("SessionCreateError", () => {
  it("name・kind・message", () => {
    const error = new SessionCreateError("budget_exhausted");
    expect(error).toBeInstanceOf(Error);
    expect(error.name).toBe("SessionCreateError");
    expect(error.kind).toBe("budget_exhausted");
    expect(error.message).toBe("セッションを作れませんでした（budget_exhausted）");
  });
});
