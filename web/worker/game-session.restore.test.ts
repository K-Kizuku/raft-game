// T-308b: 入力ログの記録と、コンテナが止まったときの復元（DesignDoc §12.3「入力ログと復元」）。

import { describe, expect, it } from "vitest";

import { MAX_INPUT_LOG_BYTES, MAX_INPUT_LOG_ROWS } from "./session/limits.ts";
import { commandJson } from "./session/restore.ts";
import { appendInputLog, readInputLog } from "./session/store.ts";
import type { FakeSocket } from "./session/test-fakes.ts";
import {
  MIN,
  SESSION_ID,
  TOKEN,
  harness,
  msg,
  parsed,
  reconnect,
  started,
  type Harness,
} from "./session/test-harness.ts";

const cmd = (type: string, cmdId: unknown, payload: unknown = {}) =>
  JSON.stringify({ v: 1, type, cmdId, payload });
const ack = (cmdId: string, t: number, ok = true, seq = 1) =>
  JSON.stringify({ v: 1, type: "ack", seq, payload: { cmdId, ok, t } });
const snapshot = (session: Record<string, unknown> | null, t = 0, seq = 1) =>
  JSON.stringify({
    v: 1,
    type: "snapshot",
    seq,
    payload: { clock: { t }, ...(session === null ? {} : { session }) },
  });
const events = (t: number, seq: number) =>
  JSON.stringify({ v: 1, type: "events", seq, payload: { clock: { t }, events: [] } });
const fatal = (code: string, seq = 1) =>
  JSON.stringify({ v: 1, type: "error", seq, payload: { code, message: "m", fatal: true } });

const SESSION = { stageHash: "sha256:abc", build: "b1", restored: false };
const LOAD_PAYLOAD = { ferryId: "f1", letterIds: [3] };

const inputLog = (h: Harness) => readInputLog(h.storage.sql);
const sentCount = (socket: FakeSocket, text: string) =>
  socket.sent.filter((sent) => sent === text).length;
const pings = (socket: FakeSocket) => socket.sentTypes.filter((t) => t === "internal.ping").length;

// 止まってから起動し直すまで（R-6 の流れ）。
async function crashAndRestore() {
  const h = await started();
  const browser1 = h.lastBrowser();
  const socket1 = h.lastContainerSocket();
  socket1.receive(snapshot(SESSION, 0, 1));
  browser1.receive(cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD));
  socket1.receive(ack("c1", 1200, true, 2));
  browser1.receive(cmd("cmd.game.pause", "c2", { paused: true }));
  socket1.receive(ack("c2", 5400, true, 3));
  h.clock.t = h.t0 + 61_000;
  socket1.receive(events(61200, 4));
  h.clock.t = h.t0 + 70_000;
  h.container.crash();
  await h.drain();
  return { h, browser1, socket1 };
}

describe("L-1 記録", () => {
  it("ack が ok: true なら (t, {type, payload}) を記録し、ack は同じ文字列でブラウザに届く", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    const load = JSON.stringify({
      v: 1,
      type: "cmd.ferry.load",
      cmdId: "c1",
      payload: { ferryId: "f1", letterIds: [3] },
    });
    browser.receive(load);
    expect(socket.sent.at(-1)).toBe(load);
    const ok = ack("c1", 1200);
    socket.receive(ok);
    expect(browser.sent).toEqual([ok]);
    expect(inputLog(h)).toEqual([
      { t: 1200, cmd: '{"type":"cmd.ferry.load","payload":{"ferryId":"f1","letterIds":[3]}}' },
    ]);
  });

  it("ok: false・待っていない cmdId・cmdId 付きの hello・cmd.sync の ack では増えない", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    browser.receive(cmd("cmd.ferry.load", "c2", LOAD_PAYLOAD));
    socket.receive(ack("c2", 1300, false, 1));
    socket.receive(ack("c2", 1300, true, 2)); // 待ちから外れた後
    socket.receive(ack("zz", 1400, true, 3));
    browser.receive(cmd("hello", "h1"));
    socket.receive(ack("h1", 1500, true, 4));
    browser.receive(cmd("cmd.sync", "s1"));
    socket.receive(ack("s1", 1600, true, 5));
    expect(inputLog(h)).toEqual([]);
    expect(browser.sent).toHaveLength(5); // 中継はいままでどおり
  });
});

