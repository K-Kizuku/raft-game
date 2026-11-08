// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Ack, Snapshot } from "../../protocol/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { gameStore } from "../../state/index.ts";
import { GameCommandProvider } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { setShowRaftTerms } from "../terms/preference.ts";
import { HudBar } from "./HudBar.tsx";
import { Notifications } from "./Notifications.tsx";
import { makeSnapshot } from "./testing/world.ts";

let nowMs = 1000;

beforeEach(() => {
  gameStore.getState().reset();
  nowMs = 1000;
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
  vi.spyOn(performance, "now").mockImplementation(() => nowMs);
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  vi.useRealTimers();
  vi.restoreAllMocks();
  setShowRaftTerms(false);
});

function load(snapshot: Snapshot = makeSnapshot()): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, performance.now());
  });
}

function withClock(clock: Partial<Snapshot["clock"]>, overrides: Partial<Snapshot> = {}): Snapshot {
  const base = makeSnapshot(overrides);
  return { ...base, clock: { ...base.clock, ...clock } };
}

function renderHud(send?: GameCommandSender | null): ReturnType<typeof render> {
  const ui: ReactElement =
    send === undefined ? (
      <HudBar />
    ) : (
      <GameCommandProvider send={send}>
        <HudBar />
      </GameCommandProvider>
    );
  return render(ui);
}

function text(container: HTMLElement, selector: string): string | null | undefined {
  return container.querySelector(selector)?.textContent;
}

function ok(): Promise<Ack> {
  return Promise.resolve({ cmdId: "c-1", ok: true, t: 29000 });
}

function mockSend(impl: () => Promise<Ack> = ok) {
  return vi.fn<GameCommandSender>(impl);
}

// <output> も role="status" を持つので、失敗の文の <p> を直接探す。
function statusText(): string | null | undefined {
  return document.querySelector("p[role=status]")?.textContent;
}

function buttons(): HTMLButtonElement[] {
  const group = screen.getByRole("group", { name: "速度" });
  return [...group.querySelectorAll("button")];
}

