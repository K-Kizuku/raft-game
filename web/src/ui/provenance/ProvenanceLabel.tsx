// 出典区分のラベル（FR-DATA-04・データ仕様 §1）。
import type { ReactElement } from "react";

export type Provenance = "fact" | "derived" | "creative";

const LABELS: Readonly<Record<Provenance, string>> = {
  fact: "出典：国土交通省『離島に関するデータ』",
  derived: "国土交通省『離島に関するデータ』をもとに作成",
  creative: "ゲーム上の設定",
};

export function ProvenanceLabel({ provenance }: { provenance: Provenance }): ReactElement {
  return (
    <small className="provenance-label" data-provenance={provenance}>
      {LABELS[provenance]}
    </small>
  );
}
