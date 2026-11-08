// リザルトの「振り返り」（T-432）。ラウンド中にストアが受け取ったものから集めた数（state/reflection.ts）と、
// 次に試すことの一言を出す。数はクライアントが観察したもので、スコア・★には使わない（スコアはサーバーの round.result）。
// 文はゲームの言葉で書き、Raft の用語は併記の設定がオンのときだけ添える（RaftTerm）。
import { useId, useMemo, type ReactElement, type ReactNode } from "react";

import { useGameStore, type World } from "../../state/index.ts";
import {
  summarizeReflection,
  type Reflection,
  type ReflectionTipKind,
} from "../../state/reflection.ts";
import { RaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { useReflectionTally, type ReflectionTracker } from "./reflectionTracker.ts";
import { formatSeconds } from "./resultView.ts";
import "./ReflectionSection.css";

// 積まずに出航した島を、数のところに何島まで並べるか（表示の決まりで、ゲームの数値ではない）。
const MAX_ISLANDS_IN_COUNT = 3;

function islandName(world: World | null, islandId: number): string {
  return world?.stage.islands[islandId]?.name ?? `島 ${islandId}`;
}

type Tip = Readonly<{ kind: ReflectionTipKind; fact: ReactNode; advice: ReactNode }>;

function tipOf(kind: ReflectionTipKind, reflection: Reflection, world: World | null): Tip | null {
  switch (kind) {
    case "emptyDepartures": {
      const top = reflection.emptyDepartures.byIsland[0];
      if (top === undefined) return null;
      const name = islandName(world, top.islandId);
      return {
        kind,
        fact: `${name}で、港に手紙があるのに積まずに出航した回が ${top.count} 回ありました。`,
        advice: `通り道の${name}でも、おまかせで積んでから出航しましょう。`,
      };
    }
    case "replyWait": {
      const wait = reflection.longestReplyWait;
      if (wait === null) return null;
      const name = islandName(world, wait.islandId);
      return {
        kind,
        fact: (
          <>
            {name}の港で、
            <RaftTerm id="leader" />
            への返事が最長 {formatSeconds(wait.ms)}待ちました。
          </>
        ),
        advice: (
          <>
            {name}に船を寄せて、返事を
            <RaftTerm id="leader" />
            の島へ早めに届けましょう。返事が
            <RaftTerm id="quorum" />
            そろうと、記録が
            <RaftTerm id="commit" />
            します。
          </>
        ),
      };
    }
    case "expiredRequests":
      return {
        kind,
        fact: `期限切れになった依頼が ${reflection.expiredRequests} 件ありました。`,
        advice: "期限の近い依頼書から先に運びましょう。",
      };
    case "overflow":
      return {
        kind,
        fact: `港があふれて処分された手紙が ${reflection.overflowDrops} 通ありました。`,
        advice: "手紙がたまっている港へ先に船を回しましょう。",
      };
  }
}

function emptyDeparturesText(reflection: Reflection, world: World | null): string {
  const { total, byIsland } = reflection.emptyDepartures;
  if (total === 0) return "0 回";
  const islands = byIsland
    .slice(0, MAX_ISLANDS_IN_COUNT)
    .map((item) => `${islandName(world, item.islandId)} ${item.count}`)
    .join("・");
  const more = byIsland.length > MAX_ISLANDS_IN_COUNT ? "・ほか" : "";
  return `${total} 回（${islands}${more}）`;
}

export function ReflectionSection({ tracker }: { tracker?: ReflectionTracker }): ReactElement {
  const id = useId();
  const tipsId = useId();
  const tally = useReflectionTally(tracker);
  const world = useGameStore((s) => s.world);
  const reflection = useMemo(() => summarizeReflection(tally, world), [tally, world]);
  const tips = reflection.tips
    .map((kind) => tipOf(kind, reflection, world))
    .filter((tip): tip is Tip => tip !== null);
  const wait = reflection.longestReplyWait;

  return (
    <section className="result-section result-reflection" aria-labelledby={id}>
      <h2 id={id}>振り返り</h2>
      <div className="result-reflection-body">
        <dl className="result-list result-reflection-counts">
          <dt>積まずに出航</dt>
          <dd data-reflection="emptyDepartures">{emptyDeparturesText(reflection, world)}</dd>
          <dt>
            <RaftTerm id="leader" />
            への返事の最長の待ち
          </dt>
          <dd data-reflection="replyWait">
            {wait === null
              ? "—"
              : `${formatSeconds(wait.ms)}（${islandName(world, wait.islandId)}の港）`}
          </dd>
          <dt>期限切れの依頼</dt>
          <dd data-reflection="expiredRequests">{reflection.expiredRequests} 件</dd>
          <dt>港であふれた手紙</dt>
          <dd data-reflection="overflow">{reflection.overflowDrops} 通</dd>
        </dl>
        <div className="result-reflection-note" role="note" aria-labelledby={tipsId}>
          <h3 id={tipsId}>次はこうしてみよう</h3>
          {tips.length === 0 ? (
            <p>大きな詰まりは見つかりませんでした。この調子で★を増やしてみよう！</p>
          ) : (
            <ul className="result-tips">
              {tips.map((tip) => (
                <li key={tip.kind} data-tip={tip.kind}>
                  <p className="result-tip-advice">{tip.advice}</p>
                  <p className="result-tip-fact">{tip.fact}</p>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </section>
  );
}
