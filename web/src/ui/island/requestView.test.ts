import { describe, expect, it } from "vitest";

import type { RequestStatus } from "../../protocol/index.ts";
import { worldFromSnapshot } from "../../state/index.ts";
import { makeRequest, makeSnapshot } from "../hud/testing/world.ts";
import { islandRequests, requestStatusView } from "./requestView.ts";

describe("requestView", () => {
  it.each<[RequestStatus, string, string]>([
    ["received", "📝", "受付"],
    ["waiting", "📮", "送付待ち"],
    ["in_transit", "⛴", "輸送中"],
    ["proposed", "🗳", "審議中"],
    ["committed", "📜", "共和国で確定"],
    ["succeeded", "✅", "確定"],
    ["rejected", "❌", "予約失敗"],
  ])("RV-1 %s は %s %s", (status, icon, text) => {
    expect(requestStatusView(status)).toEqual({ icon, text });
  });

  it("RV-1 知らない値は「？」と値", () => {
    expect(requestStatusView("future_status")).toEqual({ icon: "？", text: "future_status" });
    expect(requestStatusView("toString")).toEqual({ icon: "？", text: "toString" });
  });

  it("RV-2 進行中をすべて、続けて終わった最後の 3 件。ほかの島の依頼は入らない", () => {
    const world = worldFromSnapshot(
      makeSnapshot({
        requests: [
          makeRequest("done-1", 1, { status: "succeeded" }),
          makeRequest("active-1", 1, { status: "proposed" }),
          makeRequest("done-2", 1, { status: "rejected" }),
          makeRequest("other", 2, { status: "waiting" }),
          makeRequest("done-3", 1, { status: "succeeded", expired: true }),
          makeRequest("active-2", 1, { status: "received", expired: true }),
          makeRequest("done-4", 1, { status: "succeeded" }),
        ],
      }),
      0,
    );
    expect(islandRequests(world, 1).map((r) => r.id)).toEqual([
      "active-1",
      "active-2",
      "done-2",
      "done-3",
      "done-4",
    ]);
    expect(islandRequests(world, 2).map((r) => r.id)).toEqual(["other"]);
    expect(islandRequests(world, 3)).toEqual([]);
    expect(islandRequests(world, 1)).not.toBe(islandRequests(world, 1));
  });
});
