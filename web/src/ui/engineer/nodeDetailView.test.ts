import { describe, expect, it } from "vitest";

import type { LogEntry, NodeDetail } from "../../protocol/index.ts";
import { worldFromSnapshot } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { PROGRESS_STATE_LABELS, nodeDetailView } from "./nodeDetailView.ts";

function mini(): World {
  return worldFromSnapshot(loadMiniRound().snapshot, 0);
}

function entry(index: number, committed: boolean, overrides: Partial<LogEntry> = {}): LogEntry {
  return {
    index,
    term: 2,
    type: "normal",
    summary: `deliver r-${String(index)}`,
    committed,
    ...overrides,
  };
}

function detail(overrides: Partial<NodeDetail> = {}): NodeDetail {
  return {
    id: 2,
    role: "follower",
    term: 2,
    vote: 1,
    lead: 1,
    commit: 6,
    applied: 6,
    lastIndex: 7,
    log: [entry(5, true), entry(6, true), entry(7, false)],
    ledger: {},
    electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
    ...overrides,
  };
}

describe("nodeDetailView", () => {
  it("NV-1 ログは index の昇順で、committed はサーバーの値のまま", () => {
    const world = mini();
    const view = nodeDetailView(world, detail());
    expect(view.nodeId).toBe(2);
    expect(view.name).toBe("二の島");
    expect(view.role).toBe("Follower");
    expect(view.commit).toBe(6);
    expect(view.log.map((r) => [r.index, r.committed])).toEqual([
      [5, true],
      [6, true],
      [7, false],
    ]);
    expect(view.log[0]).toEqual({
      index: 5,
      term: 2,
      type: "normal",
      summary: "deliver r-5",
      committed: true,
    });

    // 届いた順が昇順でなければ昇順に並べる。committed は index <= commit と違っても入力の値。
    const odd = nodeDetailView(
      world,
      detail({ log: [entry(7, true), entry(5, false), entry(6, false)] }),
    );
    expect(odd.log.map((r) => [r.index, r.committed])).toEqual([
      [5, false],
      [6, false],
      [7, true],
    ]);
  });

  it("NV-2 最初の index が 1 より大きければ logTruncated", () => {
    const world = mini();
    expect(
      nodeDetailView(world, detail({ log: [entry(1, true), entry(2, false)] })).logTruncated,
    ).toBe(false);
    expect(nodeDetailView(world, detail({ log: [entry(12, true)] })).logTruncated).toBe(true);
    expect(nodeDetailView(world, detail({ log: [] })).logTruncated).toBe(false);
  });

  it("NV-3 Progress の行（State の表記・遅れ・島の名前）", () => {
    const view = nodeDetailView(
      mini(),
      detail({
        id: 1,
        role: "leader",
        lead: 1,
        lastIndex: 7,
        progress: [
          { id: 2, match: 4, next: 5, state: "probe", recentActive: false },
          { id: 3, match: 7, next: 8, state: "replicate", recentActive: true },
        ],
      }),
    );
    expect(view.progress).toEqual([
      { id: 2, name: "二の島", match: 4, next: 5, state: "Probe", recentActive: false, behind: 3 },
      {
        id: 3,
        name: "三の島",
        match: 7,
        next: 8,
        state: "Replicate",
        recentActive: true,
        behind: 0,
      },
    ]);
    expect(view.leaderId).toBeNull();
    expect(PROGRESS_STATE_LABELS).toEqual({
      probe: "Probe",
      replicate: "Replicate",
      snapshot: "Snapshot",
    });
  });

  it("NV-4 Progress が無ければ null。leaderId は lead が 0 でも自分でもないときだけ", () => {
    const world = mini();
    const follower = nodeDetailView(world, detail({ lead: 3 }));
    expect(follower.progress).toBeNull();
    expect(follower.leaderId).toBe(3);
    expect(nodeDetailView(world, detail({ lead: 0 })).leaderId).toBeNull();
    expect(nodeDetailView(world, detail({ id: 2, lead: 2 })).leaderId).toBeNull();
    // 空の配列は null にしない（Follower がいない Leader）。
    expect(nodeDetailView(world, detail({ id: 1, lead: 1, progress: [] })).progress).toEqual([]);
  });

  it("NV-5 知らない state・type・role は値のまま、空の summary は —。match > lastIndex なら遅れ 0", () => {
    const view = nodeDetailView(
      mini(),
      detail({
        id: 9,
        role: "observer" as NodeDetail["role"],
        lastIndex: 3,
        log: [entry(1, true, { summary: "", type: "weird" as LogEntry["type"] })],
        progress: [{ id: 2, match: 5, next: 6, state: "weird" as "probe", recentActive: true }],
      }),
    );
    expect(view.name).toBe("島 9");
    expect(view.role).toBe("observer");
    expect(view.log[0]?.summary).toBe("—");
    expect(view.log[0]?.type).toBe("weird");
    expect(view.progress?.[0]?.state).toBe("weird");
    expect(view.progress?.[0]?.behind).toBe(0);
  });

  it("NV-6 入力を書き換えない", () => {
    const world = mini();
    const input = detail({
      log: [entry(7, true), entry(5, true), entry(6, false)].map((e) => Object.freeze(e)),
      progress: [
        Object.freeze({ id: 3, match: 1, next: 2, state: "probe" as const, recentActive: true }),
      ],
    });
    Object.freeze(input.log);
    Object.freeze(input.progress);
    Object.freeze(input);
    const before = JSON.stringify(input);
    const view = nodeDetailView(world, input);
    expect(view.log.map((r) => r.index)).toEqual([5, 6, 7]);
    expect(JSON.stringify(input)).toBe(before);
  });
});