describe("L-2 重複と cmdId", () => {
  it("ack の前の同じ cmdId は 1 通だけ。ack の後ならまた送れる。129 文字の cmdId は届かない", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    const c1 = cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD);
    browser.receive(c1);
    browser.receive(c1);
    expect(sentCount(socket, c1)).toBe(1);
    socket.receive(ack("c1", 1200));
    browser.receive(c1);
    expect(sentCount(socket, c1)).toBe(2);

    const long = cmd("cmd.ferry.load", "x".repeat(129), LOAD_PAYLOAD);
    browser.receive(long);
    expect(sentCount(socket, long)).toBe(0);
    const empty = cmd("cmd.ferry.load", "", LOAD_PAYLOAD);
    browser.receive(empty);
    expect(sentCount(socket, empty)).toBe(0);

    browser.emit("close", { code: 1000, reason: "" });
    const disconnect = h.events().find((e) => e.event === "session.disconnect");
    expect(disconnect?.["dropped"]).toMatchObject({
      duplicate: 1,
      malformed: 2,
      pending_full: 0,
      log_full: 0,
    });
  });

  it("待ちが 256 件なら捨て（pending_full）、30 秒を過ぎた待ちは捨てて次を覚える", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    const sentBefore = socket.sent.length;
    for (let i = 0; i < 257; i++) {
      if (i % 30 === 0) h.clock.advance(1000); // レート制限の枠を変える
      h.lastBrowser().receive(cmd("cmd.ferry.load", `p${i}`, LOAD_PAYLOAD));
    }
    expect(socket.sent.length - sentBefore).toBe(256);
    h.clock.advance(30_000);
    h.lastBrowser().receive(cmd("cmd.ferry.load", "late", LOAD_PAYLOAD));
    expect(socket.sent.at(-1)).toBe(cmd("cmd.ferry.load", "late", LOAD_PAYLOAD));
  });
});

describe("L-3 置き換えた接続の ack", () => {
  it("1 本目で送ったコマンドの ack が 2 本目・1 本目のどちらのソケットに届いても記録する", async () => {
    const h = await started();
    const browser1 = h.lastBrowser();
    const socket1 = h.lastContainerSocket();
    browser1.receive(cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD));
    browser1.receive(cmd("cmd.ferry.unload", "c2", { ferryId: "f1" }));
    const { browser: browser2, socket: socket2 } = await reconnect(h);

    const ack1 = ack("c1", 1200, true, 1);
    socket2.receive(ack1);
    socket1.receive(ack("c2", 1300, true, 9));
    expect(browser2.sent).toEqual([ack1]); // 1 本目のソケットの ack は 2 本目のブラウザに届かない
    expect(browser1.sent).toEqual([]);
    expect(inputLog(h)).toEqual([
      { t: 1200, cmd: commandJson("cmd.ferry.load", LOAD_PAYLOAD) },
      { t: 1300, cmd: commandJson("cmd.ferry.unload", { ferryId: "f1" }) },
    ]);
  });

  it("t が逆転した ack は記録せず warn", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    browser.receive(cmd("cmd.a", "c1"));
    browser.receive(cmd("cmd.b", "c2"));
    socket.receive(ack("c1", 2000));
    socket.receive(ack("c2", 1999));
    expect(inputLog(h).map((e) => e.t)).toEqual([2000]);
    expect(h.events()).toContainEqual(
      expect.objectContaining({ level: "warn", event: "session.error", op: "input_log_order" }),
    );
  });
});

