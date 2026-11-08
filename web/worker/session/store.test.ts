import { describe, expect, it } from "vitest";

import { createTestState } from "../admin/test-sqlite.ts";
import {
  appendInputLog,
  countInputLog,
  ensureSessionSchema,
  inputLogStats,
  insertSession,
  readInputLog,
  readSession,
  updateSession,
} from "./store.ts";

const INIT = {
  sessionId: "s_a.b",
  stageId: "stage1",
  seed: 20261101,
  demo: true,
  tokenHash: "a".repeat(64),
  createdAt: 1000,
  deadlineAt: 1000 + 1_800_000,
  maxAlarms: 20,
  idleSince: 1000,
};

describe("S-1 SQLite", () => {
  it("表を 2 回作っても誤りにならず、行を作って読み、更新できる", async () => {
    const { storage } = await createTestState();
    ensureSessionSchema(storage.sql);
    ensureSessionSchema(storage.sql);
    expect(readSession(storage.sql)).toBeNull();

    insertSession(storage.sql, INIT);
    expect(readSession(storage.sql)).toEqual({
      sessionId: "s_a.b",
      stageId: "stage1",
      seed: 20261101,
      demo: true,
      tokenHash: "a".repeat(64),
      state: "starting",
      createdAt: 1000,
      deadlineAt: 1_801_000,
      maxAlarms: 20,
      alarmCount: 0,
      alarmAt: null,
      idleSince: 1000,
      containerStarts: 0,
      containerStartedAt: null,
      readyAt: null,
      runMs: 0,
      stageHash: null,
      build: null,
      lastClockT: null,
      endReason: null,
      endedAt: null,
      settled: false,
      resultJson: null,
      resultAt: null,
    });

    updateSession(storage.sql, { state: "running", alarmCount: 2, idleSince: null, settled: true });
    expect(readSession(storage.sql)).toMatchObject({
      state: "running",
      alarmCount: 2,
      idleSince: null,
      settled: true,
    });
  });

  it("result_json は上書きでき、input_log は空", async () => {
    const { storage } = await createTestState();
    ensureSessionSchema(storage.sql);
    insertSession(storage.sql, INIT);
    updateSession(storage.sql, { resultJson: '{"score":1}', resultAt: 2000 });
    updateSession(storage.sql, { resultJson: '{"score":2}', resultAt: 3000 });
    expect(readSession(storage.sql)).toMatchObject({ resultJson: '{"score":2}', resultAt: 3000 });
    expect(countInputLog(storage.sql)).toBe(0);
  });

  it("2 行目は作れない", async () => {
    const { storage } = await createTestState();
    ensureSessionSchema(storage.sql);
    insertSession(storage.sql, INIT);
    expect(() => insertSession(storage.sql, INIT)).toThrow();
  });
});

describe("S-2 入力ログ", () => {
  it("空なら { rows: 0, bytes: 0, lastT: null } で、読むと []", async () => {
    const { storage } = await createTestState();
    ensureSessionSchema(storage.sql);
    expect(inputLogStats(storage.sql)).toEqual({ rows: 0, bytes: 0, lastT: null });
    expect(readInputLog(storage.sql)).toEqual([]);
  });

  it("appendInputLog・readInputLog が seq の順。bytes は UTF-8 のバイト数、lastT は最後の行の t", async () => {
    const { storage } = await createTestState();
    ensureSessionSchema(storage.sql);
    const a = '{"type":"cmd.a","payload":{}}';
    const b = '{"type":"cmd.b","payload":{"s":"あ"}}';
    appendInputLog(storage.sql, 1200, a);
    appendInputLog(storage.sql, 5400, b);
    appendInputLog(storage.sql, 3000, a); // t の順ではなく、足した順
    expect(readInputLog(storage.sql)).toEqual([
      { t: 1200, cmd: a },
      { t: 5400, cmd: b },
      { t: 3000, cmd: a },
    ]);
    const bytes = new TextEncoder().encode(a + b + a).byteLength;
    expect(bytes).toBe(a.length * 2 + b.length + 2); // 「あ」は 3 バイト
    expect(inputLogStats(storage.sql)).toEqual({ rows: 3, bytes, lastT: 3000 });
    expect(countInputLog(storage.sql)).toBe(3);
  });
});
