// 学べることの図の説明（T-433）。図（web/src/ui/art/learning/）が描く場面を 1 文で言う。
// 用語は「Raft の用語を併記する」の設定に従う（RaftTerm。設定がオフならゲームの言葉だけ）。
import type { ReactElement, ReactNode } from "react";

import { RaftTerm, WithRaftTerm } from "../../ui/terms/RaftTerm.tsx";

const CAPTIONS: Readonly<Record<string, () => ReactNode>> = {
  log_replication: () => (
    <>
      <RaftTerm id="leader" />が<RaftTerm id="entry" />
      を配り、ほかの島も<RaftTerm id="ledger">台帳</RaftTerm>に写す
    </>
  ),
  commit: () => (
    <>
      <RaftTerm id="quorum" />
      から「記録したよ！」が届くと、
      <RaftTerm id="entry" />が<RaftTerm id="commit">確定</RaftTerm>する
    </>
  ),
  quorum: () => (
    <>
      5 島なら 3 島。半分より多くの
      <RaftTerm id="node" />
      がそろえば進める
    </>
  ),
  prevote: () => (
    <>
      勝てそうか周りに<WithRaftTerm raft="PreVote">相談</WithRaftTerm>してから立候補する
    </>
  ),
  heartbeat: () => (
    <>
      <RaftTerm id="leader" />は<RaftTerm id="heartbeat" />
      を出し続け、元気だと知らせる
    </>
  ),
  election_timeout: () => (
    <>
      <RaftTerm id="electionTimeout">定期便が途切れる</RaftTerm>
      と、島は「代表がいない？」と考える
    </>
  ),
  leader_election: () => (
    <>
      <RaftTerm id="candidate">立候補</RaftTerm>した島が
      <RaftTerm id="quorum" />の
      <RaftTerm id="vote" />
      を集めて
      <RaftTerm id="leader" />
      になる
    </>
  ),
  check_quorum: () => (
    <>
      <RaftTerm id="partition" />
      で返事が届かない
      <RaftTerm id="leader" />
      は、自分から退く
    </>
  ),
  state_machine_replication: () => (
    <>
      確定した
      <RaftTerm id="entry" />
      を同じ順に当てはめ、
      <RaftTerm id="ledger">台帳</RaftTerm>がそろう
    </>
  ),
  network_partition: () => (
    <>
      <RaftTerm id="partition" />
      で島が分かれ、島の多い側で新しい
      <RaftTerm id="leader" />
      が選ばれる
    </>
  ),
};

// 説明のある学べることのキー（テスト用）。
export const LEARNING_CAPTION_KEYS: readonly string[] = Object.keys(CAPTIONS);

export function LearningCaption({
  learningKey,
  className,
}: {
  learningKey: string;
  className?: string;
}): ReactElement | null {
  const caption = Object.hasOwn(CAPTIONS, learningKey) ? CAPTIONS[learningKey] : undefined;
  if (caption === undefined) {
    return null;
  }
  return (
    <span className={className} data-learning-caption={learningKey}>
      {caption()}
    </span>
  );
}
