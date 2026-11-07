import { describe, expect, it } from "vitest";

import type { NodeSummary, ServerEvent, Snapshot } from "../protocol/index.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import {
  EMPTY_TIMELINE,
  TIMELINE_LIMIT,
  appendTimelineEvents,
  timelineFromSnapshot,
  type Timeline,
} from "./timeline.ts";

function node(id: number, patch: Partial<NodeSummary> = {}): NodeSummary {
  return {
    id,
    role: "follower",
    term: 1,
    vote: 0,
    lead: 0,
    commit: 0,
    applied: 0,
    lastIndex: 0,
    ...patch,
  };
}

function snapshotWith(
  nodes: NodeSummary[],
  opts: { sessionId?: string; lastSeq?: number; t?: number } = {},
): Snapshot {
  const { snapshot } = loadMiniRound();
  return {
    ...snapshot,
    session: { ...snapshot.session, id: opts.sessionId ?? "s1" },
    clock: { ...snapshot.clock, t: opts.t ?? 0 },
    nodes,
    lastSeq: opts.lastSeq ?? 10,
  };
}

function nodeState(seq: number, t: number, id: number, patch: Partial<NodeSummary>): ServerEvent {
  return { type: "node.state", seq, t, ...node(id, patch) };
}

function elected(seq: number, t: number, id: number, term: number): ServerEvent {
  return { type: "node.leader_elected", seq, t, id, term };
}

