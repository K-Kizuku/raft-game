// リザルトのエンジニアモードのタイムラインの記録（T-701・FR-ENG-09）。
// データはサーバーに足さず、画面が受け取ったスナップショットとイベント（node.state・node.leader_elected・
// node.stepped_down）を観察して古い順に並べるだけ。どれが正しい Leader か・どこまで Commit したかは決めない
// （値はすべて届いたイベントのまま。ADR-0009）。round.result には時系列が無く、リザルトは再読み込みで消えるので
// （DesignDoc §13.6）、画面が記録しても失うものは同じ。世界の状態（World）ではなく、表示のための記録として別に持つ。
// 時刻はイベントとスナップショットの t だけを使う（実時間を読まない）。引数は書き換えない。
import type { ServerEvent, Snapshot, StepDownReason } from "../protocol/index.ts";

export type TimelineLeader = Readonly<{ id: number; term: number }>;

export type TimelineItem = Readonly<
  // 届いた node.state の Term の最大が term に上がった（id は最初にその Term になった島）
  | { kind: "term"; seq: number; t: number; term: number; id: number }
  // node.leader_elected
  | { kind: "elected"; seq: number; t: number; id: number; term: number }
  // node.stepped_down
  | {
      kind: "stepped_down";
      seq: number;
      t: number;
      id: number;
      term: number;
      reason: StepDownReason;
    }
  // 届いた node.state の Commit の最大が index に上がった
  | { kind: "commit"; seq: number; t: number; index: number }
  // スナップショットを受けた。initial = 記録の始まり（新しいセッション）、false = つなぎ直し。
  // leaders はその時点で role が leader の島（ID の昇順）
  | {
      kind: "resync";
      seq: number;
      t: number;
      initial: boolean;
      leaders: readonly TimelineLeader[];
    }
>;

export type Timeline = Readonly<{
  sessionId: string | null; // 最後に受けたスナップショットの session.id。まだなら null
  items: readonly TimelineItem[]; // 古い順
  maxTerm: number; // これまでに届いた Term の最大
  maxCommit: number; // これまでに届いた Commit の最大
  baseTerm: number; // 記録を始めた（新しいセッションのスナップショットを受けた）ときの Term の最大
  baseCommit: number; // 同じく Commit の最大
  lastSeq: number; // 最後に観察したイベントの seq（スナップショットの lastSeq から）
  truncated: boolean; // 上限を超えて古いものを捨てたか
}>;

// 画面のための上限（ゲームの数値ではない。WORLD_LIMITS と同じ扱い）。
export const TIMELINE_LIMIT = 1000;

export const EMPTY_TIMELINE: Timeline = Object.freeze({
  sessionId: null,
  items: Object.freeze([]) as readonly TimelineItem[],
  maxTerm: 0,
  maxCommit: 0,
  baseTerm: 0,
  baseCommit: 0,
  lastSeq: 0,
  truncated: false,
});

type SnapshotNodes = Snapshot["nodes"];

function maxOf(nodes: SnapshotNodes, pick: (node: SnapshotNodes[number]) => number): number {
  let max = 0;
  for (const node of nodes) {
    const value = pick(node);
    if (value > max) max = value;
  }
  return max;
}

function leadersOf(nodes: SnapshotNodes): TimelineLeader[] {
  return nodes
    .filter((node) => node.role === "leader")
    .map((node) => ({ id: node.id, term: node.term }))
    .sort((a, b) => a.id - b.id);
}

// Term が term の島のうち ID の最小（無ければ 0）。
function firstIdWithTerm(nodes: SnapshotNodes, term: number): number {
  let id = 0;
  for (const node of nodes) {
    if (node.term === term && (id === 0 || node.id < id)) id = node.id;
  }
  return id;
}

// 上限を超えた分を先頭から捨てる。
function limited(
  items: readonly TimelineItem[],
  truncated: boolean,
): { items: readonly TimelineItem[]; truncated: boolean } {
  if (items.length <= TIMELINE_LIMIT) return { items, truncated };
  return { items: items.slice(items.length - TIMELINE_LIMIT), truncated: true };
}

export function timelineFromSnapshot(prev: Timeline, snapshot: Snapshot): Timeline {
  const nodes = snapshot.nodes;
  const snapTerm = maxOf(nodes, (node) => node.term);
  const snapCommit = maxOf(nodes, (node) => node.commit);
  const leaders = leadersOf(nodes);
  const seq = snapshot.lastSeq;
  const t = snapshot.clock.t;

  if (prev.sessionId !== snapshot.session.id) {
    const items: TimelineItem[] =
      leaders.length > 0 ? [{ kind: "resync", seq, t, initial: true, leaders }] : [];
    return {
      sessionId: snapshot.session.id,
      items,
      maxTerm: snapTerm,
      maxCommit: snapCommit,
      baseTerm: snapTerm,
      baseCommit: snapCommit,
      lastSeq: seq,
      truncated: false,
    };
  }

  const added: TimelineItem[] = [{ kind: "resync", seq, t, initial: false, leaders }];
  if (snapTerm > prev.maxTerm) {
    added.push({ kind: "term", seq, t, term: snapTerm, id: firstIdWithTerm(nodes, snapTerm) });
  }
  if (snapCommit > prev.maxCommit) {
    added.push({ kind: "commit", seq, t, index: snapCommit });
  }
  const { items, truncated } = limited([...prev.items, ...added], prev.truncated);
  return {
    ...prev,
    items,
    maxTerm: Math.max(prev.maxTerm, snapTerm),
    maxCommit: Math.max(prev.maxCommit, snapCommit),
    lastSeq: seq,
    truncated,
  };
}

export function appendTimelineEvents(prev: Timeline, events: readonly ServerEvent[]): Timeline {
  let lastSeq = prev.lastSeq;
  let maxTerm = prev.maxTerm;
  let maxCommit = prev.maxCommit;
  const added: TimelineItem[] = [];

  for (const event of events) {
    if (event.seq <= lastSeq) continue; // ストアの applyEvent と同じ規則（重複・古いものは読み飛ばす）
    lastSeq = event.seq;
    switch (event.type) {
      case "node.state":
        if (event.term > maxTerm) {
          maxTerm = event.term;
          added.push({ kind: "term", seq: event.seq, t: event.t, term: event.term, id: event.id });
        }
        if (event.commit > maxCommit) {
          maxCommit = event.commit;
          added.push({ kind: "commit", seq: event.seq, t: event.t, index: event.commit });
        }
        break;
      case "node.leader_elected":
        added.push({ kind: "elected", seq: event.seq, t: event.t, id: event.id, term: event.term });
        break;
      case "node.stepped_down":
        added.push({
          kind: "stepped_down",
          seq: event.seq,
          t: event.t,
          id: event.id,
          term: event.term,
          reason: event.reason,
        });
        break;
      default:
        break;
    }
  }

  if (added.length === 0 && lastSeq === prev.lastSeq) return prev;
  const { items, truncated } =
    added.length === 0
      ? { items: prev.items, truncated: prev.truncated }
      : limited([...prev.items, ...added], prev.truncated);
  return { ...prev, items, maxTerm, maxCommit, lastSeq, truncated };
}
