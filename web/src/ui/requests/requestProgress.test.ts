import { describe, expect, it } from "vitest";

import type { RequestStatus, ServerEvent } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import { makeRequest, makeSnapshot } from "../hud/testing/world.ts";
import {
  PROGRESS_STEPS,
  criticalRequestsInProgress,
  requestProgress,
  stepState,
} from "./requestProgress.ts";
import type { ProgressStepState } from "./requestProgress.ts";

function states(status: string): ProgressStepState[] {
  const progress = requestProgress(status);
  return PROGRESS_STEPS.map((_, i) => stepState(progress, i));
}

describe("requestProgress", () => {
  it("RP-1 段階は受付・提案・記録・確定・返事の 5 つ。Raft の用語は提案・記録・確定だけ", () => {
    expect(PROGRESS_STEPS.map((s) => s.game)).toEqual(["受付", "提案", "記録", "確定", "返事"]);
    expect(PROGRESS_STEPS.map((s) => s.raft)).toEqual([null, "Propose", "Append", "Commit", null]);
  });

  it.each<[RequestStatus, ProgressStepState[], string]>([
    ["received", ["done", "current", "todo", "todo", "todo"], "代表へ送る準備をしています"],
    [
      "waiting",
      ["done", "current", "todo", "todo", "todo"],
      "依頼書が港で、代表へ運ぶ船を待っています",
    ],
    ["in_transit", ["done", "current", "todo", "todo", "todo"], "依頼書を代表へ運んでいます"],
    [
      "proposed",
      ["done", "done", "current", "todo", "todo"],
      "島々の記録と、代表への返事を待っています",
    ],
    [
      "committed",
      ["done", "done", "done", "done", "current"],
      "依頼元の島に結果が届くのを待っています",
    ],
    ["succeeded", ["done", "done", "done", "done", "done"], "結果が届きました"],
    ["rejected", ["done", "done", "done", "done", "failed"], "予約できませんでした"],
  ])("RP-2 %s → %j「%s」", (status, expected, reason) => {
    expect(states(status)).toEqual(expected);
    expect(requestProgress(status).reason).toBe(reason);
  });

  it("RP-2 結果の区分", () => {
    expect(requestProgress("proposed").outcome).toBe("pending");
    expect(requestProgress("succeeded").outcome).toBe("succeeded");
    expect(requestProgress("rejected").outcome).toBe("rejected");
  });

  it("RP-3 知らない値は段階を決めず、値をそのまま理由に出す", () => {
    expect(states("future_status")).toEqual(["todo", "todo", "todo", "todo", "todo"]);
    expect(requestProgress("future_status")).toEqual({
      done: 0,
      current: null,
      outcome: "unknown",
      reason: "状態が分かりません（future_status）",
    });
    expect(requestProgress("toString").outcome).toBe("unknown");
  });

  it("RP-4 request.created・request.updated の列から段階が決まる（戻る再送・確定まで）", () => {
    let world = worldFromSnapshot(makeSnapshot(), 0);
    const progressOf = (): ProgressStepState[] => {
      const request = world.requests["r1"];
      if (request === undefined) throw new Error("依頼 r1 が無い");
      return PROGRESS_STEPS.map((_, i) => stepState(requestProgress(request.status), i));
    };
    const updated = (seq: number, status: RequestStatus): ServerEvent => ({
      seq,
      t: seq * 100,
      type: "request.updated",
      id: "r1",
      status,
      expired: false,
      result: null,
    });

    world = applyEvents(world, [
      { seq: 1, t: 100, type: "request.created", request: makeRequest("r1", 1) },
    ]);
    expect(progressOf()).toEqual(["done", "current", "todo", "todo", "todo"]);

    world = applyEvents(world, [updated(2, "in_transit"), updated(3, "proposed")]);
    expect(progressOf()).toEqual(["done", "done", "current", "todo", "todo"]);

    // 再送で依頼書を書き直すと、審議中 → 送付待ちに戻る（DesignDoc §8.2）。
    world = applyEvents(world, [updated(4, "waiting")]);
    expect(progressOf()).toEqual(["done", "current", "todo", "todo", "todo"]);

    world = applyEvents(world, [updated(5, "proposed"), updated(6, "committed")]);
    expect(progressOf()).toEqual(["done", "done", "done", "done", "current"]);

    world = applyEvents(world, [updated(7, "succeeded")]);
    expect(progressOf()).toEqual(["done", "done", "done", "done", "done"]);
  });

  it("RP-5 重要な依頼: 重要な資源ごと（ステージの順）に、終わっていない最後の 1 件", () => {
    const world = worldFromSnapshot(
      makeSnapshot({
        requests: [
          makeRequest("b-1", 1, { resource: "firework:b", critical: true, status: "waiting" }),
          makeRequest("a-1", 2, { resource: "firework:a", critical: true, status: "proposed" }),
          makeRequest("a-2", 3, { resource: "firework:a", critical: true, status: "rejected" }),
          makeRequest("b-2", 2, { resource: "firework:b", critical: true, status: "committed" }),
          makeRequest("stall", 1, { status: "waiting" }),
          makeRequest("x", 1, { resource: "nothing", critical: true, status: "waiting" }),
        ],
      }),
      0,
    );
    expect(criticalRequestsInProgress(world).map((r) => r.id)).toEqual(["a-1", "b-2"]);

    const done = worldFromSnapshot(
      makeSnapshot({
        requests: [
          makeRequest("a-1", 1, { resource: "firework:a", critical: true, status: "succeeded" }),
        ],
      }),
      0,
    );
    expect(criticalRequestsInProgress(done)).toEqual([]);
  });
});
