// @vitest-environment jsdom
// モックの接続は既定の window.location を使うので jsdom で動かす。
import { describe, expect, it } from "vitest";

import {
  createManualTimers,
  type ConnectionHandlers,
  type ConnectionStatus,
} from "../../net/index.ts";
import type { Phase, ServerEvent } from "../../protocol/index.ts";
import { STAGE_ID_PATTERN } from "../router/routes.ts";
import { createMockRound, mockStageSummary } from "./mockRound.ts";

function recorder() {
  const log = {
    snapshots: [] as { title: string; phase: Phase }[],
    events: [] as ServerEvent[],
    results: 0,
    statuses: [] as ConnectionStatus[],
    calls: 0,
  };
  const handlers: ConnectionHandlers = {
    onSnapshot(snapshot) {
      log.calls++;
      log.snapshots.push({ title: snapshot.stage.title, phase: snapshot.phase });
    },
    onEvents(_clock, events) {
      log.calls++;
      log.events.push(...events);
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

describe("createMockRound", () => {
  it("MR-1 記録を最初から最後まで再生する", async () => {
    const timers = createManualTimers();
    const { log, handlers } = recorder();
    const link = createMockRound(handlers, { timers });
    expect(link.source).toBe("mock");

    link.connection.start();
    timers.advance(0);
    expect(log.snapshots).toEqual([{ title: "テストの海", phase: "intro" }]);

    const started = link.connection.sendCommand("cmd.game.start", {});
    timers.advance(0);
    await expect(started).resolves.toMatchObject({ ok: true });

    timers.advance(60100);
    expect(log.events).toHaveLength(30);
    expect(log.results).toBe(1);
    link.dispose();
  });

  it("MR-2 途中で dispose するとタイマーが残らず、ハンドラーも呼ばれない", () => {
    const timers = createManualTimers();
    const { log, handlers } = recorder();
    const link = createMockRound(handlers, { timers });
    link.connection.start();
    timers.advance(0);
    void link.connection.sendCommand("cmd.game.start", {}).catch(() => {});
    timers.advance(3000);
    expect(log.events.length).toBeGreaterThan(0);

    link.dispose();
    expect(timers.pendingCount()).toBe(0);
    const calls = log.calls;
    const statuses = log.statuses.length;
    timers.advance(10000);
    expect(log.calls).toBe(calls);

    link.dispose();
    expect(timers.pendingCount()).toBe(0);
    expect(log.statuses).toHaveLength(statuses);
  });
});

describe("mockStageSummary", () => {
  it("MR-3 記録とステージの表示用の値から作る", () => {
    const stage = mockStageSummary();
    expect(stage).toEqual({
      id: "mini",
      title: "テストの海（開発用の記録）",
      region: "開発用",
      islands: 3,
      ferries: 1,
      durationMs: 60000,
      intro: {
        text: "開発用の記録を再生するステージです（スコアなどは仮の値です）。",
        provenance: "creative",
        source: null,
      },
      learning: [],
    });
    expect(STAGE_ID_PATTERN.test(stage.id)).toBe(true);
  });
});
