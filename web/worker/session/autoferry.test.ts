// T-724: 自動航行（cmd.ferry.auto）とスイッチ AUTO_FERRY、ボットの操作（internal.input）の記録
// （DesignDoc §12.3「入力ログと復元」・プロトコル §9）。

import { describe, expect, it } from "vitest";

import { MAX_INPUT_LOG_ROWS } from "./limits.ts";
import { appendInputLog, readInputLog } from "./store.ts";
import { SESSION_ID, parsed, reconnect, started, type Harness } from "./test-harness.ts";

const ENABLED = { env: { AUTO_FERRY: "1" } };

const cmd = (type: string, cmdId: string, payload: unknown = {}) =>
  JSON.stringify({ v: 1, type, cmdId, payload });
const ack = (cmdId: string, t: number, seq: number, ok = true) =>
  JSON.stringify({ v: 1, type: "ack", seq, payload: { cmdId, ok, t } });
// internal.input は seq を持たない（Go はブラウザに届く電文だけに seq を振る。プロトコル §3）。
const input = (t: number, type: string, payload: unknown) =>
  JSON.stringify({ v: 1, type: "internal.input", payload: { t, cmd: { type, payload } } });
const snapshot = (seq: number) =>
  JSON.stringify({
    v: 1,
    type: "snapshot",
    seq,
    payload: { clock: { t: 0 }, session: { stageHash: "sha256:abc", build: "b1" } },
  });

const DEPART_F2 = { ferryId: "f2", to: 3 };
const inputLog = (h: Harness) => readInputLog(h.storage.sql);
const warnings = (h: Harness) => h.events().filter((e) => e["level"] === "warn");

describe("AF-1 internal.init・internal.restore の autoFerry", () => {
  it('AUTO_FERRY が "1" なら internal.init に autoFerry: true を入れる', async () => {
    const h = await started(ENABLED);
    expect(parsed(h.lastContainerSocket().sent[0])).toStrictEqual({
      v: 1,
      type: "internal.init",
      payload: {
        sessionId: SESSION_ID,
        stageId: "stage1",
        seed: 20261101,
        demo: false,
        autoFerry: true,
      },
    });
  });

  it.each<[string, Record<string, string>]>([
    ["0", { AUTO_FERRY: "0" }],
    ["true", { AUTO_FERRY: "true" }],
    ["未設定", {}],
  ])(
    "AUTO_FERRY が %s なら autoFerry のキーを入れない（コンテナは feature_disabled で断る）",
    async (_n, env) => {
      const h = await started({ env });
      const first = parsed(h.lastContainerSocket().sent[0]) as { payload: Record<string, unknown> };
      expect(Object.hasOwn(first.payload, "autoFerry")).toBe(false);
    },
  );

  it("止まった後の internal.restore にも autoFerry: true を入れ、ボットの操作を含めて渡す", async () => {
    const h = await started(ENABLED);
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(1));
    socket.receive(input(3000, "cmd.ferry.depart", DEPART_F2));
    h.container.crash();
    await h.drain();
    const { socket: socket2 } = await reconnect(h);
    const restore = parsed(socket2.sent[0]) as {
      type: string;
      payload: { autoFerry?: boolean; inputLog: { commands: unknown[] } };
    };
    expect(restore.type).toBe("internal.restore");
    expect(restore.payload.autoFerry).toBe(true);
    expect(restore.payload.inputLog.commands).toEqual([
      { t: 3000, cmd: { type: "cmd.ferry.depart", payload: DEPART_F2 } },
    ]);
  });
});

describe("AF-2 cmd.ferry.auto", () => {
  it("中継するが、ok: true の ack でも入力ログに記録しない", async () => {
    const h = await started(ENABLED);
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    const auto = cmd("cmd.ferry.auto", "a1", { ferryId: "f2", enabled: true });
    browser.receive(auto);
    expect(socket.sent.at(-1)).toBe(auto);
    const ok = ack("a1", 0, 1);
    socket.receive(ok);
    expect(browser.sent).toEqual([ok]);
    expect(inputLog(h)).toEqual([]);
  });

  it("AUTO_FERRY が無効でも中継する（feature_disabled の ack はコンテナが返し、そのまま届く）", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    const auto = cmd("cmd.ferry.auto", "a1", { ferryId: "f2", enabled: true });
    browser.receive(auto);
    expect(socket.sent.at(-1)).toBe(auto);
    const disabled = JSON.stringify({
      v: 1,
      type: "ack",
      seq: 1,
      payload: { cmdId: "a1", ok: false, t: 0, code: "feature_disabled", message: "m" },
    });
    socket.receive(disabled);
    expect(browser.sent).toEqual([disabled]);
    expect(inputLog(h)).toEqual([]);
  });
});