describe("L-4 記録の上限", () => {
  const small = (cmdId: string) => cmd("cmd.ferry.load", cmdId, LOAD_PAYLOAD);

  it("10,000 行あれば記録する種類だけを送らず、hello・cmd.sync は送る。DO を作り直しても続く", async () => {
    const h = await harness();
    for (let i = 0; i < MAX_INPUT_LOG_ROWS; i++) appendInputLog(h.storage.sql, i, '{"type":"x"}');
    await h.init();
    await h.connect();
    await h.drain();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    browser.receive(small("c1"));
    browser.receive(msg("hello"));
    browser.receive(cmd("cmd.sync", "s1"));
    browser.receive(small("c2"));
    expect(socket.sent.slice(1)).toEqual([msg("hello"), cmd("cmd.sync", "s1")]);
    const full = h.events().filter((e) => e.event === "input_log.full");
    expect(full).toEqual([
      expect.objectContaining({ level: "warn", rows: MAX_INPUT_LOG_ROWS, bytes: 12 * 10000 }),
    ]);
    expect(browser.sent).toEqual([]); // 返事はしない

    h.recreate();
    const again = await reconnect(h);
    again.browser.receive(small("c3"));
    expect(sentCount(again.socket, small("c3"))).toBe(0);
  });

  it("バイト数: 待っている分と合わせて 512 KiB を超える電文を送らない", async () => {
    const h = await harness();
    const filler = (bytes: number) => {
      const base = commandJson("cmd.big", { p: "" });
      return commandJson("cmd.big", { p: "z".repeat(bytes - base.length) });
    };
    appendInputLog(h.storage.sql, 0, filler(MAX_INPUT_LOG_BYTES - 100));
    await h.init();
    await h.connect();
    await h.drain();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    const base = commandJson("cmd.x", { p: "" });
    const hundred = { p: "y".repeat(100 - base.length) };
    expect(commandJson("cmd.x", hundred)).toHaveLength(100);
    const first = cmd("cmd.x", "c1", hundred);
    const second = cmd("cmd.x", "c2", {});
    browser.receive(first);
    browser.receive(second); // first の ack の前（待っている 100 バイトと合わせて超える）
    expect(sentCount(socket, first)).toBe(1);
    expect(sentCount(socket, second)).toBe(0);
    socket.receive(ack("c1", 10));
    expect(inputLog(h)).toHaveLength(2);

    h.recreate();
    const again = await reconnect(h);
    again.browser.receive(cmd("cmd.x", "c3", {}));
    expect(sentCount(again.socket, cmd("cmd.x", "c3", {}))).toBe(0);
    again.browser.receive(msg("hello"));
    expect(again.socket.sent.at(-1)).toBe(msg("hello"));
  });
});

describe("L-5 保存", () => {
  it("stage_hash・build を保存し、last_clock_t は 5 秒に 1 回・切断・終わりのときに書く", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(SESSION, 0, 1));
    expect(h.row()).toMatchObject({ stageHash: "sha256:abc", build: "b1", lastClockT: 0 });

    const seen: (number | null)[] = [];
    for (let i = 1; i <= 12; i++) {
      h.clock.advance(1000);
      socket.receive(events(i * 1000, i + 1));
      seen.push(h.row()?.lastClockT ?? null);
    }
    expect(seen).toEqual([0, 0, 0, 0, 5000, 5000, 5000, 5000, 5000, 10000, 10000, 10000]);

    // 読めない値では消さない
    socket.receive(snapshot({ stageHash: "", build: 1 }, 12000, 14));
    expect(h.row()).toMatchObject({ stageHash: "sha256:abc", build: "b1" });

    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    expect(h.row()?.lastClockT).toBe(12000);

    const { socket: socket2 } = await reconnect(h);
    h.clock.advance(1000);
    socket2.receive(events(13000, 1));
    expect(h.row()?.lastClockT).toBe(12000);
    socket2.receive(msg("internal.result", { payload: { score: 1 } }));
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", lastClockT: 13000 });
  });

  it("新しい stageHash・build で上書きする", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(SESSION, 0, 1));
    socket.receive(snapshot({ stageHash: "sha256:def", build: "b2" }, 0, 2));
    expect(h.row()).toMatchObject({ stageHash: "sha256:def", build: "b2" });
  });
});

