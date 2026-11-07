// ステージ紹介（概要・島の紹介・このステージで学べること・「出航する」）。
// 島の紹介文と学べることは、ステージの一覧（/api/stages の intro・learning）から受け取る（T-416・DesignDoc §13.6）。
// 「出航する」でのセッションの作成は App が行う（T-413）。
import type { ReactElement } from "react";

import { ProvenanceLabel } from "../../ui/provenance/ProvenanceLabel.tsx";
import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import type { StageCatalogEntry } from "../types.ts";
import { learningView } from "./introView.ts";
import { StageIntroText } from "./StageIntroText.tsx";

export type StageIntroScreenProps = {
  stage: StageCatalogEntry;
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
        <StageIntroText intro={stage.intro} />
      </section>
      <StageLearning stageId={stage.id} learning={stage.learning} />
      <button type="button" disabled={busy} onClick={onDepart}>
        出航する
      </button>
      <Link to={{ name: "stageSelect" }}>ステージ選択へ戻る</Link>
    </div>
  );
}

function StageLearning({
  stageId,
  learning,
}: {
  stageId: string;
  learning: readonly string[];
}): ReactElement | null {
  const view = learningView(stageId, learning);
  if (view.status === "none") {
    return null;
  }
  return (
    <section aria-label="学べること">
      <h2>このステージで学べること</h2>
      {view.status === "hidden" ? (
        <p>遊び終わったあとで種明かしします。</p>
      ) : (
        // 学べることは Raft の用語そのものなので、設定に関係なく両方を出す。
        <ul>
          {view.items.map((item) => (
            <li key={item.key}>
              {item.game}（<span lang="en">{item.raft}</span>）
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