describe("HudBar", () => {
  it("HB-1 world が null なら「状況（準備中）」", () => {
    renderHud();
    expect(screen.getByText("状況（準備中）")).toBeDefined();
  });

  it("HB-2 残り時間・スコア・確定・フェス準備度・重要な資源", () => {
    load();
    const { container } = renderHud();
    expect(text(container, ".hud-time")).toBe("⏱残り時間2:31");
    expect(text(container, ".hud-score")).toBe("スコア 1,240");
    expect(text(container, ".hud-successes")).toBe("確定 5/8");
    const meter = screen.getByRole("meter", { name: "フェス準備度" });
    expect(meter.getAttribute("value")).toBe("62");
    expect(text(container, ".hud-fest")).toContain("62%");
    const list = screen.getByRole("list", { name: "重要な資源" });
    const items = [...list.querySelectorAll("li")];
    expect(items.map((li) => li.textContent)).toEqual(["🎆大花火 A（確定）", "🎆大花火 B（まだ）"]);
    expect(items[0]?.className).toContain("is-secured");
    expect(items[1]?.className).not.toContain("is-secured");
    cleanup();

    load(makeSnapshot({ score: { total: -50, successes: 5, target: 0, criticalSecured: [] } }));
    const zero = renderHud();
    expect(text(zero.container, ".hud-successes")).toBe("確定 5");
    expect(text(zero.container, ".hud-score")).toBe("スコア -50");
    expect(screen.getByRole("meter", { name: "フェス準備度" }).getAttribute("value")).toBe("0");
    cleanup();

    const base = makeSnapshot();
    load({
      ...base,
      stage: { ...base.stage, resources: base.stage.resources.filter((r) => !r.critical) },
    });
    renderHud();
    expect(screen.queryByRole("list", { name: "重要な資源" })).toBeNull();
  });

  it("HB-2 用語の併記がオンなら「確定（Apply）」", () => {
    setShowRaftTerms(true);
    load();
    const { container } = renderHud();
    expect(text(container, ".hud-successes")).toBe("確定（Apply） 5/8");
  });

  it("HB-3 時刻が進むと残り時間が減り、一時停止中は減らない", () => {
    load();
    const { container } = renderHud();
    expect(text(container, ".hud-time")).toContain("2:31");
    nowMs += 1000;
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(text(container, ".hud-time")).toContain("2:30");
    cleanup();

    nowMs = 1000;
    load(withClock({ speed: 2 }));
    const fast = renderHud();
    nowMs += 1000;
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(text(fast.container, ".hud-time")).toContain("2:29");
    cleanup();

    nowMs = 1000;
    load(withClock({ paused: true }));
    const paused = renderHud();
    nowMs += 1000;
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(text(paused.container, ".hud-time")).toContain("2:31");
  });

  it("HB-3 新しい時計が届いたらすぐ読み直し、endsAt を超えたら 0:00", () => {
    load();
    const { container } = renderHud();
    nowMs += 500;
    act(() => {
      gameStore
        .getState()
        .applyEvents({ t: 100000, speed: 1, paused: false, endsAt: 180000 }, [], 1000);
    });
    // 受け取った時刻 1000 から 500 進んでいる。
    expect(text(container, ".hud-time")).toContain("1:20");
    act(() => {
      gameStore
        .getState()
        .applyEvents({ t: 181000, speed: 1, paused: false, endsAt: 180000 }, [], nowMs);
    });
    expect(text(container, ".hud-time")).toContain("0:00");
  });

  it("HB-4 速くする・遅くするは cmd.game.speed を送り、表示は時計が変わるまで変えない", async () => {
    load();
    const send = mockSend();
    renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "速くする" }));
    });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith("cmd.game.speed", { speed: 2 });
    expect(statusText()).toBe("");
    expect(screen.getByLabelText("いまの速度").textContent).toBe("1x");

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "遅くする" }));
    });
    expect(send).toHaveBeenLastCalledWith("cmd.game.speed", { speed: 0.5 });

    act(() => {
      gameStore
        .getState()
        .applyEvents({ t: 30000, speed: 4, paused: false, endsAt: 180000 }, [], nowMs);
    });
    expect(screen.getByLabelText("いまの速度").textContent).toBe("4x");
    expect(screen.getByRole("button", { name: "速くする" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "遅くする" }).hasAttribute("disabled")).toBe(false);

    act(() => {
      gameStore
        .getState()
        .applyEvents({ t: 31000, speed: 0.5, paused: false, endsAt: 180000 }, [], nowMs);
    });
    expect(screen.getByRole("button", { name: "遅くする" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "速くする" }).hasAttribute("disabled")).toBe(false);
  });

  it("HB-4 allowedSpeeds が空なら両方の速度のボタンが無効", () => {
    load(withClock({ allowedSpeeds: [] }));
    renderHud(mockSend());
    expect(screen.getByRole("button", { name: "速くする" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "遅くする" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "⏸ 一時停止" }).hasAttribute("disabled")).toBe(false);
  });

  it("HB-5 一時停止・再開は cmd.game.pause を送る", async () => {
    load();
    const send = mockSend();
    renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "⏸ 一時停止" }));
    });
    expect(send).toHaveBeenCalledWith("cmd.game.pause", { paused: true });
    // 押しただけでは表示を変えない。
    expect(screen.getByRole("button", { name: "⏸ 一時停止" })).toBeDefined();
    cleanup();

    load(withClock({ paused: true }));
    const send2 = mockSend();
    renderHud(send2);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "▶ 再開" }));
    });
    expect(send2).toHaveBeenCalledWith("cmd.game.pause", { paused: false });
  });

  it.each<[string, GameCommandSender | null | undefined, Snapshot["phase"]]>([
    ["Provider の外", undefined, "sailing"],
    ["send が null", null, "sailing"],
    ["intro", mockSend(), "intro"],
    ["opening", mockSend(), "opening"],
    ["ended", mockSend(), "ended"],
  ])("HB-6 %s なら 3 つのボタンが無効", (_, send, phase) => {
    load(makeSnapshot({ phase }));
    renderHud(send);
    const all = buttons();
    expect(all).toHaveLength(3);
    for (const b of all) expect(b.disabled).toBe(true);
  });

  it("HB-6 送っている間は 3 つのボタンが無効", async () => {
    load();
    let resolve: (ack: Ack) => void = () => {};
    const send = mockSend(
      () =>
        new Promise<Ack>((r) => {
          resolve = r;
        }),
    );
    renderHud(send);
    for (const b of buttons()) expect(b.disabled).toBe(false);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "速くする" }));
    });
    for (const b of buttons()) expect(b.disabled).toBe(true);
    await act(async () => {
      resolve({ cmdId: "c-1", ok: true, t: 29000 });
    });
    for (const b of buttons()) expect(b.disabled).toBe(false);
  });

  it.each<[string, () => Promise<Ack>, string]>([
    [
      "phase_invalid",
      () => Promise.resolve({ cmdId: "c-1", ok: false, t: 0, code: "phase_invalid" }),
      "いまは速度を変更できません。",
    ],
    [
      "rate_limited",
      () => Promise.resolve({ cmdId: "c-1", ok: false, t: 0, code: "rate_limited" }),
      "操作が多すぎます。少し待ってください。",
    ],
    [
      "route_blocked",
      () => Promise.resolve({ cmdId: "c-1", ok: false, t: 0, code: "route_blocked" }),
      "速度を変更できませんでした。",
    ],
    [
      "reject",
      () => Promise.reject(new Error("not_connected")),
      "サーバーにつながっていないため、速度を変更できませんでした。",
    ],
  ])("HB-7 速くするの失敗（%s）を出し、次に成功したら消す", async (_, fail, want) => {
    load();
    const send = mockSend(fail);
    renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "速くする" }));
    });
    expect(statusText()).toBe(want);
    for (const b of buttons()) expect(b.disabled).toBe(false);

    send.mockImplementation(ok);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "速くする" }));
    });
    expect(statusText()).toBe("");
  });

  it("HB-7 一時停止の失敗は「一時停止・再開」", async () => {
    load();
    const send = mockSend(() =>
      Promise.resolve({ cmdId: "c-1", ok: false, t: 0, code: "phase_invalid" }),
    );
    renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "⏸ 一時停止" }));
    });
    expect(statusText()).toBe("いまは一時停止・再開できません。");
  });

  it("HB-7 送っている間にアンマウントしても警告・例外が出ない", async () => {
    load();
    const errors = vi.spyOn(console, "error");
    const warns = vi.spyOn(console, "warn");
    let resolve: (ack: Ack) => void = () => {};
    let reject: (err: Error) => void = () => {};
    const send = mockSend(
      () =>
        new Promise<Ack>((res, rej) => {
          resolve = res;
          reject = rej;
        }),
    );
    const view = renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "速くする" }));
    });
    view.unmount();
    await act(async () => {
      resolve({ cmdId: "c-1", ok: false, t: 0, code: "phase_invalid" });
    });

    const again = renderHud(send);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "⏸ 一時停止" }));
    });
    again.unmount();
    await act(async () => {
      reject(new Error("disconnected"));
    });
    expect(errors).not.toHaveBeenCalled();
    expect(warns).not.toHaveBeenCalled();
  });

  it("HB-8 allowedSpeeds に無い速度 8 は「8x」で、遅くするで 4", async () => {
    load(withClock({ speed: 8 }));
    const send = mockSend();
    renderHud(send);
    expect(screen.getByLabelText("いまの速度").textContent).toBe("8x");
    expect(screen.getByRole("button", { name: "速くする" }).hasAttribute("disabled")).toBe(true);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "遅くする" }));
    });
    expect(send).toHaveBeenCalledWith("cmd.game.speed", { speed: 4 });
  });

  it("reset() されたら「状況（準備中）」に戻る", () => {
    load();
    renderHud();
    expect(screen.queryByText("状況（準備中）")).toBeNull();
    act(() => {
      gameStore.getState().reset();
    });
    expect(screen.getByText("状況（準備中）")).toBeDefined();
  });
});

