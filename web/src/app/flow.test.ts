import { describe, expect, it } from "vitest";

import {
  flowReducer,
  initialFlowState,
  nextStageOf,
  type FlowAction,
  type FlowState,
} from "./flow.ts";
import type { SessionHandle, StageSummary } from "./types.ts";

const stage1: StageSummary = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
  demo: false,
};
const stage2: StageSummary = { ...stage1, id: "stage2", title: "島から返事が来ない！" };
const stage3: StageSummary = { ...stage1, id: "stage3", title: "嵐の夜" };
const session: SessionHandle = { sessionId: "s-1", token: "t-1", wsPath: "/ws/s-1" };

function deepFreeze<T>(value: T): T {
  if (typeof value === "object" && value !== null) {
    for (const child of Object.values(value)) {
      deepFreeze(child);
    }
    Object.freeze(value);
  }
  return value;
}

describe("flowReducer", () => {
  it("F-1 同じステージで出航すると、選んだセッションとシードをラウンドに移す", () => {
    const selected = flowReducer(initialFlowState, {
      type: "stageSelected",
      selection: { stageId: "stage1", session, seed: 7 },
    });
    expect(selected).toEqual({ selection: { stageId: "stage1", session, seed: 7 }, round: null });

    const departed = flowReducer(selected, { type: "departed", stage: stage1 });
    expect(departed).toEqual({
      selection: null,
      round: { stage: stage1, session, seed: 7, finished: false },
    });
  });

  it("F-1 サーバーなしで選んだときも、シードをラウンドに移す", () => {
    const state: FlowState = {
      selection: { stageId: "stage1", session: null, seed: 9 },
      round: null,
    };
    const departed = flowReducer(state, { type: "departed", stage: stage1 });
    expect(departed.round).toEqual({ stage: stage1, session: null, seed: 9, finished: false });
  });

  it.each<[string, FlowState]>([
    ["別のステージ", { selection: { stageId: "stage2", session, seed: 7 }, round: null }],
    ["null", { selection: null, round: null }],
  ])("F-1 selection が%sなら session と seed は null", (_label, state) => {
    const departed = flowReducer(state, { type: "departed", stage: stage1 });
    expect(departed.round).toEqual({ stage: stage1, session: null, seed: null, finished: false });
    expect(departed.selection).toBeNull();
  });

  it("F-2 roundEnded と retried", () => {
    const playing: FlowState = {
      selection: null,
      round: { stage: stage1, session, seed: 7, finished: false },
    };
    const ended = flowReducer(playing, { type: "roundEnded" });
    expect(ended.round).toEqual({ stage: stage1, session, seed: 7, finished: true });

    const next: SessionHandle = { sessionId: "s-2", token: "t-2", wsPath: "/ws/s-2" };
    const retried = flowReducer(ended, { type: "retried", session: next, seed: 7 });
    expect(retried.round).toEqual({ stage: stage1, session: next, seed: 7, finished: false });
    expect(retried.round?.stage).toBe(stage1);

    const offline = flowReducer(ended, { type: "retried", session: null, seed: 8 });
    expect(offline.round).toEqual({ stage: stage1, session: null, seed: 8, finished: false });

    const empty: FlowState = {
      selection: { stageId: "stage1", session: null, seed: null },
      round: null,
    };
    expect(flowReducer(empty, { type: "roundEnded" })).toBe(empty);
    expect(flowReducer(empty, { type: "retried", session, seed: 1 })).toBe(empty);
  });

  it("FL-4 stageSelected で round が null になる", () => {
    const state: FlowState = {
      selection: null,
      round: { stage: stage1, session: null, seed: null, finished: true },
    };
    const next = flowReducer(state, {
      type: "stageSelected",
      selection: { stageId: "stage2", session: null, seed: null },
    });
    expect(next).toEqual({
      selection: { stageId: "stage2", session: null, seed: null },
      round: null,
    });
  });

  const actions: FlowAction[] = [
    { type: "stageSelected", selection: { stageId: "stage2", session: null, seed: null } },
    { type: "departed", stage: stage1 },
    { type: "roundEnded" },
    { type: "retried", session, seed: 3 },
  ];
  const states: FlowState[] = [
    initialFlowState,
    { selection: { stageId: "stage1", session, seed: 7 }, round: null },
    { selection: null, round: { stage: stage1, session, seed: 7, finished: false } },
    { selection: null, round: { stage: stage1, session, seed: 7, finished: true } },
  ];
  it.each(actions)("FL-4 %j は引数の state を書き換えない", (action) => {
    for (const state of states) {
      const snapshot = structuredClone(state);
      const frozen = deepFreeze(structuredClone(state));
      expect(() => flowReducer(frozen, action)).not.toThrow();
      expect(frozen).toEqual(snapshot);
    }
  });
});

describe("nextStageOf", () => {
  // FL-5
  const stages = [stage1, stage2, stage3];
  it.each<[string, readonly StageSummary[], string, StageSummary | null]>([
    ["途中の要素", stages, "stage1", stage2],
    ["途中の要素（2 番目）", stages, "stage2", stage3],
    ["最後", stages, "stage3", null],
    ["無い ID", stages, "zzz", null],
    ["空の配列", [], "stage1", null],
  ])("%s", (_label, list, stageId, want) => {
    expect(nextStageOf(list, stageId)).toBe(want);
  });
});