describe("R-6 復元（AC-1）", () => {
  it("止まった後につなぎ直すと、同じ引数で起動し直し、最初の電文が internal.restore", async () => {
    const { h, browser1 } = await crashAndRestore();
    expect(browser1.closes).toEqual([{ code: 1011, reason: "container restarting" }]);
    expect(browser1.sentTypes).not.toContain("error");
    expect(h.row()).toMatchObject({
      state: "running",
      containerStartedAt: null,
      readyAt: null,
      runMs: 70_000,
      lastClockT: 61200,
      containerStarts: 1,
    });
    expect(h.container.starts).toHaveLength(1);

    h.clock.t = h.t0 + 80_000;
    const healthBefore = h.container.healthCalls;
    const { browser: browser2, socket: socket2 } = await reconnect(h);
    expect(h.container.starts).toHaveLength(2);
    expect(h.container.starts[1]).toEqual(h.container.starts[0]);
    expect(h.container.healthCalls).toBe(healthBefore + 1);
    expect(parsed(socket2.sent[0])).toStrictEqual({
      v: 1,
      type: "internal.restore",
      payload: {
        sessionId: SESSION_ID,
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
            { t: 1200, cmd: { type: "cmd.ferry.load", payload: LOAD_PAYLOAD } },
            { t: 5400, cmd: { type: "cmd.game.pause", payload: { paused: true } } },
          ],
        },
        until: 61200,
      },
    });
    expect(h.row()).toMatchObject({
      state: "running",
      containerStarts: 2,
      containerStartedAt: h.t0 + 80_000,
    });

    const restored = snapshot({ ...SESSION, restored: true }, 61200, 1);
    socket2.receive(restored);
    expect(browser2.sent).toEqual([restored]);
    browser2.receive(cmd("cmd.ferry.load", "c3", LOAD_PAYLOAD));
    socket2.receive(ack("c3", 61300, true, 2));
    expect(inputLog(h).map((e) => e.t)).toEqual([1200, 5400, 61300]);
  });
});

describe("R-7 回数の上限（AC-2）", () => {
  it("3 回目に起動したコンテナが止まると container_lost。start は 3 回、settle は動いた時間の合計で 1 回", async () => {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    for (const [crashAt, connectAt] of [
      [10_000, 20_000],
      [30_000, 40_000],
    ] as const) {
      h.clock.t = h.t0 + crashAt;
      h.container.crash();
      await h.drain();
      expect(h.lastBrowser().closes).toEqual([{ code: 1011, reason: "container restarting" }]);
      h.clock.t = h.t0 + connectAt;
      const { socket } = await reconnect(h);
      expect(socket.sentTypes[0]).toBe("internal.restore");
    }
    expect(h.row()?.containerStarts).toBe(3);

    const browser3 = h.lastBrowser();
    h.clock.t = h.t0 + 50_000;
    h.container.crash();
    await h.drain();
    expect(browser3.sent.map(parsed)).toEqual([
      {
        v: 1,
        type: "error",
        seq: 1,
        payload: { code: "container_unavailable", message: "Container unavailable", fatal: true },
      },
    ]);
    expect(browser3.closes).toEqual([{ code: 1011, reason: "container unavailable" }]);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_lost" });
    expect(h.count("start")).toBe(3);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 30 }]);
    expect((await h.connect()).status).toBe(404);
    expect(h.count("start")).toBe(3);
  });
});

describe("R-8 試し直さない（AC-2）", () => {
  it("起動し直した後の restore_failed → 中継して 1011、container_error、start は 2 回のまま", async () => {
    const { h } = await crashAndRestore();
    const { browser, socket } = await reconnect(h);
    socket.receive(fatal("restore_failed"));
    await h.drain();
    expect(browser.sent).toEqual([fatal("restore_failed")]);
    expect(browser.closes).toEqual([{ code: 1011, reason: "container error" }]);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_error" });
    expect(h.container.starts).toHaveLength(2);
  });

  it.each(["bad_request", "not_initialized"])(
    "snapshot の前の fatal の %s → container_error",
    async (code) => {
      const h = await started();
      const browser = h.lastBrowser();
      h.lastContainerSocket().receive(fatal(code));
      await h.drain();
      expect(browser.sent).toEqual([fatal(code)]);
      expect(browser.closes).toEqual([{ code: 1011, reason: "container error" }]);
      expect(h.row()).toMatchObject({ state: "ended", endReason: "container_error" });
    },
  );

  it("snapshot の後の fatal でない error・fatal の bad_request では終わらない", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(SESSION, 0, 1));
    socket.receive(
      JSON.stringify({
        v: 1,
        type: "error",
        seq: 2,
        payload: { code: "bad_request", message: "m", fatal: false },
      }),
    );
    socket.receive(fatal("bad_request", 3));
    await h.drain();
    expect(h.row()?.state).toBe("running");
    expect(h.lastBrowser().closes).toEqual([]);
  });
});

