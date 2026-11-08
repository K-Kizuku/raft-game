// 開会式の演出「代表に〇〇島が選ばれました」（T-415・FR-GAME-02・DesignDoc §5.3・docs/05-game-progression.md §2）。
// GameScreen に重ねるスロット。データは props で受けず、ストア（T-403）から読む。ストアには書かない。
//
// 画面の作り方の判断:
// - 開会式は、ストアの leaderChanges に残る「時刻 0 の elected」（サーバーが出した node.leader_elected）で
//   見分ける。開会式のイベントは intro のスナップショットと同じ電文で届くことが多く、phase が opening の
//   まま描かれるとは限らないため。node.state から Leader を推定し直さない（ゲーム側で Leader を決めない）。
// - 時計を止めない: サーバーの開会式はゲーム内時間 0 秒で終わり、航行はすぐ始まる。演出の約 3 秒（実時間）の
//   間もゲーム内時間は進む。演出のために一時停止などのコマンドを送らない（チュートリアルの一時停止と
//   ぶつけないため）。
// - エンジニア向けの「選挙の記録」は <details> に入れ、設定に関係なく Raft の用語で書く（ゲームデザイン §8）。
// - 画像・音は使わない（素材は #131 の後に、この中だけを差し替える）。
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { FocusEvent, ReactElement, SyntheticEvent } from "react";

import { useGameStore } from "../../state/index.ts";
import type { LeaderChange, World } from "../../state/index.ts";
import { RAFT_TERMS } from "../terms/glossary.ts";
import { RaftTerm } from "../terms/RaftTerm.tsx";
import { openingCeremonyState } from "./ceremonyState.ts";
import { OPENING_CEREMONY } from "./config.ts";
import { islandLabel, openingResult } from "./openingView.ts";
import type { OpeningResult } from "./openingView.ts";
import "./OpeningCeremony.css";

// 紙吹雪の紙片の数（飾り。置き場所・色は OpeningCeremony.css の nth-child）。
const CONFETTI: readonly number[] = Array.from({ length: 16 }, (_, i) => i);

type Role = NonNullable<World["nodes"][number]>["role"];

const ROLE_NAME: Readonly<Record<Role, string>> = {
  leader: RAFT_TERMS.leader.raft,
  follower: RAFT_TERMS.follower.raft,
  pre_candidate: RAFT_TERMS.preCandidate.raft,
  candidate: RAFT_TERMS.candidate.raft,
};

export function OpeningCeremony(): ReactElement | null {
  const world = useGameStore((s) => s.world);
  const snapshot = useSyncExternalStore(
    openingCeremonyState.subscribe,
    openingCeremonyState.getSnapshot,
    openingCeremonyState.getSnapshot,
  );
  // 出し始めたときの結果を写したもの。世界が置き換わって結果が消えても、これで最後まで出す。
  const [shown, setShown] = useState<OpeningResult | null>(null);

  const result = openingResult(world);
  const pending =
    world !== null &&
    result !== null &&
    shown === null &&
    snapshot.showingKey === null &&
    !snapshot.doneKeys.has(result.key);
  const late =
    world !== null && (world.phase === "ended" || world.clock.simTime > OPENING_CEREMONY.lateSimMs);

  // 描画中の状態の更新（前の描画からの変化に合わせる React の書き方）。外部への副作用は effect で行う。
  if (pending && !late) {
    setShown(result);
  }
  if (world === null && shown !== null) {
    setShown(null);
  }

  // 遅く気づいた・終わったラウンドの結果は、出さずに終わったことにする。
  const skipKey = pending && late ? result.key : null;
  useEffect(() => {
    if (skipKey !== null) openingCeremonyState.finish(skipKey);
  }, [skipKey]);

  // 出している間は showingKey に鍵を置く。消えた・アンマウントされたら必ず finish する。
  const shownKey = shown?.key ?? null;
  useEffect(() => {
    if (shownKey === null) return;
    openingCeremonyState.begin(shownKey);
    return () => {
      openingCeremonyState.finish(shownKey);
    };
  }, [shownKey]);

  if (world === null || shown === null) return null;

  const close = (): void => {
    openingCeremonyState.finish(shown.key);
    setShown(null);
  };
  return <CeremonyBanner key={shown.key} result={shown} world={world} onClose={close} />;
}

type BannerProps = {
  result: OpeningResult;
  world: World;
  onClose: () => void;
};

