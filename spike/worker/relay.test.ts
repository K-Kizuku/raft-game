import { describe, expect, it } from "vitest";
import { exceedsBytes, newRelayStats, startRelay } from "./relay.ts";
import { FakeSocket } from "./test-fakes.ts";

async function connected() {
  const events: string[] = [];
  const browser = new FakeSocket("browser", events);
  const container = new FakeSocket("container", events);
  const stats = newRelayStats();
  await startRelay(browser, async () => container, stats);
  return { browser, container, stats, events };
}

describe("relay", () => {
  it("W-1: テキストが両方向に、中身を変えずに同じ順序で渡る", async () => {
    const { browser, container, stats } = await connected();
    const msgs = ['{"seq":1}', "plain text", "日本語", '{"seq":2,"pad":"  "}'];
    for (const m of msgs) browser.emit("message", { data: m });
    expect(container.sent).toEqual(msgs);
    const back = ['{"seq":1,"serverAt":1}', "x", "日本語"];
    for (const m of back) container.emit("message", { data: m });
    expect(browser.sent).toEqual(back);
    expect(stats).toMatchObject({ open: true, toContainer: 4, toBrowser: 3, droppedOversize: 0, droppedBinary: 0 });
  });

  it("W-1: つながる前に届いた電文は、つながった後に順に送る", async () => {
    const browser = new FakeSocket("browser");
    const container = new FakeSocket("container");
    const stats = newRelayStats();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const done = startRelay(
      browser,
      async () => {
        await gate;
        return container;
      },
      stats,
    );
    browser.emit("message", { data: "a" });
    browser.emit("message", { data: "b" });
    expect(container.sent).toEqual([]);
    release();
    await done;
    browser.emit("message", { data: "c" });
    expect(container.sent).toEqual(["a", "b", "c"]);
    expect(stats.toContainer).toBe(3);
  });

  it("W-2: 16384 バイトちょうどは渡り、16385 バイトとバイナリは捨てて数える", async () => {
    const { browser, container, stats } = await connected();
    const exact = "a".repeat(16384);
    browser.emit("message", { data: exact });
    browser.emit("message", { data: "a".repeat(16385) });
    browser.emit("message", { data: "あ".repeat(5462) }); // 16386 バイト（文字数は 5462）
    browser.emit("message", { data: "あ".repeat(5461) + "a" }); // 16384 バイト
    browser.emit("message", { data: new ArrayBuffer(8) });
    browser.emit("message", { data: new Uint8Array(2) });
    expect(container.sent).toEqual([exact, "あ".repeat(5461) + "a"]);
    expect(stats.droppedOversize).toBe(2);
    expect(stats.droppedBinary).toBe(2);
    expect(stats.toContainer).toBe(2);
  });

  it("W-3: ブラウザが閉じたらコンテナ側を 1000 で閉じる", async () => {
    const { browser, container, stats } = await connected();
    browser.emit("close", { code: 1000, reason: "bye" });
    expect(container.closes.map((c) => c.code)).toEqual([1000]);
    expect(stats).toMatchObject({ open: false, closedBy: "browser", browserCloseCode: 1000 });
    browser.emit("message", { data: "late" });
    expect(container.sent).toEqual([]);
  });

  it("W-3: コンテナが閉じた・エラーならブラウザ側を 1011 で閉じる", async () => {
    for (const kind of ["close", "error"] as const) {
      const { browser, container, stats } = await connected();
      container.emit(kind, kind === "close" ? { code: 1006, reason: "" } : new Error("reset"));
      expect(browser.closes.map((c) => c.code)).toEqual([1011]);
      expect(stats).toMatchObject({ open: false, closedBy: "container" });
      container.emit("close", { code: 1006, reason: "" }); // 2 回目は何もしない
      expect(browser.closes).toHaveLength(1);
    }
  });

  it("W-4: コンテナへの接続が失敗（webSocket がない・例外）ならブラウザ側を 1011 で閉じる", async () => {
    for (const connect of [async () => null, async () => Promise.reject(new Error("refused"))]) {
      const browser = new FakeSocket("browser");
      const stats = newRelayStats();
      await startRelay(browser, connect, stats);
      expect(browser.accepted).toBe(true);
      expect(browser.closes.map((c) => c.code)).toEqual([1011]);
      expect(stats).toMatchObject({ open: false, closedBy: "connect_failed" });
      browser.emit("message", { data: "x" });
      expect(stats.toContainer).toBe(0);
    }
  });

  it("close が例外を投げても中継は壊れない", async () => {
    const { browser, container } = await connected();
    container.close = () => {
      throw new Error("already closed");
    };
    expect(() => browser.emit("close", { code: 1001, reason: "" })).not.toThrow();
  });

  it("exceedsBytes は UTF-8 のバイト数で数える", () => {
    expect(exceedsBytes("a".repeat(10), 10)).toBe(false);
    expect(exceedsBytes("a".repeat(11), 10)).toBe(true);
    expect(exceedsBytes("ああ", 6)).toBe(false);
    expect(exceedsBytes("ああa", 6)).toBe(true);
    expect(exceedsBytes("😀", 4)).toBe(false);
    expect(exceedsBytes("😀", 3)).toBe(true);
  });
});