describe("R-9 死活監視（AC-6）", () => {
  it("5 秒ごとに internal.ping。pong があれば続き、3 回続けてなければ destroy して 1011、つなぎ直すと復元", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(SESSION, 0, 1));
    expect(pings(socket)).toBe(0);
    h.clock.advanceTimers(5000);
    expect(parsed(socket.sent.at(-1))).toEqual({ v: 1, type: "internal.ping", payload: {} });
    socket.receive(msg("internal.pong"));
    h.clock.advanceTimers(5000);
    socket.receive(msg("internal.pong"));
    expect(pings(socket)).toBe(2);
    expect(browser.sentTypes).not.toContain("internal.pong");

    // ここから pong を返さない
    h.clock.advanceTimers(15_000);
    await h.drain();
    expect(h.count("destroy")).toBe(0);
    expect(browser.closes).toEqual([]);
    h.clock.advanceTimers(5000);
    await h.drain();
    expect(h.count("destroy")).toBe(1);
    expect(browser.closes).toEqual([{ code: 1011, reason: "container restarting" }]);
    expect(h.row()).toMatchObject({ state: "running", containerStartedAt: null });
    expect(h.clock.activeTimers).toBe(0);
    expect(h.events()).toContainEqual(
      expect.objectContaining({ event: "container.stopped", cause: "ping_timeout" }),
    );

    const { socket: socket2 } = await reconnect(h);
    expect(h.container.starts).toHaveLength(2);
    expect(socket2.sentTypes[0]).toBe("internal.restore");
  });

  it("ブラウザが閉じた後は internal.ping を送らない", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    h.lastBrowser().emit("close", { code: 1000, reason: "" });
    await h.drain();
    expect(h.clock.activeTimers).toBe(0);
    h.clock.advanceTimers(30_000);
    expect(pings(socket)).toBe(0);
  });

  it("置き換えたら古い接続の死活監視を止める", async () => {
    const h = await started();
    const socket1 = h.lastContainerSocket();
    const { socket: socket2 } = await reconnect(h);
    expect(h.clock.activeTimers).toBe(1);
    h.clock.advanceTimers(5000);
    expect(pings(socket1)).toBe(0);
    expect(pings(socket2)).toBe(1);
  });

  it("destroy が失敗 → container_lost で終わり、container_started_at が残り、30 秒後に後始末をやり直す", async () => {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    h.container.destroyError = new Error("busy");
    h.clock.advanceTimers(20_000);
    await h.drain();
    const now = h.t0 + 20_000;
    expect(h.row()).toMatchObject({
      state: "ended",
      endReason: "container_lost",
      containerStartedAt: h.t0,
      settled: false,
      alarmAt: now + 30_000,
    });
    expect(h.container.starts).toHaveLength(1);
    expect(h.costGuard.settles).toEqual([]);

    h.container.destroyError = null;
    h.clock.t = now + 30_000;
    await h.alarm();
    expect(h.row()).toMatchObject({ settled: true, containerStartedAt: null });
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 50 }]);
    expect(h.container.starts).toHaveLength(1);
  });
});

describe("R-10 つないでいないときに止まる", () => {
  async function closedThenCrashed() {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    h.clock.t = h.t0 + 10_000;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    h.clock.t = h.t0 + 20_000;
    h.container.crash();
    await h.drain();
    expect(h.container.starts).toHaveLength(1);
    expect(h.row()).toMatchObject({ state: "running", containerStartedAt: null, runMs: 20_000 });
    return h;
  }

  it("start は増えず、つなぐと起動し直して internal.restore", async () => {
    const h = await closedThenCrashed();
    const { socket } = await reconnect(h);
    expect(h.container.starts).toHaveLength(2);
    expect(socket.sentTypes[0]).toBe("internal.restore");
  });

  it("つながないまま 5 分 → idle で終わり、start は 1 回、destroy なし、settle は止まるまでの時間", async () => {
    const h = await closedThenCrashed();
    h.clock.t = h.t0 + 10_000 + 5 * MIN;
    await h.alarm();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "idle" });
    expect(h.container.starts).toHaveLength(1);
    expect(h.count("destroy")).toBe(0);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 20 }]);
  });
});

