// ゲームの言葉 ⇔ Raft の用語の表（T-412・DesignDoc §13.7・用語集）。画面に出す文字列の正本。
// 併記の括弧は RaftTerm と withRaftTerm が付けるので、raft には全角の括弧を含めない。

export type RaftTermId =
  | "leader"
  | "follower"
  | "preCandidate"
  | "candidate"
  | "term"
  | "ledger"
  | "entry"
  | "commit"
  | "apply"
  | "quorum"
  | "node"
  | "letter"
  | "heartbeat"
  | "vote"
  | "electionTimeout"
  | "checkQuorum"
  | "partition"
  | "staleLetter";

export type RaftTermEntry = Readonly<{ game: string; raft: string }>;

export const RAFT_TERMS: Readonly<Record<RaftTermId, RaftTermEntry>> = {
  leader: { game: "代表", raft: "Leader" },
  follower: { game: "一般の島", raft: "Follower" },
  preCandidate: { game: "立候補の相談中", raft: "PreCandidate" },
  candidate: { game: "立候補中", raft: "Candidate" },
  term: { game: "代表の任期", raft: "Term" },
  ledger: { game: "共和国の台帳", raft: "Log" },
  entry: { game: "記録", raft: "Log Entry" },
  commit: { game: "共和国で確定", raft: "Commit" },
  apply: { game: "確定", raft: "Apply" },
  quorum: { game: "過半数", raft: "Quorum" },
  node: { game: "島", raft: "Node" },
  letter: { game: "手紙", raft: "Message" },
  heartbeat: { game: "定期便", raft: "Heartbeat" },
  vote: { game: "投票", raft: "Vote" },
  electionTimeout: { game: "定期便が途切れる", raft: "Election Timeout" },
  checkQuorum: { game: "孤立した代表が退く", raft: "CheckQuorum" },
  partition: { game: "嵐", raft: "Network Partition" },
  staleLetter: { game: "古い手紙", raft: "古い Term" },
};

// プロトコル §5.2 の Letter.label。T-301 の生成された型ができたら置き換える。
export type LetterLabel =
  | "heartbeat"
  | "heartbeat_resp"
  | "append"
  | "append_probe"
  | "append_resp"
  | "append_reject"
  | "prevote"
  | "prevote_resp"
  | "vote"
  | "vote_resp"
  | "request"
  | "other";

// 手紙の label ⇔ Msg*（ゲームデザイン §2.1）。手紙のゲーム上の名前は表示する側が持つ。
export const LETTER_RAFT_TERMS: Readonly<Record<LetterLabel, string | null>> = {
  heartbeat: "MsgHeartbeat",
  heartbeat_resp: "MsgHeartbeatResp",
  append: "MsgApp",
  append_probe: "MsgApp",
  append_resp: "MsgAppResp",
  append_reject: "MsgAppResp・Reject",
  prevote: "MsgPreVote",
  prevote_resp: "MsgPreVoteResp",
  vote: "MsgVote",
  vote_resp: "MsgVoteResp",
  // 住民の依頼書は Raft の手紙ではない。
  request: null,
  other: null,
};

function isLetterLabel(label: string): label is LetterLabel {
  return Object.hasOwn(LETTER_RAFT_TERMS, label);
}

// 知らない label には何も添えない（プロトコル §8）。
export function letterRaftTerm(label: string): string | null {
  return isLetterLabel(label) ? LETTER_RAFT_TERMS[label] : null;
}

// PixiJS の文字・aria-label・title など、文字列が要るところで使う。
export function withRaftTerm(game: string, raft: string | null, show: boolean): string {
  return show && raft !== null && raft !== "" ? `${game}（${raft}）` : game;
}