// 記録（web/src/net/fixtures の mini-record）をストアに入れて、状況とお知らせが描かれることを確かめる（T-422）。
describe("記録を入れたときの状況とお知らせ（T-422）", () => {
  it("HB-T422-1 数字の札・フェス準備度の帯・郵便局長の吹き出しが出る", () => {
    const { snapshot, events } = loadMiniRound();
    const list = events.filter((e) => e.seq <= 29);
    act(() => {
      gameStore.getState().applySnapshot(snapshot, performance.now());
      gameStore
        .getState()
        .applyEvents({ t: 30000, speed: 1, paused: false, endsAt: 60000 }, list, performance.now());
    });
    const view = render(
      <>
        <HudBar />
        <Notifications />
      </>,
    );
    for (const selector of [".hud-time", ".hud-score", ".hud-successes"]) {
      expect(view.container.querySelector(selector)?.classList.contains("hud-item")).toBe(true);
    }
    expect(view.container.querySelector(".hud-fest meter")).not.toBeNull();
    const notice = view.container.querySelector(".notices > .notice");
    expect(notice?.querySelector(".notice-face")?.getAttribute("aria-hidden")).toBe("true");
    expect(notice?.querySelector(".notice-body .notice-speaker")?.textContent).toMatch(/郵便局長$/);
  });
});
