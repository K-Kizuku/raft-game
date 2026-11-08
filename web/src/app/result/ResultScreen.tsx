// リザルト（T-408・FR-SCORE-01〜03・FR-UI-06・ゲームデザイン §5.3・§7.5）。
// 値はすべてサーバーの round.result（ストアの roundResult）をそのまま出す。スコア・★・統計・文章を
// クライアントで計算し直さない（ADR-0009）。★の条件の文も、サーバーの stars で ✓ を付けるだけ。
// 技術的な結果と出来事はリザルトの解説なので、用語の併記の設定に関係なく Raft の用語で出す（ゲームデザイン §8）。
import { useId, useMemo, type ReactElement } from "react";

import type { FinalLog } from "../../protocol/index.ts";
import { useGameStore, type World } from "../../state/index.ts";
import { formatDuration } from "../format.ts";
import type { SessionHandle, StageSummary } from "../types.ts";
import { tokens } from "../../ui/theme/tokens.ts";
import { Fireworks } from "./Fireworks.tsx";
import { InputLogDownload } from "./InputLogDownload.tsx";
import { ReplayButton } from "./ReplayButton.tsx";
import { ResultEngineer } from "./ResultEngineer.tsx";
import { ResultSeed } from "./ResultSeed.tsx";
import {
  deliveredRows,
  formatPoints,
  formatSecondsOrDash,
  hasFireworks,
  logBands,
  logScale,
  normalizeResult,
  termColor,
  termRanges,
  type ResultView,
  type Stars,
  type TechnicalView,
} from "./resultView.ts";
import "./ResultScreen.css";

// 段階の言葉（画面の文言で、ゲームの数値ではない）。
const STAR_MESSAGES: Readonly<Record<Stars, string>> = {
  0: "目標まであと一歩。もう一度挑戦してみよう！",
  1: "★1 クリア！ 島じまの依頼が共和国で確定しました。",
  2: "★2 花火の予約がすべて確定！ 花火大会が開かれます。",
  3: "★3 パーフェクト！ 称号「共和国の名船長」を手に入れました。",
};

const MAX_STARS = 3;
const NO_COLOR = tokens["--color-ink-muted"];

export type ResultScreenProps = {
  stage: StageSummary;
  nextStage: StageSummary | null;
  onRetry: () => void;
  onNextStage: () => void;
  onStageSelect: () => void;
  session?: SessionHandle | null; // T-714・T-720e。入力ログのダウンロードとリプレイに使う（サーバーなしなら null）
};

function starConditions(world: World | null): string[] {
  const target = world?.score.target ?? 0;
  return [
    target > 0 ? `確定した依頼が目標数（${target} 件）以上` : "確定した依頼が目標数以上",
    "重要な資源（花火）をすべて確定",
    "スコアが高得点の目標以上",
  ];
}

