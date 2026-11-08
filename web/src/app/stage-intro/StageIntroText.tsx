// ステージの紹介文と、その出典区分・出典（T-416・FR-UI-02・FR-DATA-03・FR-DATA-04）。
// ステージ選択と紹介で共有する。文字は React の文字として出す（HTML として解釈しない）。
import type { ReactElement } from "react";

import { ProvenanceLabel } from "../../ui/provenance/ProvenanceLabel.tsx";
import type { StageText } from "../types.ts";
import { sourceText } from "./introView.ts";
import "./StageIntro.css";

export function StageIntroText({ intro }: { intro: StageText }): ReactElement {
  const { source } = intro;
  return (
    <div className="stage-intro-text">
      <p className="stage-intro-text__body">{intro.text}</p>
      <p className="stage-intro-text__meta">
        <ProvenanceLabel provenance={intro.provenance} />
        {source && <span className="stage-intro-text__source">{sourceText(source)}</span>}
      </p>
      {source?.quote && (
        <p className="stage-intro-text__quote">
          引用：<q>{source.quote}</q>
        </p>
      )}
    </div>
  );
}
