// ステージ紹介（概要・島の紹介・このステージで学べること・「出航する」）。
// 島の紹介文と学べることは、ステージの一覧（/api/stages の intro・learning）から受け取る（T-416・DesignDoc §13.6）。
// 「出航する」でのセッションの作成は App が行う（T-413）。
import type { ReactElement } from "react";

import { LearningArt } from "../../ui/art/learning/LearningArt.tsx";
import { ProvenanceLabel } from "../../ui/provenance/ProvenanceLabel.tsx";
import { formatDuration } from "../format.ts";
import { Link } from "../router/Link.tsx";
import type { StageCatalogEntry } from "../types.ts";
import { learningView } from "./introView.ts";
import { LearningCaption } from "./LearningCaption.tsx";
import { StageIntroText } from "./StageIntroText.tsx";
import "./StageIntro.css";

export type StageIntroScreenProps = {
  stage: StageCatalogEntry;
  onDepart: () => void;
  busy: boolean; // セッションの作成中（T-413）は「出航する」を押せなくする
};

export function StageIntroScreen({ stage, onDepart, busy }: StageIntroScreenProps): ReactElement {
  return (
    <div className="app-screen stage-intro">
      <h1 tabIndex={-1}>{stage.title}</h1>
      <p className="stage-intro-region">{stage.region}</p>
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
      <section aria-label="島の紹介" className="stage-intro-letter">
        <h2>島の紹介</h2>
        <StageIntroText intro={stage.intro} />
      </section>
      <StageLearning stageId={stage.id} learning={stage.learning} />
      <button
        type="button"
        className="btn btn-primary stage-intro-depart"
        disabled={busy}
        onClick={onDepart}
      >
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
    <section aria-label="学べること" className="stage-intro-learning">
      <h2>このステージで学べること</h2>
      {view.status === "hidden" ? (
        <p>遊び終わったあとで種明かしします。</p>
      ) : (
        // 学べることの名前は Raft の用語そのものなので、設定に関係なく両方を出す。
        // 名前の横に小さな図（T-433）と、図の場面の説明（用語は設定に従う）を添える。
        <ul>
          {view.items.map((item) => (
            <li key={item.key} className="stage-intro-learning-item">
              <LearningArt learningKey={item.key} className="stage-intro-learning-art" />
              <span className="stage-intro-learning-body">
                <span className="stage-intro-learning-name">
                  {item.game}（<span lang="en">{item.raft}</span>）
                </span>
                <LearningCaption learningKey={item.key} className="stage-intro-learning-caption" />
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
