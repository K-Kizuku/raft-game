// ステージ紹介（概要と「出航する」）。
// 島の紹介文のデータの出どころは未定（DesignDoc §13.6）なので、準備中の欄だけを置く。
import type { ReactElement } from "react";

import { ProvenanceLabel } from "../../ui/provenance/ProvenanceLabel.tsx";
import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import type { StageSummary } from "../types.ts";

export type StageIntroScreenProps = {
  stage: StageSummary;
  onDepart: () => void;
};

export function StageIntroScreen({ stage, onDepart }: StageIntroScreenProps): ReactElement {
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
      <button type="button" onClick={onDepart}>
        出航する
      </button>
      <Link to={{ name: "stageSelect" }}>ステージ選択へ戻る</Link>
    </div>
  );
}