describe("R-11 DO の作り直し（AC-8）", () => {
  it("(1) running: true → monitor を呼び直し、start は増えず、最初の電文が internal.restore", async () => {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    h.recreate();
    const { socket } = await reconnect(h);
    expect(h.count("monitor")).toBe(2);
    expect(h.container.starts).toHaveLength(1);
    expect(socket.sentTypes[0]).toBe("internal.restore");
  });

  async function stoppedWhileRecreated(lastAckT: number) {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(SESSION, 0, 1));
    browser.receive(cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD));
    socket.receive(ack("c1", 1200, true, 2));
    h.clock.t = h.t0 + 5000;
    socket.receive(events(5000, 3)); // SQLite に書く
    h.clock.t = h.t0 + 6000;
    socket.receive(events(6000, 4)); // メモリだけ
    browser.receive(cmd("cmd.ferry.load", "c2", LOAD_PAYLOAD));
    socket.receive(ack("c2", lastAckT, true, 5));
    expect(h.row()?.lastClockT).toBe(5000);
    h.recreate();
    h.container.running = false;
    h.clock.t = h.t0 + 60_000;
    const again = await reconnect(h);
    expect(h.row()).toMatchObject({
      runMs: 60_000,
      containerStarts: 2,
      containerStartedAt: h.t0 + 60_000,
    });
    expect(h.container.starts).toHaveLength(2);
    const restore = parsed(again.socket.sent[0]) as {
      type: string;
      payload: { until: number; inputLog: { commands: unknown[] } };
    };
    expect(restore.type).toBe("internal.restore");
    expect(restore.payload.inputLog.commands).toHaveLength(2);
    return restore.payload.until;
  }

  it("(2) running: false → run_ms に足して起動し直し、until は last_clock_t", async () => {
    expect(await stoppedWhileRecreated(3000)).toBe(5000);
  });

  it("(2) running: false → until は最後の t のほうが大きければそれ", async () => {
    expect(await stoppedWhileRecreated(8000)).toBe(8000);
  });

  it("(3) 作り直した後の monitor() が running: true のまま終わる → 起動し直さず container.monitor_ended", async () => {
    const h = await started();
    h.recreate();
    const { browser } = await reconnect(h);
    h.container.exit.resolve(); // running は true のまま
    await h.drain();
    expect(h.container.starts).toHaveLength(1);
    expect(h.count("destroy")).toBe(0);
    expect(browser.closes).toEqual([]);
    expect(h.row()?.state).toBe("running");
    expect(h.events()).toContainEqual(
      expect.objectContaining({ level: "warn", event: "container.monitor_ended" }),
    );
    // 次の /connect で見張り直す
    await reconnect(h);
    expect(h.count("monitor")).toBe(3);
    expect(h.container.starts).toHaveLength(1);
  });
});

describe("R-12 コストガード（AC-7）", () => {
  it("起動し直しは /connect の後でだけ。reserve を呼ばず、deadline_at を変えない", async () => {
    const { h } = await crashAndRestore();
    const deadline = h.row()?.deadlineAt;
    expect(h.container.starts).toHaveLength(1);
    await h.alarm(); // まだ鳴る時刻ではないが、鳴っても起動しない
    expect(h.container.starts).toHaveLength(1);
    expect((await h.connect("X".repeat(22))).status).toBe(404);
    await h.drain();
    expect(h.container.starts).toHaveLength(1);
    await reconnect(h);
    expect(h.container.starts).toHaveLength(2);
    expect(h.row()?.deadlineAt).toBe(deadline);
    // FakeCostGuard.reserve は呼ばれると例外を投げる
    expect(h.events().filter((e) => e.event === "session.error")).toEqual([]);
  });

  it("deadline_at を過ぎてから止まる → time_limit で終わり、start は増えない", async () => {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    h.clock.t = h.t0 + 30 * MIN;
    h.container.crash();
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "time_limit" });
    expect(h.container.starts).toHaveLength(1);
  });

  it("止まった後、deadline_at を過ぎてからつなぎ直す → 起動せず time_limit", async () => {
    const { h } = await crashAndRestore();
    h.clock.t = h.t0 + 30 * MIN;
    expect((await h.connect()).status).toBe(200);
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "time_limit" });
    expect(h.container.starts).toHaveLength(1);
    expect(parsed(h.lastBrowser().sent.at(-1))).toMatchObject({
      type: "error",
      payload: { code: "session_ended" },
    });
  });
});

