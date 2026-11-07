// リザルトのシードの節（T-705・FR-GAME-06・DesignDoc §13.6）。
// シードはサーバーがスナップショットで返した値（ストアの world.session.seed。ADR-0009）をそのまま出す。
// 世界が無い（サーバーにつながらずに終わった）ときは節を出さない。URL にはシードだけを入れる。
import { useId, useState, type ReactElement } from "react";

import { useGameStore } from "../../state/index.ts";
import { seedUrl } from "../session/seedParam.ts";
import "./ResultSeed.css";

const COPIED = "コピーしました。";
const COPY_FAILED = "コピーできませんでした。URL を選んでコピーしてください。";

type CopyState = { text: string; count: number } | null;

export function ResultSeed({ stageId }: { stageId: string }): ReactElement | null {
  const headingId = useId();
  const seed = useGameStore((s) => s.world?.session.seed ?? null);
  const [copy, setCopy] = useState<CopyState>(null);

  if (seed === null) {
    return null;
  }
  const url = seedUrl(window.location.origin, stageId, seed);

  // 押すたびに文を置き直す（同じ文でも、読み上げに気づかせるため key を変える）。
  function show(text: string): void {
    setCopy((prev) => ({ text, count: (prev?.count ?? 0) + 1 }));
  }

  function onCopy(): void {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (typeof clipboard?.writeText !== "function") {
      show(COPY_FAILED);
      return;
    }
    clipboard.writeText(url).then(
      () => {
        show(COPIED);
      },
      () => {
        show(COPY_FAILED);
      },
    );
  }

  return (
    <section className="result-section result-seed" aria-labelledby={headingId}>
      <h2 id={headingId}>同じ条件で遊ぶ</h2>
      <p>
        このラウンドのシード: <span className="result-seed-value">{seed}</span>
      </p>
      <p>
        同じシードなら、依頼と嵐は同じ時刻に起きます。結果が変わるのは船の動かし方だけです。「同じ条件でもう一度」は、このシードで始めます。
      </p>
      <p className="result-seed-url">
        このシードで遊ぶ URL: <code>{url}</code>{" "}
        <button type="button" className="btn" onClick={onCopy}>
          URL をコピー
        </button>
      </p>
      {copy !== null && (
        <p key={copy.count} role="status" className="result-seed-copy">
          {copy.text}
        </p>
      )}
    </section>
  );
}
