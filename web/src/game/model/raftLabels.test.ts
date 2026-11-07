import { describe, expect, it } from "vitest";

import type { Letter, RaftInfo } from "../../protocol/index.ts";
import { worldFromSnapshot } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { dockedFerry, ferryLetter, makeSnapshot, portLetter } from "../testing/snapshot.ts";
import {
  RAFT_TAG_FERRY_OFFSET,
  RAFT_TAG_MAX_LINES,
  RAFT_TAG_PILE_OFFSET,
  buildRaftTags,
  countTypes,
  tagLines,
} from "./raftLabels.ts";
import { buildMapScene } from "./scene.ts";

const SIZE = { width: 800, height: 600 };
const NONE: UiSelection = { islandId: null, ferryId: null };

function raft(type: string, stale = false): RaftInfo {
  return {
    type,
    term: 1,
    logTerm: 0,
    index: 0,
    commit: 0,
    entries: [],
    reject: false,
    rejectHint: 0,
    stale,
  };
}

function withRaft(letter: Letter, type: string, stale = false): Letter {
  return { ...letter, raft: raft(type, stale) };
}

function msg(id: number, type: string, stale = false): Letter {
  return withRaft(portLetter(id, 1, 2), type, stale);
}

function world(letters: Letter[], ferries = [dockedFerry("f1", 1, [20, 21])]): World {
  return worldFromSnapshot(makeSnapshot({ letters, ferries }), 0);
}

describe("countTypes", () => {
  it("RL-1 MsgHeartbeat・MsgApp ×2・依頼書 → MsgApp・MsgHeartbeat・依頼書 の順", () => {
    const letters = [
      msg(1, "MsgHeartbeat"),
      msg(2, "MsgApp"),
      portLetter(3, 1, 2, "request"),
      msg(4, "MsgApp"),
    ];
    expect(countTypes(letters)).toEqual([
      { type: "MsgApp", count: 2 },
      { type: "MsgHeartbeat", count: 1 },
      { type: "依頼書", count: 1 },
    ]);
  });

  it("RL-2 知らない種類は決まった順の後ろに昇順、依頼書と ? はその後ろ", () => {
    const letters = [
      portLetter(1, 1, 2), // raft の無い Raft の手紙
      msg(2, "MsgZ"),
      portLetter(3, 1, 2, "request"),
      msg(4, "MsgA"),
      msg(5, "MsgSnap"),
      msg(6, "MsgVote"),
    ];
    expect(countTypes(letters).map((c) => c.type)).toEqual([
      "MsgVote",
      "MsgSnap",
      "MsgA",
      "MsgZ",
      "依頼書",
      "?",
    ]);
    // 入力の順に関係なく同じ並び。
    expect(countTypes([...letters].reverse())).toEqual(countTypes(letters));
  });
});

describe("tagLines", () => {
  it("RL-3 1 通は × を付けず、上限を超える種類は「+n 種」、stale は最後", () => {
    expect(tagLines([{ type: "MsgApp", count: 1 }], 0)).toEqual(["MsgApp"]);
    expect(tagLines([{ type: "MsgApp", count: 3 }], 0)).toEqual(["MsgApp ×3"]);
    const five = ["MsgVote", "MsgApp", "MsgAppResp", "MsgHeartbeat", "依頼書"].map((type) => ({
      type,
      count: 2,
    }));
    expect(RAFT_TAG_MAX_LINES).toBe(3);
    expect(tagLines(five, 0)).toEqual(["MsgVote ×2", "MsgApp ×2", "MsgAppResp ×2", "+2 種"]);
    expect(tagLines(five, 2)).toEqual([
      "MsgVote ×2",
      "MsgApp ×2",
      "MsgAppResp ×2",
      "+2 種",
      "stale ×2",
    ]);
    expect(tagLines([{ type: "MsgApp", count: 2 }], 1)).toEqual(["MsgApp ×2", "stale ×1"]);
  });
});

describe("buildRaftTags", () => {
  it("RL-4 港の宛先ごとの山と、積み荷のある船に札を作り、位置は山・船の下", () => {
    const w = world([
      msg(1, "MsgApp"),
      msg(2, "MsgApp", true),
      withRaft(portLetter(3, 1, 3), "MsgHeartbeat"),
      portLetter(4, 1, 3, "request"),
      withRaft(ferryLetter(20, "f1", 2), "MsgVote"),
      withRaft(ferryLetter(21, "f1", 3), "MsgVoteResp", true),
    ]);
    const scene = buildMapScene(w, NONE, 0, SIZE);
    const tags = buildRaftTags(w, scene);
    expect(tags.map((t) => t.key)).toEqual(["pile:1:2", "pile:1:3", "ferry:f1"]);
    expect(tags.map((t) => t.lines)).toEqual([
      ["MsgApp ×2", "stale ×1"],
      ["MsgHeartbeat", "依頼書"],
      ["MsgVote", "MsgVoteResp", "stale ×1"],
    ]);
    const pile2 = scene.piles.find((p) => p.islandId === 1 && p.to === 2);
    const pile3 = scene.piles.find((p) => p.islandId === 1 && p.to === 3);
    const f1 = scene.ferries.find((f) => f.id === "f1");
    expect(tags[0]?.at).toEqual({ x: pile2?.at.x, y: (pile2?.at.y ?? 0) + RAFT_TAG_PILE_OFFSET });
    expect(tags[1]?.at).toEqual({ x: pile3?.at.x, y: (pile3?.at.y ?? 0) + RAFT_TAG_PILE_OFFSET });
    expect(tags[2]?.at).toEqual({ x: f1?.at.x, y: (f1?.at.y ?? 0) + RAFT_TAG_FERRY_OFFSET });
  });

  it("RL-5 手紙の無い船・山には札を作らない", () => {
    const empty = world([], [dockedFerry("f1", 1), dockedFerry("f2", 2)]);
    expect(buildRaftTags(empty, buildMapScene(empty, NONE, 0, SIZE))).toEqual([]);

    const onlyPort = world([msg(1, "MsgApp")], [dockedFerry("f1", 1)]);
    const tags = buildRaftTags(onlyPort, buildMapScene(onlyPort, NONE, 0, SIZE));
    expect(tags.map((t) => t.key)).toEqual(["pile:1:2"]);
  });

  it("RL-6 入力を書き換えない（凍らせた入力で投げない）", () => {
    const w = world([msg(1, "MsgApp"), withRaft(ferryLetter(20, "f1", 2), "MsgApp")]);
    const scene = buildMapScene(w, NONE, 0, SIZE);
    deepFreeze(w);
    deepFreeze(scene);
    expect(() => buildRaftTags(w, scene)).not.toThrow();
    const letters = Object.freeze([msg(1, "MsgApp"), msg(2, "MsgHeartbeat")].map(deepFreeze));
    expect(() => countTypes(letters)).not.toThrow();
    const counts = Object.freeze([Object.freeze({ type: "MsgApp", count: 1 })]);
    expect(() => tagLines(counts, 1)).not.toThrow();
  });
});

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const v of Object.values(value)) deepFreeze(v);
  }
  return value;
}
