// T-714: GET /input-log（FR-LOG-03・DesignDoc §12.3）。DO の SQLite の入力ログを添付ファイルの JSON で返す。

import { describe, expect, it } from "vitest";

import { SESSION_ID, TOKEN, harness, msg, started, type Harness } from "./session/test-harness.ts";

const cmd = (type: string, cmdId: string, payload: unknown = {}) =>
  JSON.stringify({ v: 1, type, cmdId, payload });
const ack = (cmdId: string, t: number, seq: number) =>
  JSON.stringify({ v: 1, type: "ack", seq, payload: { cmdId, ok: true, t } });
const snapshot = (seq: number) =>
  JSON.stringify({
    v: 1,
    type: "snapshot",
    seq,
    payload: {
      clock: { t: 0 },
      session: { stageHash: "sha256:abc", build: "b1", restored: false },
    },
  });

const LOAD_PAYLOAD = { ferryId: "f1", letterIds: [3] };
const FILENAME = 'attachment; filename="raft-game-stage1-20261101.json"';

// snapshot の後にコマンドを 2 つ中継し、どちらも ack: ok を受ける。
async function withTwoCommands(): Promise<Harness> {
  const h = await started();
  const browser = h.lastBrowser();
  const socket = h.lastContainerSocket();
  socket.receive(snapshot(1));
  browser.receive(cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD));
  socket.receive(ack("c1", 1200, 2));
  browser.receive(cmd("cmd.game.pause", "c2", { paused: true }));
  socket.receive(ack("c2", 5400, 3));
  return h;
}

// ラウンドを終える（round.result → internal.result）。コンテナは止める。
async function endRound(h: Harness): Promise<void> {
  const socket = h.lastContainerSocket();
  socket.receive(msg("round.result", { seq: 4 }));
  h.clock.t = h.t0 + 200_000;
  socket.receive(msg("internal.result", { payload: { score: 1 } }));
  await h.drain();
  expect(h.row()).toMatchObject({ state: "ended", endReason: "round_ended" });
}

const EXPECTED_BODY =
  '{"format":1,"build":"b1","stageId":"stage1","stageHash":"sha256:abc","seed":20261101,' +
  '"commands":[{"t":1200,"cmd":{"type":"cmd.ferry.load","payload":{"ferryId":"f1","letterIds":[3]}}},' +
  '{"t":5400,"cmd":{"type":"cmd.game.pause","payload":{"paused":true}}}]}';

describe("GI-1 取り出し", () => {
  it("コマンドを 2 つ中継した後 → 200 で形式 1 の JSON の添付ファイル", async () => {
    const h = await withTwoCommands();
    const res = await h.inputLog();

    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("application/json; charset=utf-8");
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(res.headers.get("content-disposition")).toBe(FILENAME);
    const body = await res.text();
    expect(body).toBe(EXPECTED_BODY);
    expect((JSON.parse(body) as { commands: unknown[] }).commands).toHaveLength(2);
  });

  it("ログ session.input_log は rows・bytes だけで、トークン・本文を出さない", async () => {
    const h = await withTwoCommands();
    const before = h.logs.length;
    await h.inputLog();

    const added = h.logs.slice(before);
    expect(added).toHaveLength(1);
    expect(JSON.parse(added[0] ?? "")).toEqual({
      level: "info",
      event: "session.input_log",
      sessionId: SESSION_ID,
      rows: 2,
      bytes: expect.any(Number) as unknown,
    });
    expect(added[0]).not.toContain(TOKEN);
    expect(added[0]).not.toContain(h.tokenHash);
    expect(added[0]).not.toContain("cmd.ferry.load");
  });

  it('開始の前（snapshot なし・記録 0 件）→ build・stageHash が ""・commands が []', async () => {
    const h = await harness();
    expect((await h.init()).status).toBe(201);
    const res = await h.inputLog();

    expect(res.status).toBe(200);
    expect(await res.json()).toStrictEqual({
      format: 1,
      build: "",
      stageId: "stage1",
      stageHash: "",
      seed: 20261101,
      commands: [],
    });
  });
});

describe("GI-2 終わった後・拒否", () => {
  it("ラウンドの終了後（ended）でも 200 で同じ本文", async () => {
    const h = await withTwoCommands();
    await endRound(h);
    const res = await h.inputLog();

    expect(res.status).toBe(200);
    expect(res.headers.get("content-disposition")).toBe(FILENAME);
    expect(await res.text()).toBe(EXPECTED_BODY);
  });

  it.each<[string, string | null]>([
    ["違うトークン", "Tok_en-abcdefghijklmnX"],
    ["ヘッダーなし", null],
    ["形が違う", "short"],
  ])("%s → 404 not_found（区別しない）", async (_name, token) => {
    const h = await withTwoCommands();
    const res = await h.inputLog(token);

    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({ code: "not_found", message: "Not found" });
  });

  it("行がない DO → 404 not_found", async () => {
    const h = await harness();
    const res = await h.inputLog();

    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({ code: "not_found", message: "Not found" });
  });
});

describe("GI-3 コンテナ・アラーム・行に触れない", () => {
  it("遊んでいる途中・終わった後のどちらでも、コンテナの起動・アラームの設定・行の書き換えが起きない", async () => {
    const h = await withTwoCommands();
    for (const step of ["running", "ended"] as const) {
      if (step === "ended") await endRound(h);
      const calls = [...h.calls];
      const alarms = [...h.storage.alarms];
      const row = h.row();
      expect((await h.inputLog()).status).toBe(200);
      expect((await h.inputLog("Tok_en-abcdefghijklmnX")).status).toBe(404);
      await h.drain();
      expect(h.calls).toEqual(calls);
      expect(h.storage.alarms).toEqual(alarms);
      expect(h.row()).toEqual(row);
    }
  });

  it("行がない DO でもコンテナ・アラームに触れない", async () => {
    const h = await harness();
    await h.inputLog();
    await h.drain();
    expect(h.calls).toEqual([]);
  });
});

describe("GI-4 メソッド", () => {
  it("POST /input-log → 405 と Allow: GET", async () => {
    const h = await withTwoCommands();
    const res = await h.inputLog(TOKEN, "POST");

    expect(res.status).toBe(405);
    expect(res.headers.get("allow")).toBe("GET");
    expect(await res.json()).toMatchObject({ code: "method_not_allowed" });
  });
});
