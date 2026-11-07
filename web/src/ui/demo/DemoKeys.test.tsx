// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";

import type { Ack, Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { GameCommandProvider } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { DemoKeys } from "./DemoKeys.tsx";
import { makeSnapshot } from "./testing/world.ts";

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

function load(snapshot: Snapshot = makeSnapshot()): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function okSend(): SendMock {
  return vi.fn<SendFn>(() => Promise.resolve(OK));
}

function renderWith(send: SendMock | null): ReturnType<typeof render> {
  return render(
    <GameCommandProvider send={send as GameCommandSender | null}>
      <DemoKeys />
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

type KeyInit = { shiftKey?: boolean; repeat?: boolean; ctrlKey?: boolean; metaKey?: boolean };

const KEYS: Readonly<Record<string, string>> = {
  KeyD: "d",
  KeyF: "f",
  KeyN: "n",
  Space: " ",
  ShiftLeft: "Shift",
};

function down(code: string, init: KeyInit = {}, target: Window | Element = window): boolean {
  let result = true;
  act(() => {
    result = fireEvent.keyDown(target, { code, key: KEYS[code] ?? code, ...init });
  });
  return result;
}

function up(code: string, init: KeyInit = {}): void {
  act(() => {
    fireEvent.keyUp(window, { code, key: KEYS[code] ?? code, ...init });
  });
}

function deferred(): { promise: Promise<Ack>; resolve: (ack: Ack) => void } {
  let resolve: (ack: Ack) => void = () => undefined;
  const promise = new Promise<Ack>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function pressAll(): void {
  down("KeyD");
  down("KeyF");
  up("KeyF");
  down("KeyN");
  down("Space");
}

describe("DemoKeys", () => {
  it("DC-1 デモでないセッション・world が null では何も描かず、送らない", () => {
    const send = okSend();
    const { container } = renderWith(send);
    expect(container.innerHTML).toBe("");
    pressAll();

    load(
      makeSnapshot({ session: { id: "s", stageId: "x", seed: 1, demo: false, restored: false } }),
    );
    expect(container.innerHTML).toBe("");
    pressAll();
    expect(send).not.toHaveBeenCalled();
  });

  it("DC-2 D で demo.trigger を 1 回送り、繰り返し・結果待ちの D は送らない", async () => {
    load();
    const first = deferred();
    const send: SendMock = vi.fn<SendFn>(() => first.promise);
    renderWith(send);
    expect(screen.getByRole("complementary", { name: "デモ操作" })).not.toBeNull();

    down("KeyD");
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenLastCalledWith("demo.trigger", { event: "storm" });
    down("KeyD", { repeat: true });
    down("KeyD");
    expect(send).toHaveBeenCalledTimes(1);

    first.resolve(OK);
    await flush();
    send.mockImplementation(() => Promise.resolve(OK));
    down("KeyD", { shiftKey: true });
    expect(send).toHaveBeenCalledTimes(2);
    expect(send).toHaveBeenLastCalledWith("demo.trigger", { event: "storm" });
  });

  it("DC-3 N で cmd.game.skip を送る", () => {
    load();
    const send = okSend();
    renderWith(send);
    down("KeyN");
    expect(send).toHaveBeenCalledExactlyOnceWith("cmd.game.skip", { until: "next_notable" });
  });

  it("DC-4 Space で一時停止・再開を送り、ボタンにフォーカスがあるときは送らない", async () => {
    load();
    const send = okSend();
    renderWith(send);
    expect(down("Space")).toBe(false);
    expect(send).toHaveBeenLastCalledWith("cmd.game.pause", { paused: true });
    await flush();

    load(makeSnapshot({}, { paused: true }));
    down("Space");
    expect(send).toHaveBeenLastCalledWith("cmd.game.pause", { paused: false });
    await flush();
    expect(send).toHaveBeenCalledTimes(2);

    const button = document.createElement("button");
    button.textContent = "積む";
    document.body.append(button);
    button.focus();
    expect(down("Space", {}, button)).toBe(true);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("DC-5 F を押している間は 4 倍速（Shift で 8 倍速）、離すと元の速度", () => {
    load();
    const send = okSend();
    renderWith(send);

    down("KeyF");
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 4 });
    expect(screen.getByText("早送り中 ×4")).not.toBeNull();
    down("KeyF", { repeat: true });
    expect(send).toHaveBeenCalledTimes(1);
    down("ShiftLeft", { shiftKey: true });
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 8 });
    expect(screen.getByText("早送り中 ×8")).not.toBeNull();
    up("ShiftLeft");
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 4 });
    up("KeyF");
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 1 });
    expect(send).toHaveBeenCalledTimes(4);
    expect(screen.queryByText(/早送り中/)).toBeNull();

    down("KeyF", { shiftKey: true });
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 8 });
    up("KeyF", { shiftKey: true });
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 1 });
    expect(send).toHaveBeenCalledTimes(6);
  });

  it("DC-6 すでに 4 倍速なら送らない。blur で押したときの速度に戻す", () => {
    load(makeSnapshot({}, { speed: 4 }));
    const send = okSend();
    renderWith(send);
    down("KeyF");
    up("KeyF");
    expect(send).not.toHaveBeenCalled();

    load();
    down("KeyF");
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 4 });
    act(() => {
      fireEvent.blur(window);
    });
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 1 });
    up("KeyF");
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("DC-7 航行中でない・送る関数がないときは送らず、一覧を薄くする", () => {
    const send = okSend();
    renderWith(send);
    for (const phase of ["intro", "ended"] as const) {
      load(makeSnapshot({ phase }));
      expect(down("Space")).toBe(true);
      pressAll();
      expect(screen.getByRole("list").getAttribute("aria-disabled")).toBe("true");
    }
    expect(send).not.toHaveBeenCalled();
    cleanup();

    load();
    render(<DemoKeys />);
    pressAll();
    expect(screen.getByRole("list").getAttribute("aria-disabled")).toBe("true");
  });

  it("DC-8 入力欄にフォーカス・修飾キーつきでは送らない", () => {
    load();
    const send = okSend();
    renderWith(send);
    expect(screen.getByRole("list").hasAttribute("aria-disabled")).toBe(false);
    const input = document.createElement("input");
    document.body.append(input);
    input.focus();
    down("KeyD", {}, input);
    down("Space", {}, input);
    down("KeyD", { ctrlKey: true });
    down("KeyF", { metaKey: true });
    expect(send).not.toHaveBeenCalled();
  });

  it("DC-9 失敗したら code に応じた文を出し、次の成功で消す", async () => {
    load();
    const send: SendMock = vi.fn<SendFn>(() =>
      Promise.resolve<Ack>({ ...OK, ok: false, code: "phase_invalid" }),
    );
    renderWith(send);
    const status = screen.getByRole("status");

    down("KeyD");
    await flush();
    expect(status.textContent).toBe("いまは嵐を起こせません。");

    send.mockImplementation(() => Promise.resolve<Ack>({ ...OK, ok: false, code: "rate_limited" }));
    down("KeyD");
    await flush();
    expect(status.textContent).toBe("操作が多すぎます。少し待ってください。");

    send.mockImplementation(() => Promise.resolve<Ack>({ ...OK, ok: false, code: "bad_request" }));
    down("KeyN");
    await flush();
    expect(status.textContent).toBe("次の出来事まで進められませんでした。");

    send.mockImplementation(() => Promise.reject(new Error("closed")));
    down("Space");
    await flush();
    expect(status.textContent).toBe(
      "サーバーにつながっていないため、一時停止・再開できませんでした。",
    );

    send.mockImplementation(() => Promise.resolve(OK));
    down("KeyD");
    await flush();
    expect(status.textContent).toBe("");
  });

  it("DC-10 アンマウントの後は送らず、結果待ちのままアンマウントしても警告が出ない", async () => {
    load();
    const errors = vi.spyOn(console, "error");
    const pending = deferred();
    const send: SendMock = vi.fn<SendFn>(() => pending.promise);
    const { unmount } = renderWith(send);
    down("KeyD");
    expect(send).toHaveBeenCalledTimes(1);
    unmount();

    pending.resolve({ ...OK, ok: false, code: "bad_request" });
    await flush();
    down("KeyD");
    down("KeyF");
    up("KeyF");
    expect(send).toHaveBeenCalledTimes(1);
    expect(errors).not.toHaveBeenCalled();
  });
});
