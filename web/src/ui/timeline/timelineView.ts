// タイムラインの記録（web/src/state/timeline.ts）から、図の 3 段（Term の帯・島ごとの Leader の区間・Commit の階段）と
// 一覧の文を作る純粋な関数（T-701・FR-ENG-09）。記録を並べ直すだけで、どれが正しい Leader かを決めない。
// 2 つの島が同時に Leader の区間を持つこと（嵐で孤立した古い Leader）もそのまま出す（ゲームデザイン §7.4）。
// 文はエンジニアモードなので、用語の併記の設定に関係なく Raft の用語で書く。整数の演算だけで書く。
// ゲーム中のエンジニアパネルでも使えるよう、web/src/app/** を import しない。
import type { StepDownReason } from "../../protocol/index.ts";
import type { Timeline, TimelineItem } from "../../state/index.ts";
import { tokens } from "../theme/tokens.ts";

export type TimelineIsland = Readonly<{ id: number; name: string; color: string }>;
export type TimelineViewInput = Readonly<{
  timeline: Timeline;
  endT: number; // 横軸の右端（ゲーム内時刻 ms）。呼ぶ側が world.clock.endsAt と最後の項目の t の大きい方を渡す
  islands: readonly TimelineIsland[]; // world.stage の islandIds の順
}>;
export type TermBand = Readonly<{ term: number; from: number; to: number }>;
// open = 終わりを見ていない（endT まで伸ばした）
export type LeaderSegment = Readonly<{ from: number; to: number; term: number; open: boolean }>;
export type LeaderRow = Readonly<{ island: TimelineIsland; segments: readonly LeaderSegment[] }>;
export type CommitPoint = Readonly<{ t: number; index: number }>;
export type TimelineLine = Readonly<{ t: number; text: string }>;
export type TimelineView = Readonly<{
  startT: number; // 横軸の左端。最初の項目の t と 0 の小さい方（ふつうは 0）
  endT: number;
  termBands: readonly TermBand[]; // Term ごとの区間（次の term の項目まで。最後は endT まで）
  leaderRows: readonly LeaderRow[]; // islands の順。代表だった区間が 1 つも無い島も行を作る
  commits: readonly CommitPoint[];
  resyncs: readonly number[]; // つなぎ直した時刻（initial: false の resync だけ）
  lines: readonly TimelineLine[]; // 一覧の文（古い順）
  truncated: boolean;
}>;
export type TimelineTick = Readonly<{ t: number; label: string }>;

// 横軸の目盛りの間隔（画面の値で、ゲームの数値ではない）。
export const TICK_INTERVAL_MS = 30000;

const STEP_DOWN_TEXT: Readonly<Record<StepDownReason, string>> = {
  check_quorum: "CheckQuorum：過半数と連絡が取れない",
  higher_term: "より新しい Term を知った",
};

// ミリ秒を秒に切り捨てて `m:ss` にする（web/src/app/format.ts の formatDuration と同じ書式。ui から app を import しない）。
export function formatTimelineTime(ms: number): string {
  const totalSeconds = (ms - (ms % 1000)) / 1000;
  const seconds = totalSeconds % 60;
  const minutes = (totalSeconds - seconds) / 60;
  return `${minutes}:${seconds < 10 ? "0" : ""}${seconds}`;
}

// 0 から TICK_INTERVAL_MS ごとの目盛りのうち、endT までのもの（startT はいつも 0 以下）。
export function timelineTicks(view: Pick<TimelineView, "startT" | "endT">): TimelineTick[] {
  const ticks: TimelineTick[] = [];
  for (let t = Math.max(view.startT, 0); t <= view.endT; t += TICK_INTERVAL_MS) {
    ticks.push({ t, label: formatTimelineTime(t) });
  }
  return ticks;
}

export function timeToX(
  t: number,
  view: Pick<TimelineView, "startT" | "endT">,
  width: number,
): number {
  const span = view.endT - view.startT;
  if (span <= 0) return 0;
  const offset = Math.min(Math.max(t - view.startT, 0), span);
  return Math.round((offset * width) / span);
}

type IslandLookup = (id: number) => TimelineIsland;

function islandLookup(islands: readonly TimelineIsland[]): IslandLookup {
  const byId = new Map<number, TimelineIsland>();
  for (const island of islands) byId.set(island.id, island);
  return (id) => byId.get(id) ?? { id, name: `島 ${id}`, color: tokens["--color-ink-muted"] };
}

function buildTermBands(timeline: Timeline, startT: number, endT: number): TermBand[] {
  const bands: TermBand[] = [];
  let term = timeline.baseTerm;
  let from = startT;
  const close = (to: number): void => {
    if (term > 0 && to > from) bands.push({ term, from, to });
  };
  for (const item of timeline.items) {
    if (item.kind !== "term") continue;
    close(item.t);
    term = item.term;
    from = item.t;
  }
  close(endT);
  return bands;
}

