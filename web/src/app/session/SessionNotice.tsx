// セッションの作成の状態の表示（T-413）。App がステージ選択・紹介・リザルトの画面の後ろに置く。
import type { ReactElement } from "react";

import { SESSION_ERROR_MESSAGES } from "./errors.ts";
import type { SessionCreationState } from "./useSessionCreation.ts";
import "./SessionNotice.css";

export type SessionNoticeProps = {
  state: SessionCreationState;
  allowOffline: boolean; // 開発のときだけ真（「サーバーなしで進む（開発用）」を出す）
  onOffline: () => void;
};

export function SessionNotice({
  state,
  allowOffline,
  onOffline,
}: SessionNoticeProps): ReactElement | null {
  switch (state.status) {
    case "idle":
      return null;
    case "pending":
      return (
        <p role="status" className="session-notice session-notice-pending">
          ステージを準備しています…
        </p>
      );
    case "error":
      return (
        <div role="alert" className="session-notice session-notice-error">
          <p>{SESSION_ERROR_MESSAGES[state.kind]}</p>
          {allowOffline && (
            <button type="button" className="btn" onClick={onOffline}>
              サーバーなしで進む（開発用）
            </button>
          )}
        </div>
      );
  }
}
