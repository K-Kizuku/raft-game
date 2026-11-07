import { describe, expect, it } from "vitest";

import {
  MAX_CMD_ID_LENGTH,
  MAX_INPUT_LOG_BYTES,
  MAX_INPUT_LOG_ROWS,
  MAX_RESTORE_MESSAGE_BYTES,
  MAX_SESSION_TEXT_LENGTH,
} from "./limits.ts";
import { buildRestoreMessage, commandJson, recordDecision, restoreUntil } from "./restore.ts";
import type { SessionRow } from "./store.ts";

function rowOf(change: Partial<SessionRow> = {}): SessionRow {
  return {
    sessionId: "s_a.b",
    stageId: "stage1",
    seed: 20261101,
    demo: false,
    tokenHash: "a".repeat(64),
    state: "running",
    createdAt: 1000,
    deadlineAt: 1_801_000,
    maxAlarms: 20,
    alarmCount: 1,
    alarmAt: null,
    idleSince: null,
    containerStarts: 1,
    containerStartedAt: null,
    readyAt: null,
    runMs: 0,
    stageHash: "sha256:abc",
    build: "b1",
    lastClockT: null,
    endReason: null,
    endedAt: null,
    settled: false,
    resultJson: null,
    resultAt: null,
    ...change,
  };
}

const utf8 = (text: string) => new TextEncoder().encode(text).byteLength;

describe("R-1 buildRestoreMessage", () => {
  it("形がプロトコル §9 の internal.restore と完全に一致し、commands は渡した順", () => {
    const commands = [
      { t: 1200, cmd: commandJson("cmd.ferry.load", { ferryId: "f1", letterIds: [3] }) },
      { t: 5400, cmd: commandJson("cmd.game.pause", { paused: true }) },
    ];
    const text = buildRestoreMessage(rowOf(), commands, 61200);
    expect(JSON.parse(text)).toStrictEqual({
      v: 1,
      type: "internal.restore",
      payload: {
        sessionId: "s_a.b",
        stageId: "stage1",
        seed: 20261101,
        demo: false,
        inputLog: {
          format: 1,
          build: "b1",
          stageId: "stage1",
          stageHash: "sha256:abc",
          seed: 20261101,
          commands: [
            {
              t: 1200,
              cmd: { type: "cmd.ferry.load", payload: { ferryId: "f1", letterIds: [3] } },
            },
            { t: 5400, cmd: { type: "cmd.game.pause", payload: { paused: true } } },
          ],
        },
        until: 61200,
      },
    });
  });

  it('build が NULL なら ""、demo は真偽値、until が 0 ならキーがない、commands が空なら []', () => {
    const parsed = JSON.parse(buildRestoreMessage(rowOf({ build: null, demo: true }), [], 0)) as {
      payload: Record<string, unknown> & { inputLog: Record<string, unknown> };
    };
    expect(parsed.payload.demo).toBe(true);
    expect(parsed.payload.inputLog.build).toBe("");
    expect(parsed.payload.inputLog.commands).toEqual([]);
    expect("until" in parsed.payload).toBe(false);
  });
});

describe("R-2 最大の入力ログ", () => {
  it("10,000 行・cmd の合計 524288 バイト・t は 7 桁でも 1 MiB 以下", () => {
    const base = commandJson("cmd.ferry.load", { p: "" });
    const commands: { t: number; cmd: string }[] = [];
    let total = 0;
    for (let i = 0; i < MAX_INPUT_LOG_ROWS; i++) {
      const length = i < 4288 ? 53 : 52; // 4288 × 53 + 5712 × 52 = 524288
      const cmd = commandJson("cmd.ferry.load", { p: "x".repeat(length - base.length) });
      total += utf8(cmd);
      commands.push({ t: 9_000_000 + i, cmd });
    }
    expect(total).toBe(MAX_INPUT_LOG_BYTES);
    const row = rowOf({
      sessionId: "s".repeat(256),
      stageHash: "h".repeat(MAX_SESSION_TEXT_LENGTH),
      build: "b".repeat(MAX_SESSION_TEXT_LENGTH),
    });
    const text = buildRestoreMessage(row, commands, 9_999_999);
    expect(utf8(text)).toBeLessThanOrEqual(MAX_RESTORE_MESSAGE_BYTES);
    const parsed = JSON.parse(text) as { payload: { inputLog: { commands: unknown[] } } };
    expect(parsed.payload.inputLog.commands).toHaveLength(MAX_INPUT_LOG_ROWS);
  });
});

describe("R-3 restoreUntil", () => {
  it.each<[number | null, number | null, number]>([
    [61200, 5400, 61200],
    [null, 5400, 5400],
    [1000, 5400, 5400],
    [null, null, 0],
  ])("(%s, %s) → %s", (clockT, lastT, until) => {
    expect(restoreUntil(clockT, lastT)).toBe(until);
  });
});

describe("R-4 recordDecision", () => {
  it.each(["hello", "ping", "cmd.sync", "sub.engineer"])("%s（cmdId あり）→ forward", (type) => {
    expect(recordDecision(type, "c1")).toBe("forward");
  });

  it.each(["cmd.ferry.load", "cmd.game.pause"])(
    "%s（cmdId が 1 文字・128 文字）→ record",
    (type) => {
      expect(recordDecision(type, "c")).toBe("record");
      expect(recordDecision(type, "c".repeat(MAX_CMD_ID_LENGTH))).toBe("record");
    },
  );

  it("cmdId のキーなし → forward", () => {
    expect(recordDecision("cmd.ferry.load", undefined)).toBe("forward");
  });

  it.each<[string, unknown]>([
    ["空", ""],
    ["129 文字", "c".repeat(129)],
    ["数の 1", 1],
    ["null", null],
  ])("cmdId が %s → drop", (_name, cmdId) => {
    expect(recordDecision("cmd.ferry.load", cmdId)).toBe("drop");
  });
});

describe("R-5 commandJson", () => {
  it("キーの順が type・payload", () => {
    expect(commandJson("cmd.ferry.load", { ferryId: "f1", letterIds: [3] })).toBe(
      '{"type":"cmd.ferry.load","payload":{"ferryId":"f1","letterIds":[3]}}',
    );
    expect(commandJson("cmd.game.skip", {})).toBe('{"type":"cmd.game.skip","payload":{}}');
  });
});
