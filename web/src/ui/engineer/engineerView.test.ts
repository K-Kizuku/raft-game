import { describe, expect, it } from "vitest";

import type { Letter, NodeDetail, Snapshot } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import {
  ENGINEER_LETTER_LIMIT,
  ROLE_LABELS,
  findLetter,
  letterFields,
  letterSummary,
  locText,
  nodeRows,
  roleLabel,
  targetNodeId,
} from "./engineerView.ts";

// mini の記録の seq 1〜n を入れた世界。
function mini(n: number): World {
  const { snapshot, events } = loadMiniRound();
  return applyEvents(worldFromSnapshot(snapshot, 0), events.slice(0, n));
}

function detail(id: number, vote: number): NodeDetail {
  return {
    id,
    role: "leader",
    term: 2,
    vote,
    lead: 1,
    commit: 0,
    applied: 0,
    lastIndex: 1,
    log: [],
    ledger: {},
    electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
  };
}

function letterOf(world: World, id: number): Letter {
  const letter = world.letters[id];
  if (letter === undefined) throw new Error(`手紙 ${String(id)} が無い`);
  return letter;
}

function withoutRaft(letter: Letter): Letter {
  const copy: Letter = { ...letter };
  delete copy.raft;
  return copy;
}

function requestLetter(overrides: Partial<Letter> = {}): Letter {
  return {
    id: 7,
    kind: "request",
    from: 2,
    to: 1,
    createdAt: 2500,
    loc: { type: "port", islandId: 2 },
    label: "request",
    request: { reqId: "r-1", hops: 2 },
    ...overrides,
  };
}