function CeremonyBanner({ result, world, onClose }: BannerProps): ReactElement {
  const sectionRef = useRef<HTMLElement | null>(null);
  const remainingRef = useRef<number>(OPENING_CEREMONY.durationMs);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const paused = hovered || focused || detailsOpen;

  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);

  // 実時間で durationMs 出す。止めている間は残り時間を覚え、再開では resumeMinMs 以上を残す。
  useEffect(() => {
    if (paused) return;
    const startedAt = performance.now();
    const timer = setTimeout(() => {
      onCloseRef.current();
    }, remainingRef.current);
    return () => {
      clearTimeout(timer);
      const elapsed = performance.now() - startedAt;
      remainingRef.current = Math.max(remainingRef.current - elapsed, OPENING_CEREMONY.resumeMinMs);
    };
  }, [paused]);

  const onBlur = (e: FocusEvent<HTMLElement>): void => {
    const next = e.relatedTarget;
    if (next instanceof Node && e.currentTarget.contains(next)) return;
    setFocused(false);
  };

  const onToggle = (e: SyntheticEvent<HTMLDetailsElement>): void => {
    setDetailsOpen(e.currentTarget.open);
  };

  const onCloseClick = (): void => {
    const section = sectionRef.current;
    const active = document.activeElement;
    if (section !== null && active !== null && section.contains(active)) {
      const heading = document.querySelector<HTMLElement>(".game-screen h1");
      heading?.focus();
    }
    onClose();
  };

  return (
    <>
      <div className="opening-ceremony__confetti" aria-hidden="true">
        {CONFETTI.map((i) => (
          <span key={i} />
        ))}
      </div>
      <section
        ref={sectionRef}
        className="opening-ceremony"
        role="status"
        aria-label="開会式"
        onPointerEnter={() => setHovered(true)}
        onPointerLeave={() => setHovered(false)}
        onFocus={() => setFocused(true)}
        onBlur={onBlur}
      >
        <h2 className="opening-ceremony__title">開会式</h2>
        <p className="opening-ceremony__message">
          <RaftTerm id="leader" />に
          <span className="opening-ceremony__island">
            <span
              className="opening-ceremony__dot"
              style={result.color === null ? undefined : { backgroundColor: result.color }}
              aria-hidden="true"
            />
            <span className="opening-ceremony__crown" aria-hidden="true">
              👑
            </span>
            {result.islandName}
          </span>
          が選ばれました
        </p>
        <button type="button" className="btn opening-ceremony__close" onClick={onCloseClick}>
          閉じる
        </button>
        <details className="opening-ceremony__election" onToggle={onToggle}>
          <summary>選挙の記録</summary>
          <ElectionRecord result={result} world={world} />
        </details>
      </section>
    </>
  );
}

function changeText(world: World, change: LeaderChange): string {
  const who = `${islandLabel(world, change.id)}（ID ${change.id}）`;
  if (change.kind === "elected") {
    return `${who}が Leader になった（Term ${change.term}）`;
  }
  return `${who}が Leader を退いた（Term ${change.term}、${change.reason}）`;
}

// エンジニア向けの記録。設定に関係なく Raft の用語で書く。値はストアの今の world から読む。
function ElectionRecord({ result, world }: { result: OpeningResult; world: World }): ReactElement {
  return (
    <div className="opening-ceremony__record">
      <p>Term {result.term}</p>
      <p>
        Leader: {islandLabel(world, result.leaderId)}（ID {result.leaderId}）
      </p>
      <ol className="opening-ceremony__changes">
        {result.changes.map((change) => (
          <li key={change.seq}>{changeText(world, change)}</li>
        ))}
      </ol>
      <table className="opening-ceremony__nodes">
        <caption>各島の状態（いまの値）</caption>
        <thead>
          <tr>
            <th scope="col">ID</th>
            <th scope="col">島</th>
            <th scope="col">Role</th>
            <th scope="col">Term</th>
            <th scope="col">Lead</th>
          </tr>
        </thead>
        <tbody>
          {world.stage.islandIds.map((id) => {
            const node = world.nodes[id];
            return (
              <tr key={id}>
                <td>{id}</td>
                <td>{islandLabel(world, id)}</td>
                <td>{node === undefined ? "—" : ROLE_NAME[node.role]}</td>
                <td>{node === undefined ? "—" : node.term}</td>
                <td>{node === undefined || node.lead === 0 ? "—" : node.lead}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="opening-ceremony__note">
        投票（Vote）は、エンジニアパネル（E）で選んだ島の詳細に出ます。
      </p>
    </div>
  );
}
