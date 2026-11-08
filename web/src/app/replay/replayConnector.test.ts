// @vitest-environment jsdom
// 接続は既定の window.location を使うので jsdom で動かす。
import { describe, expect, it } from "vitest";

import {
  createManualTimers,
  type ConnectionHandlers,
  type ConnectionStatus,
} from "../../net/index.ts";
import type { Snapshot } from "../../protocol/index.ts";
import { parseReplayResponse, type ReplaySuccess } from "../../replay/response.ts";
import { successResponse, TEST_SCORE } from "../../replay/testing/responses.ts";
import { createReplayConnector } from "./replayConnector.ts";

function outcome(): ReplaySuccess {
  const parsed = parseReplayResponse(JSON.stringify(successResponse()));
  if (!parsed.ok) {
    throw new Error("fixture");
  }
  return parsed;
}

function recorder() {
  const log = {
    snapshots: [] as Snapshot[],
    events: 0,
    results: 0,
    calls: 0,
    statuses: [] as ConnectionStatus[],
  };
  const handlers: ConnectionHandlers = {
    onSnapshot(snapshot) {
      log.calls++;
      log.snapshots.push(snapshot);
    },
    onEvents(_clock, events) {
      log.calls++;
      log.events += events.length;
    },
    onRoundResult() {
      log.calls++;
      log.results++;
    },
    onStatus(status) {
      log.statuses.push(status);
    },
  };
  return { log, handlers };
}

describe("createReplayConnector", () => {
  it("modeOf は常に replay", () => {
    const connector = createReplayConnector(outcome());
    expect(connector.modeOf(null)).toBe("replay");
  });

  it("RS-9 記録を再生し、船の操作は ack の失敗、dispose の後にタイマーが残らない", async () => {
    const timers = createManualTimers();
    const replay = outcome();
    const { log, handlers } = recorder();
    const link = await createReplayConnector(replay, { timers }).open(null, handlers);
    expect(link.source).toBe("replay");

    link.connection.start();
    timers.advance(0);
    expect(log.snapshots).toHaveLength(1);
    const snapshot = log.snapshots[0];
    expect(snapshot?.session.id).toBe("replay_mini_1");
    expect(snapshot?.stage.title).toBe("テストの海");
    expect(snapshot?.score).toEqual(TEST_SCORE);

    const started = link.connection.sendCommand("cmd.game.start", {});
    timers.advance(0);
    await expect(started).resolves.toMatchObject({ ok: true });

    const depart = link.connection.sendCommand("cmd.ferry.depart", { ferryId: "f1", to: 2 });
    timers.advance(0);
    await expect(depart).resolves.toMatchObject({ ok: false, code: "phase_invalid" });

    timers.advance(3000);
    expect(log.events).toBeGreaterThan(0);

    link.dispose();
    expect(timers.pendingCount()).toBe(0);
    const calls = log.calls;
    timers.advance(100000);
    expect(log.calls).toBe(calls);
    expect(log.results).toBe(0);
    link.dispose();
    expect(timers.pendingCount()).toBe(0);
  });

  it("最後まで再生すると outcome の result を送る", async () => {
    const timers = createManualTimers();
    const replay = outcome();
    let result: unknown = null;
    const { handlers } = recorder();
    const link = await createReplayConnector(replay, { timers }).open(null, {
      ...handlers,
      onRoundResult(r) {
        result = r;
      },
    });
    link.connection.start();
    timers.advance(0);
    void link.connection.sendCommand("cmd.game.start", {});
    timers.advance(60100);
    expect(result).toEqual(replay.result);
    link.dispose();
  });
});