describe("engineerView", () => {
  it("EV-1 roleLabel", () => {
    expect(Object.keys(ROLE_LABELS)).toHaveLength(4);
    const cases: [string, string][] = [
      ["follower", "Follower"],
      ["pre_candidate", "PreCandidate"],
      ["candidate", "Candidate"],
      ["leader", "Leader"],
      ["observer", "observer"],
      ["constructor", "constructor"],
    ];
    for (const [role, want] of cases) {
      expect(roleLabel(role)).toBe(want);
    }
  });

  it("EV-2 targetNodeId", () => {
    const w5 = mini(5);
    const cases: { world: World | null; selected: number | null; want: number | null }[] = [
      { world: w5, selected: 2, want: 2 },
      { world: w5, selected: null, want: 1 },
      { world: w5, selected: 9, want: 1 },
      { world: mini(25), selected: null, want: 1 },
      { world: null, selected: 1, want: null },
      { world: { ...w5, nodes: {} }, selected: 1, want: null },
    ];
    for (const { world, selected, want } of cases) {
      expect(targetNodeId(world, selected)).toBe(want);
    }
    // seq 25 の島 1 は Leader でない（Leader がいないので、最も小さい ID）。
    expect(mini(25).nodes[1]?.role).toBe("follower");
    // 最も小さい ID は nodes の並びに依らない。
    const w = mini(25);
    const reordered = { ...w, nodes: { 3: w.nodes[3], 2: w.nodes[2] } } as World;
    expect(targetNodeId(reordered, null)).toBe(2);
  });

  it("EV-3 nodeRows", () => {
    const w = mini(5);
    const rows = nodeRows(w, null, 1);
    expect(rows.map((r) => r.id)).toEqual([1, 2, 3]);
    expect(rows.map((r) => r.name)).toEqual(["一の島", "二の島", "三の島"]);
    expect(rows[0]).toMatchObject({
      role: "leader",
      roleLabel: "Leader",
      term: 2,
      lead: 1,
      lastIndex: 1,
      target: true,
    });
    expect(rows.map((r) => r.vote)).toEqual([null, null, null]);
    expect(rows.map((r) => r.target)).toEqual([true, false, false]);

    expect(nodeRows(w, detail(1, 1), 1).map((r) => r.vote)).toEqual([1, null, null]);
    expect(nodeRows(w, detail(2, 1), 1).map((r) => r.vote)).toEqual([null, 1, null]);
    expect(nodeRows(w, null, 3).map((r) => r.target)).toEqual([false, false, true]);
    expect(nodeRows(w, null, null).map((r) => r.target)).toEqual([false, false, false]);

    const { snapshot } = loadMiniRound();
    const noStage: Snapshot = {
      ...snapshot,
      stage: { ...snapshot.stage, islands: [], routes: [] },
      nodes: [...snapshot.nodes].reverse(),
    };
    const empty = worldFromSnapshot(noStage, 0);
    const emptyRows = nodeRows(empty, null, null);
    expect(emptyRows.map((r) => r.id)).toEqual([1, 2, 3]);
    expect(emptyRows.map((r) => r.name)).toEqual(["島 1", "島 2", "島 3"]);
  });

  it("EV-4 findLetter", () => {
    const w9 = mini(9);
    expect(findLetter(w9, 1).status).toBe("present");
    expect(findLetter(w9, 2).status).toBe("present");

    const delivered = findLetter(mini(14), 1);
    expect(delivered).toMatchObject({ status: "delivered", islandId: 2, accepted: true, t: 7000 });
    if (delivered.status !== "delivered") throw new Error("配達済みでない");
    expect(delivered.letter.label).toBe("heartbeat");

    expect(findLetter(mini(19), 3)).toMatchObject({ status: "dropped", reason: "superseded" });
    expect(findLetter(mini(30), 99)).toEqual({ status: "missing" });
  });

  it("EV-5 letterFields", () => {
    const fields = letterFields(letterOf(mini(9), 2));
    expect(fields.map((f) => f.name)).toEqual([
      "Type",
      "From",
      "To",
      "Term",
      "LogTerm",
      "Index",
      "Commit",
      "Reject",
      "RejectHint",
      "Entries",
      "Stale",
      "Label",
      "CreatedAt",
    ]);
    expect(fields.map((f) => f.value)).toEqual([
      "MsgApp",
      "1",
      "3",
      "2",
      "2",
      "1",
      "0",
      "false",
      "0",
      "1",
      "false",
      "append",
      "0:01",
    ]);

    const stale = letterFields(letterOf(mini(20), 2));
    expect(stale.find((f) => f.name === "Stale")?.value).toBe("true");

    expect(letterFields(requestLetter())).toEqual([
      { name: "Type", value: "依頼書（Raft のメッセージではない）" },
      { name: "From", value: "2" },
      { name: "To", value: "1" },
      { name: "ReqId", value: "r-1" },
      { name: "Hops", value: "2" },
      { name: "Label", value: "request" },
      { name: "CreatedAt", value: "0:02" },
    ]);
    const noRequest = letterFields(requestLetter({ request: undefined }));
    expect(noRequest.filter((f) => f.name === "ReqId" || f.name === "Hops")).toEqual([
      { name: "ReqId", value: "—" },
      { name: "Hops", value: "—" },
    ]);

    const noRaft = withoutRaft(letterOf(mini(9), 1));
    expect(letterFields(noRaft)).toEqual([
      { name: "Type", value: "—" },
      { name: "From", value: "1" },
      { name: "To", value: "2" },
      { name: "Label", value: "heartbeat" },
      { name: "CreatedAt", value: "0:01" },
    ]);
  });

  it("EV-6 locText・letterSummary", () => {
    const w = mini(9);
    const cases: [Letter["loc"], string][] = [
      [{ type: "port", islandId: 1 }, "港: 一の島"],
      [{ type: "ferry", ferryId: "f1" }, "船: テスト丸"],
      [{ type: "ferry", ferryId: "f9" }, "船: f9"],
      [{ type: "ferry" }, "船: —"],
      [{ type: "relay", islandId: 3 }, "中継倉庫: 三の島"],
      [{ type: "delivered" }, "配達済み"],
      [{ type: "dropped" }, "処分"],
      [{ type: "port" }, "港: —"],
    ];
    for (const [loc, want] of cases) {
      expect(locText(w, loc)).toBe(want);
    }

    expect(letterSummary(letterOf(w, 2))).toBe("#2 MsgApp 1→3");
    expect(letterSummary(requestLetter())).toBe("#7 依頼書 2→1");
    const noRaft = withoutRaft(letterOf(w, 1));
    expect(letterSummary(noRaft)).toBe("#1 heartbeat 1→2");
    expect(ENGINEER_LETTER_LIMIT).toBe(50);
  });
});