describe("AF-3 internal.input（ボットの操作）", () => {
  it("届いた順に (t, {type, payload}) を記録し、ブラウザには送らない。プレイヤーの ack と順が混ざらない", async () => {
    const h = await started(ENABLED);
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    browser.receive(cmd("cmd.game.start", "c1"));
    socket.receive(ack("c1", 0, 1));
    socket.receive(input(3000, "cmd.ferry.autoload", DEPART_F2));
    socket.receive(input(3000, "cmd.ferry.depart", DEPART_F2));
    browser.receive(cmd("cmd.ferry.depart", "c2", { ferryId: "f1", to: 2 }));
    socket.receive(ack("c2", 3500, 2));
    expect(inputLog(h)).toEqual([
      { t: 0, cmd: '{"type":"cmd.game.start","payload":{}}' },
      { t: 3000, cmd: '{"type":"cmd.ferry.autoload","payload":{"ferryId":"f2","to":3}}' },
      { t: 3000, cmd: '{"type":"cmd.ferry.depart","payload":{"ferryId":"f2","to":3}}' },
      { t: 3500, cmd: '{"type":"cmd.ferry.depart","payload":{"ferryId":"f1","to":2}}' },
    ]);
    expect(browser.sentTypes).toEqual(["ack", "ack"]);
    // ブラウザに届く seq は抜けなくつながる（ボットの操作の間も cmd.sync を招かない）
    expect(browser.sent.map((m) => (parsed(m) as { seq: number }).seq)).toEqual([1, 2]);

    // GET /input-log（ダウンロード）にも入る
    const res = await h.inputLog();
    const body = (await res.json()) as { commands: { t: number; cmd: { type: string } }[] };
    expect(body.commands.map((c) => [c.t, c.cmd.type])).toEqual([
      [0, "cmd.game.start"],
      [3000, "cmd.ferry.autoload"],
      [3000, "cmd.ferry.depart"],
      [3500, "cmd.ferry.depart"],
    ]);
  });

  it.each<[string, unknown]>([
    ["payload がオブジェクトでない", []],
    ["t がない", { cmd: { type: "cmd.ferry.depart", payload: DEPART_F2 } }],
    ["t が負", { t: -1, cmd: { type: "cmd.ferry.depart", payload: DEPART_F2 } }],
    ["t が小数", { t: 1.5, cmd: { type: "cmd.ferry.depart", payload: DEPART_F2 } }],
    ["cmd がない", { t: 1 }],
    ["type が文字列でない", { t: 1, cmd: { type: 1, payload: DEPART_F2 } }],
    ["payload がない", { t: 1, cmd: { type: "cmd.ferry.depart" } }],
    [
      "ボットが出さない種類（cmd.game.start）",
      { t: 1, cmd: { type: "cmd.game.start", payload: {} } },
    ],
    [
      "ボットが出さない種類（cmd.ferry.auto）",
      { t: 1, cmd: { type: "cmd.ferry.auto", payload: {} } },
    ],
  ])("%s → 記録せず warn", async (_name, payload) => {
    const h = await started(ENABLED);
    const browser = h.lastBrowser();
    h.lastContainerSocket().receive(JSON.stringify({ v: 1, type: "internal.input", payload }));
    expect(inputLog(h)).toEqual([]);
    expect(browser.sent).toEqual([]);
    expect(warnings(h)).toEqual([
      expect.objectContaining({ event: "session.error", op: "input_log_bot", error: "malformed" }),
    ]);
  });

  it("最後に記録した t より小さければ記録しない", async () => {
    const h = await started(ENABLED);
    const socket = h.lastContainerSocket();
    socket.receive(input(5000, "cmd.ferry.depart", DEPART_F2));
    socket.receive(input(4000, "cmd.ferry.depart", DEPART_F2));
    expect(inputLog(h).map((e) => e.t)).toEqual([5000]);
    expect(warnings(h)).toEqual([
      expect.objectContaining({ event: "session.error", op: "input_log_order" }),
    ]);
  });

  it("行数の上限に達していれば記録せず、input_log.full を 1 回だけ出す", async () => {
    const h = await started(ENABLED);
    for (let i = 0; i < MAX_INPUT_LOG_ROWS; i++) appendInputLog(h.storage.sql, 0, "{}");
    const socket = h.lastContainerSocket();
    socket.receive(input(1, "cmd.ferry.depart", DEPART_F2));
    socket.receive(input(2, "cmd.ferry.depart", DEPART_F2));
    expect(inputLog(h)).toHaveLength(MAX_INPUT_LOG_ROWS);
    expect(h.events().filter((e) => e.event === "input_log.full")).toHaveLength(1);
  });

  it("置き換えた接続のソケットから届いても、いまのコンテナのものなら記録する", async () => {
    const h = await started(ENABLED);
    const socket1 = h.lastContainerSocket();
    const { browser: browser2 } = await reconnect(h);
    socket1.receive(input(3000, "cmd.ferry.depart", DEPART_F2));
    expect(inputLog(h)).toEqual([
      { t: 3000, cmd: '{"type":"cmd.ferry.depart","payload":{"ferryId":"f2","to":3}}' },
    ]);
    expect(browser2.sent).toEqual([]);
  });

  it("止まったと判断した古いコンテナのソケットから届いたものは記録しない", async () => {
    const h = await started(ENABLED);
    const socket1 = h.lastContainerSocket();
    socket1.receive(snapshot(1));
    h.container.crash();
    await h.drain();
    socket1.receive(input(3000, "cmd.ferry.depart", DEPART_F2));
    expect(inputLog(h)).toEqual([]);
  });
});