function steppedDown(seq: number, t: number, id: number, term: number): ServerEvent {
  return { type: "node.stepped_down", seq, t, id, term, reason: "check_quorum" };
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object") {
    for (const child of Object.values(value)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}

function started(): Timeline {
  return timelineFromSnapshot(EMPTY_TIMELINE, snapshotWith([node(1), node(2), node(3)]));
}

describe("timelineFromSnapshot", () => {
  it("TL-1 新しいセッションのスナップショット（leader なし）", () => {
    const timeline = started();
    expect(timeline.sessionId).toBe("s1");
    expect(timeline.items).toEqual([]);
    expect(timeline.maxTerm).toBe(1);
    expect(timeline.maxCommit).toBe(0);
    expect(timeline.baseTerm).toBe(1);
    expect(timeline.baseCommit).toBe(0);
    expect(timeline.lastSeq).toBe(10);
    expect(timeline.truncated).toBe(false);
  });

  it("TL-5 同じセッションのスナップショット → resync の後に term", () => {
    const next = timelineFromSnapshot(
      started(),
      snapshotWith(
        [node(1, { term: 2 }), node(2, { term: 3, role: "leader" }), node(3, { term: 3 })],
        { lastSeq: 40, t: 50000 },
      ),
    );
    expect(next.items).toEqual([
      { kind: "resync", seq: 40, t: 50000, initial: false, leaders: [{ id: 2, term: 3 }] },
      { kind: "term", seq: 40, t: 50000, term: 3, id: 2 },
    ]);
    expect(next.maxTerm).toBe(3);
    expect(next.baseTerm).toBe(1);
    expect(next.lastSeq).toBe(40);
  });

  it("TL-5 Commit が進んでいれば commit も足す。下がっていれば最大を保つ", () => {
    const before = appendTimelineEvents(started(), [nodeState(11, 1000, 1, { commit: 5 })]);
    const up = timelineFromSnapshot(
      before,
      snapshotWith([node(1, { commit: 7 }), node(2), node(3)], { lastSeq: 20, t: 2000 }),
    );
    expect(up.items.slice(-1)).toEqual([{ kind: "commit", seq: 20, t: 2000, index: 7 }]);
    expect(up.maxCommit).toBe(7);

    const down = timelineFromSnapshot(
      up,
      snapshotWith([node(1, { commit: 3 }), node(2), node(3)], { lastSeq: 30, t: 3000 }),
    );
    expect(down.items.slice(-1)).toEqual([
      { kind: "resync", seq: 30, t: 3000, initial: false, leaders: [] },
    ]);
    expect(down.maxCommit).toBe(7);
  });

  it("TL-5b 新しいセッションのスナップショットで leader が島 1 → resync（initial）が 1 件だけ", () => {
    const timeline = timelineFromSnapshot(
      EMPTY_TIMELINE,
      snapshotWith([node(3, { term: 2 }), node(1, { term: 2, role: "leader" }), node(2)], {
        lastSeq: 7,
        t: 12000,
      }),
    );
    expect(timeline.items).toEqual([
      { kind: "resync", seq: 7, t: 12000, initial: true, leaders: [{ id: 1, term: 2 }] },
    ]);
    expect(timeline.maxTerm).toBe(2);
    expect(timeline.baseTerm).toBe(2);
  });

  it("TL-6 別のセッションのスナップショット → 作り直す", () => {
    const old = appendTimelineEvents(started(), [
      nodeState(11, 1000, 1, { term: 4, commit: 9 }),
      elected(12, 1500, 1, 4),
    ]);
    const next = timelineFromSnapshot(
      old,
      snapshotWith([node(1), node(2)], { sessionId: "s2", lastSeq: 3 }),
    );
    expect(next.sessionId).toBe("s2");
    expect(next.items).toEqual([]);
    expect(next.maxTerm).toBe(1);
    expect(next.maxCommit).toBe(0);
    expect(next.lastSeq).toBe(3);
  });

  it("ノードが無いスナップショットでは最大は 0", () => {
    const timeline = timelineFromSnapshot(EMPTY_TIMELINE, snapshotWith([]));
    expect(timeline.maxTerm).toBe(0);
    expect(timeline.maxCommit).toBe(0);
    expect(timeline.items).toEqual([]);
  });
});

describe("appendTimelineEvents", () => {
  it("TL-2 Term・Commit の増加だけを記録する", () => {
    let timeline = appendTimelineEvents(started(), [nodeState(11, 30000, 2, { term: 2 })]);
    expect(timeline.items).toEqual([{ kind: "term", seq: 11, t: 30000, term: 2, id: 2 }]);
    timeline = appendTimelineEvents(timeline, [nodeState(12, 31000, 3, { term: 2 })]);
    expect(timeline.items).toHaveLength(1);
    expect(timeline.lastSeq).toBe(12);
    timeline = appendTimelineEvents(timeline, [nodeState(13, 32000, 2, { term: 2, commit: 1 })]);
    expect(timeline.items.slice(1)).toEqual([{ kind: "commit", seq: 13, t: 32000, index: 1 }]);
    expect(timeline.maxTerm).toBe(2);
    expect(timeline.maxCommit).toBe(1);
  });

  it("TL-2 同じイベントで Term と Commit が上がれば term を先に", () => {
    const timeline = appendTimelineEvents(started(), [
      nodeState(11, 5000, 1, { term: 3, commit: 2 }),
    ]);
    expect(timeline.items.map((item) => item.kind)).toEqual(["term", "commit"]);
  });

  it("TL-3 leader_elected・stepped_down が順に入る。ほかの種類は見ない", () => {
    const { events } = loadMiniRound();
    const other = events.find((event) => event.type === "letter.created");
    if (other === undefined) throw new Error("letter.created がない");
    const timeline = appendTimelineEvents(started(), [
      elected(11, 1000, 1, 2),
      { ...other, seq: 12 },
      steppedDown(13, 21000, 1, 2),
    ]);
    expect(timeline.items).toEqual([
      { kind: "elected", seq: 11, t: 1000, id: 1, term: 2 },
      { kind: "stepped_down", seq: 13, t: 21000, id: 1, term: 2, reason: "check_quorum" },
    ]);
    expect(timeline.lastSeq).toBe(13);
  });

  it("TL-4 seq が lastSeq 以下のイベント → 同じオブジェクト", () => {
    const timeline = appendTimelineEvents(started(), [elected(11, 1000, 1, 2)]);
    expect(appendTimelineEvents(timeline, [elected(11, 1000, 1, 2), elected(5, 0, 2, 2)])).toBe(
      timeline,
    );
    expect(appendTimelineEvents(timeline, [])).toBe(timeline);
  });

  it("TL-4 項目を足さなくても seq が進めば lastSeq を更新する", () => {
    const timeline = started();
    const next = appendTimelineEvents(timeline, [nodeState(11, 1000, 1, { term: 1 })]);
    expect(next).not.toBe(timeline);
    expect(next.items).toBe(timeline.items);
    expect(next.lastSeq).toBe(11);
  });

  it("TL-7 上限を超えたら先頭を捨てて truncated", () => {
    const events: ServerEvent[] = [];
    for (let i = 1; i <= TIMELINE_LIMIT + 5; i++) {
      events.push(nodeState(10 + i, i * 100, 1, { commit: i }));
    }
    const timeline = appendTimelineEvents(started(), events);
    expect(timeline.items).toHaveLength(TIMELINE_LIMIT);
    expect(timeline.items[0]).toEqual({ kind: "commit", seq: 16, t: 600, index: 6 });
    expect(timeline.truncated).toBe(true);
    expect(timeline.maxCommit).toBe(TIMELINE_LIMIT + 5);
  });

  it("TL-8 引数を書き換えない", () => {
    const first = deepFreeze(started());
    const events = deepFreeze([
      nodeState(11, 1000, 1, { term: 2, commit: 1 }),
      elected(12, 1000, 1, 2),
      steppedDown(13, 2000, 1, 2),
    ]);
    const next = appendTimelineEvents(first, events);
    expect(next.items).toHaveLength(4);
    const snapshot = deepFreeze(
      snapshotWith([node(1, { term: 5, role: "leader", commit: 4 })], { lastSeq: 20 }),
    );
    expect(() => timelineFromSnapshot(deepFreeze(next), snapshot)).not.toThrow();
    expect(() => timelineFromSnapshot(EMPTY_TIMELINE, snapshot)).not.toThrow();
    expect(Object.isFrozen(EMPTY_TIMELINE)).toBe(true);
  });
});