describe("R-13 stage_hash がない", () => {
  it("session のない snapshot のまま止まる → つなぐと internal.init", async () => {
    const h = await started();
    h.lastContainerSocket().receive(snapshot(null, 0, 1));
    h.container.crash();
    await h.drain();
    const { socket } = await reconnect(h);
    expect(h.container.starts).toHaveLength(2);
    expect(socket.sentTypes[0]).toBe("internal.init");
  });

  it("stage_hash がないのに記録が 1 行ある → container_lost", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    socket.receive(snapshot(null, 0, 1));
    browser.receive(cmd("cmd.ferry.load", "c1", LOAD_PAYLOAD));
    socket.receive(ack("c1", 100, true, 2));
    expect(inputLog(h)).toHaveLength(1);
    h.container.crash();
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_lost" });
    expect(parsed(browser.sent.at(-1))).toMatchObject({
      type: "error",
      payload: { code: "container_unavailable" },
    });
    expect(h.container.starts).toHaveLength(1);
  });
});

describe("R-14 起動し直したコンテナの期限切れ", () => {
  it("2 回目のコンテナの /healthz が 503 のまま → start_timeout として止まったと判断し、終わらない", async () => {
    const h = await started({ env: { CONTAINER_START_TIMEOUT_MS: "1000" } });
    h.lastContainerSocket().receive(snapshot(SESSION, 0, 1));
    h.container.crash();
    await h.drain();
    h.container.healthDefault = 503;
    const wsBefore = h.container.wsCalls;
    expect((await h.connect()).status).toBe(200);
    await h.drain();
    expect(h.container.starts).toHaveLength(2);
    expect(h.count("destroy")).toBe(1);
    expect(h.lastBrowser().closes).toEqual([{ code: 1011, reason: "container restarting" }]);
    expect(h.container.wsCalls).toBe(wsBefore);
    expect(h.row()).toMatchObject({
      state: "running",
      containerStarts: 2,
      containerStartedAt: null,
    });
    expect(h.events()).toContainEqual(
      expect.objectContaining({ event: "container.stopped", cause: "start_timeout" }),
    );
  });
});

describe("R-15 ログ（AC-9）", () => {
  it("止まって 1 回復元し、ラウンドが終わる 1 ゲームで、中身を出さず 25 行以下", async () => {
    const { h } = await crashAndRestore();
    const { socket } = await reconnect(h);
    socket.receive(snapshot({ ...SESSION, restored: true }, 61200, 1));
    socket.receive(msg("round.result", { seq: 2 }));
    h.clock.t = h.t0 + 200_000;
    socket.receive(msg("internal.result", { payload: { score: 1 } }));
    await h.drain();
    expect(h.row()?.endReason).toBe("round_ended");

    expect(h.logs.length).toBeLessThanOrEqual(25);
    for (const line of h.logs) {
      expect(JSON.parse(line)).toMatchObject({ sessionId: SESSION_ID });
      for (const forbidden of [
        TOKEN,
        h.tokenHash,
        "sha256:abc",
        '"b1"',
        "b1",
        "ferryId",
        "internal.restore",
      ]) {
        expect(line).not.toContain(forbidden);
      }
    }
    const events = h.events().map((e) => e.event);
    expect(events).toContain("container.stopped");
    expect(events).toContain("session.restore");
    expect(h.events()).toContainEqual(
      expect.objectContaining({ event: "container.start", containerStarts: 2, restart: true }),
    );
    expect(h.events()).toContainEqual(
      expect.objectContaining({ event: "session.restore", commands: 2, until: 61200 }),
    );
    expect(h.events()).toContainEqual(
      expect.objectContaining({ event: "container.stopped", cause: "monitor", containerStarts: 1 }),
    );
  });
});
