// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";

import type { Ack, Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { GameCommandProvider } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { DemoKeys } from "../demo/DemoKeys.tsx";
import { makeFerrySnapshot } from "../ferry/testing/world.ts";
import { GameShortcuts } from "./GameShortcuts.tsx";

type SendFn = (type: string, payload: unknown) => Promise<Ack>;
type SendMock = Mock<SendFn>;

const OK: Ack = { cmdId: "c-1", ok: true, t: 29000 };

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

function load(snapshot: Snapshot = makeFerrySnapshot()): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function demoSnapshot(): Snapshot {
  return makeFerrySnapshot({
    session: { id: "s-demo", stageId: "stage-demo", seed: 1, demo: true, restored: false },
  });
}

function pausedSnapshot(): Snapshot {
  const base = makeFerrySnapshot();
  return { ...base, clock: { ...base.clock, paused: true } };
}

function okSend(): SendMock {
  return vi.fn<SendFn>(() => Promise.resolve(OK));
}

function renderWith(send: SendMock | null): ReturnType<typeof render> {
  return render(
    <GameCommandProvider send={send as GameCommandSender | null}>
      <GameShortcuts />
    </GameCommandProvider>,
  );
}

async function flush(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

const KEYS: Readonly<Record<string, string>> = { Space: " ", Digit2: "2", Digit3: "3" };

// keydown を送り、イベントの defaultPrevented を返す（fireEvent は preventDefault されたら false を返す）。
function down(code: string, target: Window | Element = window): boolean {
  let result = true;
  act(() => {
    result = fireEvent.keyDown(target, { code, key: KEYS[code] ?? code });
  });
  return !result;
}

function deferred(): { promise: Promise<Ack>; resolve: (ack: Ack) => void } {
  let resolve: (ack: Ack) => void = () => undefined;
  const promise = new Promise<Ack>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function status(): string | null {
  return screen.getByRole("status").textContent;
}

describe("GameShortcuts", () => {
  it("GS-1 航行中・デモでない世界の Space で一時停止・再開を送り、既定の動きを止める", async () => {
    load();
    const send = okSend();
    renderWith(send);
    expect(down("Space")).toBe(true);
    expect(send).toHaveBeenCalledExactlyOnceWith("cmd.game.pause", { paused: true });
    await flush();

    load(pausedSnapshot());
    expect(down("Space")).toBe(true);
    expect(send).toHaveBeenLastCalledWith("cmd.game.pause", { paused: false });
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("GS-2 デモの世界の Space はこの部品からは送らず、DemoKeys と並べても 1 回だけ送る", async () => {
    load(demoSnapshot());
    const send = okSend();
    renderWith(send);
    expect(down("Space")).toBe(false);
    expect(send).not.toHaveBeenCalled();
    cleanup();

    render(
      <GameCommandProvider send={send as GameCommandSender}>
        <DemoKeys />
        <GameShortcuts />
      </GameCommandProvider>,
    );
    down("Space");
    await flush();
    expect(send).toHaveBeenCalledExactlyOnceWith("cmd.game.pause", { paused: true });
  });

  it("GS-3 開会式の世界・send が null では送らず、既定の動きも止めない", () => {
    const send = okSend();
    renderWith(send);
    expect(down("Space")).toBe(false); // world が null
    for (const phase of ["intro", "ended"] as const) {
      load(makeFerrySnapshot({ phase }));
      expect(down("Space")).toBe(false);
    }
    expect(send).not.toHaveBeenCalled();
    cleanup();

    load();
    renderWith(null);
    expect(down("Space")).toBe(false);
  });

  it("GS-4 結果を待っている間の 2 回目の Space は送らず、解決した後はまた送れる", async () => {
    load();
    const first = deferred();
    const send: SendMock = vi.fn<SendFn>(() => first.promise);
    renderWith(send);
    down("Space");
    expect(down("Space")).toBe(false);
    expect(send).toHaveBeenCalledTimes(1);

    first.resolve(OK);
    await flush();
    send.mockImplementation(() => Promise.resolve(OK));
    expect(down("Space")).toBe(true);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("GS-5 失敗したら HUD と同じ文を状態の文に入れ、成功で消す", async () => {
    load();
    const send: SendMock = vi.fn<SendFn>(() =>
      Promise.resolve<Ack>({ ...OK, ok: false, code: "phase_invalid" }),
    );
    renderWith(send);
    down("Space");
    await flush();
    expect(status()).toBe("いまは一時停止・再開できません。");

    send.mockImplementation(() => Promise.resolve<Ack>({ ...OK, ok: false, code: "rate_limited" }));
    down("Space");
    await flush();
    expect(status()).toBe("操作が多すぎます。少し待ってください。");

    send.mockImplementation(() => Promise.resolve<Ack>({ ...OK, ok: false, code: "bad_request" }));
    down("Space");
    await flush();
    expect(status()).toBe("一時停止・再開できませんでした。");

    send.mockImplementation(() => Promise.reject(new Error("closed")));
    down("Space");
    await flush();
    expect(status()).toBe("サーバーにつながっていないため、一時停止・再開できませんでした。");

    send.mockImplementation(() => Promise.resolve(OK));
    down("Space");
    await flush();
    expect(status()).toBe("");
  });

  it("GS-6 Digit2 で 2 隻目を選んで名前を読み上げ、無い番号の Digit3 は何もしない", () => {
    load();
    const send = okSend();
    renderWith(send);
    expect(down("Digit2")).toBe(true);
    expect(gameStore.getState().selection.ferryId).toBe("f2");
    expect(status()).toBe("しまかぜを選びました");

    expect(down("Digit3")).toBe(false);
    expect(gameStore.getState().selection.ferryId).toBe("f2");
    expect(status()).toBe("しまかぜを選びました");

    // 同じ番号を押しても選んだまま。ボタンにフォーカスがあっても選べる。開会式の間・デモでも選べる。
    down("Digit2");
    expect(gameStore.getState().selection.ferryId).toBe("f2");
    const button = document.createElement("button");
    document.body.append(button);
    button.focus();
    act(() => {
      fireEvent.keyDown(button, { code: "Numpad1", key: "1" });
    });
    expect(gameStore.getState().selection.ferryId).toBe("f1");
    expect(status()).toBe("ごとう丸を選びました");

    load(makeFerrySnapshot({ phase: "intro" }));
    down("Digit2");
    expect(gameStore.getState().selection.ferryId).toBe("f2");
    load(demoSnapshot());
    act(() => {
      fireEvent.keyDown(window, { code: "Digit1", key: "1" });
    });
    expect(gameStore.getState().selection.ferryId).toBe("f1");
    expect(send).not.toHaveBeenCalled();
  });

  it("GS-7 アンマウントの後の keydown では何も起きず、結果が届いても警告が出ない", async () => {
    load();
    const errors = vi.spyOn(console, "error");
    const pending = deferred();
    const send: SendMock = vi.fn<SendFn>(() => pending.promise);
    const { unmount } = renderWith(send);
    down("Space");
    expect(send).toHaveBeenCalledTimes(1);
    unmount();

    pending.resolve({ ...OK, ok: false, code: "bad_request" });
    await flush();
    expect(down("Space")).toBe(false);
    expect(down("Digit2")).toBe(false);
    expect(send).toHaveBeenCalledTimes(1);
    expect(gameStore.getState().selection.ferryId).toBeNull();
    expect(errors).not.toHaveBeenCalled();
  });
});