function StarsSection({ view, world }: { view: ResultView; world: World | null }): ReactElement {
  const id = useId();
  const stars = view.stars;
  return (
    <section className="result-section" aria-labelledby={id}>
      <h2 id={id}>クリアの段階</h2>
      <p className="result-stars" role="img" aria-label={`★ ${stars} / ${MAX_STARS}`}>
        {Array.from({ length: MAX_STARS }, (_, i) =>
          i < stars ? (
            <span key={i} className="result-star result-star-filled" aria-hidden="true">
              ★
            </span>
          ) : (
            <span key={i} className="result-star result-star-empty" aria-hidden="true">
              ☆
            </span>
          ),
        )}
      </p>
      <p className="result-stage-message">{STAR_MESSAGES[stars]}</p>
      <ul className="result-conditions" aria-label="★の条件">
        {starConditions(world).map((text, i) => {
          const achieved = stars >= i + 1;
          return (
            <li key={i} data-achieved={achieved ? "true" : "false"}>
              <span className="result-condition-mark" aria-hidden="true">
                {achieved ? "✓" : "・"}
              </span>
              <span className="visually-hidden">{achieved ? "達成" : "未達成"}</span>
              <span className="result-condition">{text}</span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function ScoreSection({ view }: { view: ResultView }): ReactElement {
  const id = useId();
  const rows: [string, number][] = [
    ["依頼の得点", view.breakdown.requests],
    ["期限切れ", view.breakdown.expired],
    ["運航コスト", view.breakdown.ferryCost],
    ["公平ボーナス", view.breakdown.fairness],
  ];
  return (
    <section className="result-section" aria-labelledby={id}>
      <h2 id={id}>スコア</h2>
      <p className="result-total">{formatPoints(view.total, false)} 点</p>
      <table className="result-breakdown" aria-label="スコアの内訳">
        <tbody>
          {rows.map(([label, value]) => (
            <tr key={label}>
              <th scope="row">{label}</th>
              <td>{formatPoints(value, true)}</td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr>
            <th scope="row">合計</th>
            <td>{formatPoints(view.total, false)}</td>
          </tr>
        </tfoot>
      </table>
    </section>
  );
}

function RequestsSection({ view }: { view: ResultView }): ReactElement {
  const id = useId();
  return (
    <section className="result-section" aria-labelledby={id}>
      <h2 id={id}>依頼</h2>
      <dl className="result-list">
        <dt>確定</dt>
        <dd>{view.succeeded} 件</dd>
        <dt>予約失敗</dt>
        <dd>{view.rejected} 件</dd>
        <dt>期限切れ</dt>
        <dd>{view.expired} 件</dd>
      </dl>
    </section>
  );
}

function FinalLogItem({
  log,
  scale,
  world,
}: {
  log: FinalLog;
  scale: number;
  world: World | null;
}): ReactElement {
  const island = world?.stage.islands[log.id];
  const name = island?.name ?? `島 ${log.id}`;
  const color = island?.color ?? NO_COLOR;
  const bands = logBands(log, scale);
  const termsText = termRanges(log)
    .map(([term, first, last]) => `Term ${term}: ${first}〜${last}`)
    .join("、");
  return (
    <li className="result-log">
      <span className="result-log-island">
        <span className="result-log-dot" aria-hidden="true" style={{ backgroundColor: color }} />
        {name}
      </span>
      {scale > 0 ? (
        <div className="log-band" aria-hidden="true">
          {bands.offset > 0 && <span className="log-gap" style={{ flexGrow: bands.offset }} />}
          {bands.segments.map((segment, i) => (
            <span
              key={i}
              className="log-segment"
              data-term={segment.term}
              data-committed={segment.committed ? "true" : "false"}
              style={{ flexGrow: segment.size, backgroundColor: termColor(segment.term) }}
            />
          ))}
          {bands.padding > 0 && <span className="log-gap" style={{ flexGrow: bands.padding }} />}
        </div>
      ) : (
        <p className="result-log-empty">ログは空です。</p>
      )}
      <p className="result-log-text">
        Last Index {log.lastIndex}・Commit {log.commit}
        {log.commit < log.lastIndex && `（${log.commit + 1}〜${log.lastIndex} は未確定）`}
      </p>
      {termsText !== "" && <p className="result-log-text">{termsText}</p>}
    </li>
  );
}

function TechnicalSection({
  technical,
  world,
}: {
  technical: TechnicalView | null;
  world: World | null;
}): ReactElement {
  const id = useId();
  const delivered = useMemo(
    () => (technical === null ? null : deliveredRows(technical.lettersDelivered)),
    [technical],
  );
  const scale = useMemo(
    () => (technical === null ? 0 : logScale(technical.finalLogs)),
    [technical],
  );
  return (
    <section className="result-section" aria-labelledby={id}>
      <h2 id={id}>技術的な結果</h2>
      {technical === null || delivered === null ? (
        <p>技術的な結果はまだ集計されていません。</p>
      ) : (
        <dl className="result-list">
          <dt>配った手紙</dt>
          <dd>
            {delivered.total === 0 ? (
              "—"
            ) : (
              <>
                <span>{delivered.total} 通</span>
                <ul className="result-letters" aria-label="配った手紙の種類">
                  {delivered.rows.map((row) => (
                    <li key={row.label}>
                      {row.name}
                      {row.raft !== null && (
                        <>
                          （<span lang="en">{row.raft}</span>）
                        </>
                      )}
                      {row.count} 通
                    </li>
                  ))}
                </ul>
              </>
            )}
          </dd>
          <dt>Leader の交代</dt>
          <dd>{technical.leaderChanges} 回</dd>
          <dt>平均 Commit Latency</dt>
          <dd>{formatSecondsOrDash(technical.avgCommitLatencyMs)}</dd>
          <dt>最長の音信不通</dt>
          <dd>{formatSecondsOrDash(technical.longestSilenceMs)}</dd>
          <dt>各島の最終ログ</dt>
          <dd>
            {technical.finalLogs.length === 0 ? (
              "記録がありません。"
            ) : (
              <ol className="result-logs" aria-label="各島の最終ログ">
                {technical.finalLogs.map((log) => (
                  <FinalLogItem key={log.id} log={log} scale={scale} world={world} />
                ))}
              </ol>
            )}
          </dd>
        </dl>
      )}
    </section>
  );
}

function StorySection({ view }: { view: ResultView }): ReactElement {
  const id = useId();
  return (
    <section className="result-section result-letter" aria-labelledby={id}>
      <h2 id={id}>今日起きた分散システムの出来事</h2>
      {view.story.length === 0 ? (
        <p>今日は書き残す出来事がありませんでした。</p>
      ) : (
        <ol className="result-story">
          {view.story.map((item, i) => (
            <li key={i}>
              <time>{formatDuration(item.t)}</time>
              <p>{item.text}</p>
              {item.concepts.length > 0 && (
                <ul className="result-concepts" aria-label="関係する Raft の仕組み">
                  {item.concepts.map((concept, j) => (
                    <li key={j} lang="en">
                      {concept}
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

export function ResultScreen({
  stage,
  nextStage,
  onRetry,
  onNextStage,
  onStageSelect,
  session,
}: ResultScreenProps): ReactElement {
  const roundResult = useGameStore((s) => s.roundResult);
  const world = useGameStore((s) => s.world);
  const view = useMemo(
    () => (roundResult === null ? null : normalizeResult(roundResult)),
    [roundResult],
  );

  return (
    <div className="app-screen result">
      <h1 tabIndex={-1}>リザルト</h1>
      <p>{stage.title}</p>
      {view === null ? (
        <p>結果を受け取っていません。</p>
      ) : (
        // 広い画面では、列ごとに並べ、列の中だけを巻き取る（T-429・docs/11 §15）。
        <div className="result-board">
          <div className="result-column result-column-summary">
            <StarsSection view={view} world={world} />
            {hasFireworks(view.stars) && <Fireworks />}
            <div className="result-figures">
              <ScoreSection view={view} />
              <RequestsSection view={view} />
            </div>
          </div>
          <div className="result-column result-column-technical">
            <TechnicalSection technical={view.technical} world={world} />
          </div>
          <div className="result-column result-column-story">
            <StorySection view={view} />
            <ResultEngineer />
          </div>
        </div>
      )}
      <div className="result-column result-next">
        {view !== null && <ResultSeed stageId={stage.id} />}
        {view !== null && (
          <InputLogDownload
            session={session ?? null}
            available={roundResult?.inputLogAvailable === true}
          />
        )}
        {view !== null && (
          <ReplayButton
            session={session ?? null}
            available={roundResult?.inputLogAvailable === true}
          />
        )}
        <ResultActions
          nextStage={nextStage}
          onRetry={onRetry}
          onNextStage={onNextStage}
          onStageSelect={onStageSelect}
        />
      </div>
    </div>
  );
}

function ResultActions({
  nextStage,
  onRetry,
  onNextStage,
  onStageSelect,
}: Pick<
  ResultScreenProps,
  "nextStage" | "onRetry" | "onNextStage" | "onStageSelect"
>): ReactElement {
  return (
    <div className="result-actions">
      <button type="button" className="btn btn-primary" onClick={onRetry}>
        同じ条件でもう一度
      </button>
      {nextStage !== null && (
        <button type="button" className="btn" onClick={onNextStage}>
          次のステージ
        </button>
      )}
      <button type="button" className="btn" onClick={onStageSelect}>
        ステージ選択
      </button>
    </div>
  );
}
