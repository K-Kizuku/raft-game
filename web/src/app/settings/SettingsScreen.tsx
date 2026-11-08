// 設定（T-412・FR-UI-10）。見た目は T-424a: 紙のパネルに、スイッチの見た目のトグルを置く。
// ステージの進み具合（クリアの記録）を消す節（T-703・T-703b・DesignDoc §13.9）。
// 効果音のオン・オフ（T-434。HUD の隅のボタンと同じ設定）。
import { useId, useState, type ReactElement } from "react";

import { ArtIcon } from "../../ui/art/ArtIcon.tsx";
import { soundEngine } from "../../ui/sound/engine.ts";
import { setSoundEnabled, useSoundPreference } from "../../ui/sound/preference.ts";
import { letterRaftTerm } from "../../ui/terms/glossary.ts";
import { RaftTerm, WithRaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { setShowRaftTerms, useTermsPreference } from "../../ui/terms/preference.ts";
import { stageProgress, useStageProgress } from "../progress/progress.ts";
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
      <SoundSection />
      <ProgressSection />
      <Link className="btn app-back" to={{ name: "title" }}>
        <ArtIcon kind="back" />
        タイトルへ
      </Link>
    </div>
  );
}

// 「効果音を鳴らす」のトグル。値はこのブラウザに保存する（既定はオン）。
function SoundSection(): ReactElement {
  const { enabled, persistent } = useSoundPreference();
  const descriptionId = useId();
  return (
    <section className="settings-sound">
      <label className="settings-toggle">
        <input
          className="settings-switch"
          type="checkbox"
          checked={enabled}
          aria-describedby={descriptionId}
          onChange={(event) => {
            soundEngine.unlock();
            setSoundEnabled(event.currentTarget.checked);
          }}
        />
        効果音を鳴らす
      </label>
      <p className="settings-description" id={descriptionId}>
        出航・到着・配達・依頼の確定・嵐の予報で、短い音を鳴らします。
      </p>
      {persistent ? null : (
        <p className="settings-warning">
          このブラウザには効果音の設定を保存できませんでした。ページを閉じると元に戻ります。
        </p>
      )}
    </section>
  );
}

// 「ステージの進み具合」の節。消す前に同じ場所で確かめる（window.confirm を使わない）。
function ProgressSection(): ReactElement {
  const { bestStars, persistent } = useStageProgress();
  const [step, setStep] = useState<"idle" | "confirm" | "cleared">("idle");
  const count = Object.keys(bestStars).length;
  return (
    <section className="settings-progress">
      <h2>ステージの進み具合</h2>
      <p className="settings-progress-count">クリアしたステージ: {count}</p>
      {step === "confirm" ? (
        <div className="settings-progress-confirm">
          <p>本当に消しますか？（ステージ選択のクリア済みの印が消えます）</p>
          <button
            type="button"
            className="btn"
            onClick={() => {
              stageProgress.clearAll();
              setStep("cleared");
            }}
          >
            消す
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => {
              setStep("idle");
            }}
          >
            やめる
          </button>
        </div>
      ) : (
        <button
          type="button"
          className="btn"
          disabled={count === 0}
          onClick={() => {
            setStep("confirm");
          }}
        >
          進み具合を消す
        </button>
      )}
      {step === "cleared" && (
        <p className="settings-progress-done" role="status">
          進み具合を消しました。
        </p>
      )}
      {persistent ? null : (
        <p className="settings-warning">
          このブラウザには進み具合を保存できませんでした。ページを閉じると元に戻ります。
        </p>
      )}
    </section>
  );
}
