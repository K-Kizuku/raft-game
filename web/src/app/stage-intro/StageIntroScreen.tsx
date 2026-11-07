// ステージ紹介（概要と「出航する」）。
// 島の紹介文の表示は後続のタスク（DesignDoc §13.6）なので、準備中の欄だけを置く。
// 「出航する」でのセッションの作成は App が行う（T-413）。
import type { ReactElement } from "react";

import { ProvenanceLabel } from "../../ui/provenance/ProvenanceLabel.tsx";
import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import type { StageSummary } from "../types.ts";

export type StageIntroScreenProps = {
  stage: StageSummary;
  onDepart: () => void;
  busy: boolean; // セッションの作成中（T-413）は「出航する」を押せなくする
};

export function StageIntroScreen({ stage, onDepart, busy }: StageIntroScreenProps): ReactElement {
  return (
    <div className="app-screen stage-intro">
      <h1 tabIndex={-1}>{stage.title}</h1>
      <p>{stage.region}</p>
      <dl className="stage-intro-facts">
        <div>
          <dt>島の数</dt>
          <dd>
            {stage.islands} <ProvenanceLabel provenance="creative" />
          </dd>
        </div>
        <div>
          <dt>フェリー</dt>
          <dd>
            {stage.ferries} 隻 <ProvenanceLabel provenance="creative" />
          </dd>
        </div>
        <div>
          <dt>制限時間</dt>
          <dd>
            {formatDuration(stage.durationMs)} <ProvenanceLabel provenance="creative" />
          </dd>
        </div>
      </dl>
      <section aria-label="島の紹介">
        <h2>島の紹介</h2>
        <p>準備中です。</p>
      </section>
      <button type="button" disabled={busy} onClick={onDepart}>
        出航する
      </button>
      <Link to={{ name: "stageSelect" }}>ステージ選択へ戻る</Link>
    </div>
  );
}
