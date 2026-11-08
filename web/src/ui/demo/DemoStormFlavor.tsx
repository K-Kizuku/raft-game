// デモの嵐の紹介の文（T-305b・FR-EVT-07・FR-DATA-04）。デモのセッションで、デモの嵐の航路が遮断されている間だけ、
// ステージ定義の demo.storm.flavor を出典区分とともに出す。データはストア（T-403）から読み、
// 世界の状態（snapshot.stage.demoStorm と route.*）だけから表示を決める（ADR-0009）。
// デモのセッションかどうかは、描く側（DemoKeys）が決める。
import type { ReactElement } from "react";

import { useGameStore } from "../../state/index.ts";
import { ProvenanceLabel } from "../provenance/ProvenanceLabel.tsx";
import { activeDemoStormFlavor } from "./demoStorm.ts";
import "./DemoStormFlavor.css";

export function DemoStormFlavor(): ReactElement | null {
  const flavor = useGameStore((s) => activeDemoStormFlavor(s.world));
  if (flavor === null) return null;
  const quote = flavor.source?.quote ?? null;
  return (
    <section aria-label="嵐の知らせ" className="demo-storm-flavor">
      <p className="demo-storm-flavor-text">{flavor.text}</p>
      {quote !== null && <p className="demo-storm-flavor-quote">「{quote}」</p>}
      <ProvenanceLabel provenance={flavor.provenance} />
    </section>
  );
}
