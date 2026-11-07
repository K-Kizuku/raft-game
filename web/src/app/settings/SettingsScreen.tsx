// 設定（T-412・FR-UI-10）。
import { useId, type ReactElement } from "react";

import { letterRaftTerm } from "../../ui/terms/glossary.ts";
import { RaftTerm, WithRaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { setShowRaftTerms, useTermsPreference } from "../../ui/terms/preference.ts";
import { Link } from "../router/Link.tsx";

export function SettingsScreen(): ReactElement {
  const { showRaftTerms, persistent } = useTermsPreference();
  const descriptionId = useId();
  return (
    <div className="app-screen app-settings">
      <h1 tabIndex={-1}>設定</h1>
      <label>
        <input
          type="checkbox"
          checked={showRaftTerms}
          aria-describedby={descriptionId}
          onChange={(event) => {
            setShowRaftTerms(event.currentTarget.checked);
          }}
        />
        Raft の用語を併記する
      </label>
      <p id={descriptionId}>オンにすると、ゲームの言葉に Raft の用語を小さく添えます。</p>
      <p className="settings-terms-example">
        表示の例：
        <RaftTerm id="leader" />・<RaftTerm id="commit" />・
        <WithRaftTerm raft={letterRaftTerm("append_resp")}>記録したよ！</WithRaftTerm>
      </p>
      <p>設定はこのブラウザに保存します（サーバーには送りません）。</p>
      {persistent ? null : (
        <p role="status">
          このブラウザには設定を保存できませんでした。ページを閉じると元に戻ります。
        </p>
      )}
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}