function buildLeaderRows(
  items: readonly TimelineItem[],
  islands: readonly TimelineIsland[],
  lookup: IslandLookup,
  startT: number,
  endT: number,
): LeaderRow[] {
  const segments = new Map<number, LeaderSegment[]>();
  const opened = new Map<number, { from: number; term: number }>();
  const order: number[] = islands.map((island) => island.id);
  let resynced = false;

  const segmentsOf = (id: number): LeaderSegment[] => {
    let list = segments.get(id);
    if (list === undefined) {
      list = [];
      segments.set(id, list);
      if (!order.includes(id)) order.push(id);
    }
    return list;
  };
  const close = (id: number, to: number): void => {
    const open = opened.get(id);
    if (open === undefined) return;
    segmentsOf(id).push({ from: open.from, to, term: open.term, open: false });
    opened.delete(id);
  };

  for (const item of items) {
    switch (item.kind) {
      case "elected":
        close(item.id, item.t);
        segmentsOf(item.id);
        opened.set(item.id, { from: item.t, term: item.term });
        break;
      case "stepped_down":
        if (opened.has(item.id)) {
          close(item.id, item.t);
        } else if (!resynced && segmentsOf(item.id).length === 0) {
          // 記録の途中から見た（対応する elected が無い）→ 左端から開いていたものとして閉じる
          segmentsOf(item.id).push({ from: startT, to: item.t, term: item.term, open: false });
        }
        break;
      case "resync":
        resynced = true;
        for (const id of [...opened.keys()]) close(id, item.t);
        for (const leader of item.leaders) {
          segmentsOf(leader.id);
          opened.set(leader.id, { from: item.t, term: leader.term });
        }
        break;
      default:
        break;
    }
  }
  for (const [id, open] of opened) {
    segmentsOf(id).push({ from: open.from, to: endT, term: open.term, open: true });
  }

  const known = order.slice(0, islands.length);
  const unknown = order.slice(islands.length).sort((a, b) => a - b);
  return [...known, ...unknown].map((id) => ({
    island: lookup(id),
    segments: [...(segments.get(id) ?? [])].sort((a, b) => a.from - b.from),
  }));
}

function buildLines(timeline: Timeline, lookup: IslandLookup): TimelineLine[] {
  const lines: TimelineLine[] = [];
  const items = timeline.items;
  let commit = timeline.baseCommit;
  let i = 0;
  while (i < items.length) {
    const item = items[i];
    if (item === undefined) break;
    switch (item.kind) {
      case "commit": {
        // 続けて並ぶ commit の項目（間にほかの種類が無いもの）を 1 行にまとめる
        let j = i;
        let last = item;
        while (j + 1 < items.length) {
          const next = items[j + 1];
          if (next === undefined || next.kind !== "commit") break;
          last = next;
          j++;
        }
        const count = j - i + 1;
        lines.push({
          t: item.t,
          text:
            count === 1
              ? `Commit Index が ${item.index} になった`
              : `Commit Index が ${commit} → ${last.index} に進んだ（${count} 回）`,
        });
        commit = last.index;
        i = j + 1;
        continue;
      }
      case "elected":
        lines.push({
          t: item.t,
          text: `${lookup(item.id).name} が Leader になった（Term ${item.term}）`,
        });
        break;
      case "stepped_down":
        lines.push({
          t: item.t,
          text: `${lookup(item.id).name} が Leader をおりた（Term ${item.term}・${STEP_DOWN_TEXT[item.reason]}）`,
        });
        break;
      case "term":
        lines.push({
          t: item.t,
          text: `Term が ${item.term} に上がった（${lookup(item.id).name} から）`,
        });
        break;
      case "resync":
        if (item.initial) {
          const names = item.leaders.map((leader) => lookup(leader.id).name).join("、");
          lines.push({
            t: item.t,
            text: names === "" ? "記録を始めた" : `記録を始めた（このときの Leader: ${names}）`,
          });
        } else {
          lines.push({
            t: item.t,
            text: "サーバーにつなぎ直した（この前の出来事は一部欠けている可能性があります）",
          });
        }
        break;
    }
    i++;
  }
  return lines;
}

export function buildTimelineView(input: TimelineViewInput): TimelineView {
  const { timeline, islands } = input;
  const first = timeline.items[0];
  const startT = first === undefined ? 0 : Math.min(first.t, 0);
  const endT = Math.max(input.endT, startT);
  const lookup = islandLookup(islands);
  const commits: CommitPoint[] = [];
  const resyncs: number[] = [];
  for (const item of timeline.items) {
    if (item.kind === "commit") commits.push({ t: item.t, index: item.index });
    if (item.kind === "resync" && !item.initial) resyncs.push(item.t);
  }
  return {
    startT,
    endT,
    termBands: buildTermBands(timeline, startT, endT),
    leaderRows: buildLeaderRows(timeline.items, islands, lookup, startT, endT),
    commits,
    resyncs,
    lines: buildLines(timeline, lookup),
    truncated: timeline.truncated,
  };
}
