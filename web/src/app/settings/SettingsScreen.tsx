// 設定（T-412・FR-UI-10）。見た目は T-424a: 紙のパネルに、スイッチの見た目のトグルを置く。
import { useId, type ReactElement } from "react";

import { ArtIcon } from "../../ui/art/ArtIcon.tsx";
import { letterRaftTerm } from "../../ui/terms/glossary.ts";
import { RaftTerm, WithRaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { setShowRaftTerms, useTermsPreference } from "../../ui/terms/preference.ts";
import { Link } from "../router/Link.tsx";
import "./SettingsScreen.css";

export function SettingsScreen(): ReactElement {
  const { showRaftTerms, persistent } = useTermsPreference();
  const descriptionId = useId();
  return (
    <div className="app-screen app-card app-settings">
      <h1 tabIndex={-1}>設定</h1>
      <label className="settings-toggle">
        <input
          className="settings-switch"
          type="checkbox"
          checked={showRaftTerms}
          aria-describedby={descriptionId}
          onChange={(event) => {
            setShowRaftTerms(event.currentTarget.checked);
          }}
        />
        Raft の用語を併記する
      </label>
      <p className="settings-description" id={descriptionId}>
        オンにすると、ゲームの言葉に Raft の用語を小さく添えます。
      </p>
      <p className="settings-terms-example">
        表示の例：
        <RaftTerm id="leader" />・<RaftTerm id="commit" />・
        <WithRaftTerm raft={letterRaftTerm("append_resp")}>記録したよ！</WithRaftTerm>
      </p>
      <p className="settings-storage">設定はこのブラウザに保存します（サーバーには送りません）。</p>
      {persistent ? null : (
        <p className="settings-warning" role="status">
          このブラウザには設定を保存できませんでした。ページを閉じると元に戻ります。
        </p>
      )}
      <Link className="btn app-back" to={{ name: "title" }}>
        <ArtIcon kind="back" />
        タイトルへ
      </Link>
    </div>
  );
}
