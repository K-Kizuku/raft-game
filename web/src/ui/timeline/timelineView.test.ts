import { describe, expect, it } from "vitest";

import { EMPTY_TIMELINE, type Timeline, type TimelineItem } from "../../state/index.ts";
import {
  buildTimelineView,
  formatTimelineTime,
  timelineTicks,
  timeToX,
  type TimelineIsland,
} from "./timelineView.ts";

const ISLANDS: TimelineIsland[] = [
  { id: 1, name: "一の島", color: "red" },
  { id: 2, name: "二の島", color: "green" },
  { id: 3, name: "三の島", color: "blue" },
];

function timeline(items: TimelineItem[], patch: Partial<Timeline> = {}): Timeline {
  return { ...EMPTY_TIMELINE, sessionId: "s1", baseTerm: 1, maxTerm: 1, items, ...patch };
}

function view(items: TimelineItem[], patch: Partial<Timeline> = {}, endT = 120000) {
  return buildTimelineView({ timeline: timeline(items, patch), endT, islands: ISLANDS });
}

function segmentsOf(v: ReturnType<typeof view>, id: number) {
  const row = v.leaderRows.find((r) => r.island.id === id);
  if (row === undefined) throw new Error(`島 ${id} の行がない`);
  return row.segments;
}

describe("buildTimelineView", () => {
  it("TV-1 Term の帯", () => {
    const v = view([
      { kind: "term", seq: 2, t: 30000, term: 2, id: 1 },
      { kind: "term", seq: 3, t: 50000, term: 3, id: 2 },
    ]);
    expect(v.startT).toBe(0);
    expect(v.endT).toBe(120000);
    expect(v.termBands).toEqual([
      { term: 1, from: 0, to: 30000 },
      { term: 2, from: 30000, to: 50000 },
      { term: 3, from: 50000, to: 120000 },
    ]);
  });

  it("TV-1 Term 0 の帯は作らない", () => {
    const v = view([{ kind: "term", seq: 2, t: 10000, term: 1, id: 1 }], { baseTerm: 0 });
    expect(v.termBands).toEqual([{ term: 1, from: 10000, to: 120000 }]);
  });

  it("TV-2 Leader の行", () => {
    const v = view([
      { kind: "elected", seq: 1, t: 0, id: 1, term: 1 },
      { kind: "stepped_down", seq: 2, t: 35000, id: 1, term: 1, reason: "check_quorum" },
      { kind: "elected", seq: 3, t: 40000, id: 3, term: 2 },
    ]);
    expect(v.leaderRows.map((row) => row.island.id)).toEqual([1, 2, 3]);
    expect(segmentsOf(v, 1)).toEqual([{ from: 0, to: 35000, term: 1, open: false }]);
    expect(segmentsOf(v, 2)).toEqual([]);
    expect(segmentsOf(v, 3)).toEqual([{ from: 40000, to: 120000, term: 2, open: true }]);
  });

  it("TV-3 2 つの Leader の区間が重なる", () => {
    const v = view([
      { kind: "elected", seq: 1, t: 0, id: 1, term: 1 },
      { kind: "elected", seq: 2, t: 60000, id: 3, term: 2 },
      { kind: "stepped_down", seq: 3, t: 70000, id: 1, term: 1, reason: "check_quorum" },
    ]);
    expect(segmentsOf(v, 1)).toEqual([{ from: 0, to: 70000, term: 1, open: false }]);
    expect(segmentsOf(v, 3)).toEqual([{ from: 60000, to: 120000, term: 2, open: true }]);
  });

  it("TV-3 同じ島の次の elected で前の区間を閉じる", () => {
    const v = view([
      { kind: "elected", seq: 1, t: 0, id: 2, term: 1 },
      { kind: "elected", seq: 2, t: 50000, id: 2, term: 3 },
    ]);
    expect(segmentsOf(v, 2)).toEqual([
      { from: 0, to: 50000, term: 1, open: false },
      { from: 50000, to: 120000, term: 3, open: true },
    ]);
  });

  it("TV-4 resync で開いていた区間が閉じ、leaders の島で開き直す", () => {
    const v = view([
      { kind: "elected", seq: 1, t: 0, id: 1, term: 1 },
      { kind: "resync", seq: 9, t: 45000, initial: false, leaders: [{ id: 2, term: 3 }] },
      { kind: "term", seq: 9, t: 45000, term: 3, id: 2 },
    ]);
    expect(segmentsOf(v, 1)).toEqual([{ from: 0, to: 45000, term: 1, open: false }]);
    expect(segmentsOf(v, 2)).toEqual([{ from: 45000, to: 120000, term: 3, open: true }]);
    expect(v.resyncs).toEqual([45000]);
    expect(v.lines[1]?.text).toBe(
      "サーバーにつなぎ直した（この前の出来事は一部欠けている可能性があります）",
    );
  });

  it("TV-4 initial の resync は印を出さず、Leader の区間を開く", () => {
    const v = view([
      {
        kind: "resync",
        seq: 5,
        t: 20000,
        initial: true,
        leaders: [
          { id: 1, term: 2 },
          { id: 3, term: 1 },
        ],
      },
    ]);
    expect(v.resyncs).toEqual([]);
    expect(segmentsOf(v, 1)).toEqual([{ from: 20000, to: 120000, term: 2, open: true }]);
    expect(v.lines).toEqual([
      { t: 20000, text: "記録を始めた（このときの Leader: 一の島、三の島）" },
    ]);
  });

  it("対応する elected の無い stepped_down は左端から開いていたものとして閉じる", () => {
    const v = view([
      { kind: "stepped_down", seq: 2, t: 15000, id: 2, term: 1, reason: "higher_term" },
    ]);
    expect(segmentsOf(v, 2)).toEqual([{ from: 0, to: 15000, term: 1, open: false }]);
  });

  it("TV-5 続く Commit を 1 行にまとめる", () => {
    const commits: TimelineItem[] = [
      { kind: "commit", seq: 1, t: 1000, index: 1 },
      { kind: "commit", seq: 2, t: 2000, index: 2 },
      { kind: "commit", seq: 3, t: 3000, index: 3 },
    ];
    expect(view(commits).lines).toEqual([
      { t: 1000, text: "Commit Index が 0 → 3 に進んだ（3 回）" },
    ]);
    expect(view(commits).commits).toEqual([
      { t: 1000, index: 1 },
      { t: 2000, index: 2 },
      { t: 3000, index: 3 },
    ]);

    const split = view([
      commits[0] as TimelineItem,
      { kind: "elected", seq: 2, t: 1500, id: 1, term: 1 },
      { kind: "commit", seq: 3, t: 2000, index: 2 },
      { kind: "commit", seq: 4, t: 3000, index: 3 },
    ]);
    expect(split.lines.map((line) => line.text)).toEqual([
      "Commit Index が 1 になった",
      "一の島 が Leader になった（Term 1）",
      "Commit Index が 1 → 3 に進んだ（2 回）",
    ]);
  });

  it("TV-5 まとめの最初の前の値は baseCommit", () => {
    const v = view(
      [
        { kind: "commit", seq: 1, t: 1000, index: 11 },
        { kind: "commit", seq: 2, t: 2000, index: 12 },
      ],
      { baseCommit: 10, maxCommit: 10 },
    );
    expect(v.lines[0]?.text).toBe("Commit Index が 10 → 12 に進んだ（2 回）");
  });

  it("TV-6 理由の文と名前の無い島", () => {
    const v = view([
      { kind: "elected", seq: 1, t: 0, id: 4, term: 2 },
      { kind: "stepped_down", seq: 2, t: 1000, id: 4, term: 2, reason: "check_quorum" },
      { kind: "stepped_down", seq: 3, t: 2000, id: 1, term: 3, reason: "higher_term" },
      { kind: "term", seq: 4, t: 3000, term: 4, id: 2 },
    ]);
    expect(v.lines.map((line) => line.text)).toEqual([
      "島 4 が Leader になった（Term 2）",
      "島 4 が Leader をおりた（Term 2・CheckQuorum：過半数と連絡が取れない）",
      "一の島 が Leader をおりた（Term 3・より新しい Term を知った）",
      "Term が 4 に上がった（二の島 から）",
    ]);
    // 名前の無い島も行を作る（islands の後ろ）
    expect(v.leaderRows.map((row) => row.island.name)).toEqual([
      "一の島",
      "二の島",
      "三の島",
      "島 4",
    ]);
  });

  it("truncated をそのまま渡す。空の記録は空の一覧", () => {
    expect(view([], { truncated: true }).truncated).toBe(true);
    const empty = view([]);
    expect(empty.lines).toEqual([]);
    expect(empty.termBands).toEqual([{ term: 1, from: 0, to: 120000 }]);
  });
});

describe("timeToX・目盛り", () => {
  it("TV-7 timeToX", () => {
    const range = { startT: 0, endT: 120000 };
    expect(timeToX(0, range, 1000)).toBe(0);
    expect(timeToX(120000, range, 1000)).toBe(1000);
    expect(timeToX(60000, range, 1000)).toBe(500);
    expect(timeToX(10, { startT: 0, endT: 0 }, 1000)).toBe(0);
  });

  it("30 秒ごとの目盛りと m:ss", () => {
    expect(timelineTicks({ startT: 0, endT: 95000 }).map((tick) => tick.label)).toEqual([
      "0:00",
      "0:30",
      "1:00",
      "1:30",
    ]);
    expect(formatTimelineTime(59999)).toBe("0:59");
    expect(formatTimelineTime(180000)).toBe("3:00");
  });
});
