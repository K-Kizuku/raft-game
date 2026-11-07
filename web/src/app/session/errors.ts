// セッションの作成の失敗の種類と文言（T-413・プロトコル §2 の「画面の表示」）。
// 例外のメッセージに、トークン・要求の本文・応答の中身を含めない（NFR-08）。

export type SessionErrorKind =
  | "budget_exhausted"
  | "sessions_disabled"
  | "rate_limited"
  | "turnstile_failed"
  | "challenge_failed"
  | "forbidden_origin"
  | "bad_request"
  | "unavailable";

export const SESSION_ERROR_MESSAGES: Readonly<Record<SessionErrorKind, string>> = {
  budget_exhausted: "本日の受付は終了しました。",
  sessions_disabled: "ただいま受付を停止しています。",
  rate_limited: "少し待ってからお試しください。",
  turnstile_failed: "確認できませんでした。もう一度お試しください。",
  challenge_failed: "確認の画面を表示できませんでした。もう一度お試しください。",
  forbidden_origin: "このページからはステージを始められません。ページを読み込み直してください。",
  bad_request: "このステージは始められませんでした。ステージの一覧を読み込み直してください。",
  unavailable: "ステージを始められませんでした。もう一度お試しください。",
};

// 応答の code から分類する。challenge_failed は応答ではなく Turnstile のウィジェットの失敗なので、
// ここには入れない。
const CODE_KINDS: ReadonlyMap<string, SessionErrorKind> = new Map<string, SessionErrorKind>([
  ["budget_exhausted", "budget_exhausted"],
  ["sessions_disabled", "sessions_disabled"],
  ["rate_limited", "rate_limited"],
  ["turnstile_failed", "turnstile_failed"],
  ["forbidden_origin", "forbidden_origin"],
  ["bad_request", "bad_request"],
]);

// 201 以外の応答の分類。code は本文を JSON として読めたときの `code`（文字列でなければ undefined）。
export function sessionErrorKindOf(status: number, code: string | undefined): SessionErrorKind {
  if (code !== undefined) {
    return CODE_KINDS.get(code) ?? "unavailable";
  }
  return status === 429 ? "rate_limited" : "unavailable";
}

export class SessionCreateError extends Error {
  readonly kind: SessionErrorKind;

  constructor(kind: SessionErrorKind) {
    super(`セッションを作れませんでした（${kind}）`);
    this.name = "SessionCreateError";
    this.kind = kind;
  }
}
