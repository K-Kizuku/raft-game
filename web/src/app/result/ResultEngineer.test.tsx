// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { gameStore } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { ResultEngineer } from "./ResultEngineer.tsx";

function loadRound(): void {
  const { snapshot, events } = loadMiniRound();
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
    gameStore
      .getState()
      .applyEvents({ t: 60000, speed: 1, paused: false, endsAt: 60000 }, events, 1);
  });
}

function toggle(): HTMLElement {
  return screen.getByRole("button", { name: "E エンジニアモード" });
}

function heading(): HTMLElement | null {
  return screen.queryByRole("heading", { name: "タイムライン" });
}

const originalScroll = Element.prototype.scrollIntoView;

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  Element.prototype.scrollIntoView = originalScroll;
});

describe("ResultEngineer", () => {
  it("RE-1 ボタンで開閉する", () => {
    loadRound();
    render(<ResultEngineer />);
    const button = toggle();
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(button.getAttribute("aria-controls")).toBe("result-engineer");
    expect(button.getAttribute("aria-keyshortcuts")).toBe("E");
    expect(heading()).toBeNull();

    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(heading()).not.toBeNull();
    const section = screen.getByRole("region", { name: "タイムライン" });
    expect(section.id).toBe("result-engineer");
    expect(
      screen.getByText(
        "代表（Leader）の任期（Term）・交代と、共和国で確定した記録の番号（Commit Index）の移り変わりです。",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("img", { name: "Term・Leader・Commit のタイムライン" })).toBeTruthy();
    expect(screen.getByRole("list", { name: "出来事の一覧" }).textContent).toContain(
      "一の島 が Leader になった（Term 2）",
    );
    expect(screen.queryByText("古い記録は省きました。")).toBeNull();

    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(heading()).toBeNull();
  });

  it("RE-1 記録が無ければ「記録がありません。」", () => {
    render(<ResultEngineer />);
    fireEvent.click(toggle());
    expect(screen.getByText("記録がありません。")).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("RE-2 E キーで開閉する（修飾キー・押しっぱなし・変換中・入力欄では切り替えない）", () => {
    loadRound();
    render(
      <>
        <input aria-label="入力" />
        <ResultEngineer />
      </>,
    );
    fireEvent.keyDown(window, { key: "e" });
    expect(heading()).not.toBeNull();
    fireEvent.keyDown(window, { key: "E" });
    expect(heading()).toBeNull();

    fireEvent.keyDown(window, { key: "e", ctrlKey: true });
    fireEvent.keyDown(window, { key: "e", repeat: true });
    fireEvent.keyDown(window, { key: "e", isComposing: true });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "入力" }), { key: "e" });
    expect(heading()).toBeNull();
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
  });

  it("RE-3 アンマウントの後の keydown で何も起きない", () => {
    const { unmount } = render(<ResultEngineer />);
    unmount();
    expect(() => fireEvent.keyDown(window, { key: "e" })).not.toThrow();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("RE-4 scrollIntoView が無い環境でも開ける。ある環境では開いたときに 1 回呼ぶ", () => {
    // jsdom には scrollIntoView が無い
    Element.prototype.scrollIntoView = undefined as unknown as typeof originalScroll;
    const { unmount } = render(<ResultEngineer />);
    fireEvent.click(toggle());
    expect(heading()).not.toBeNull();
    unmount();

    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(<ResultEngineer />);
    expect(scroll).not.toHaveBeenCalled();
    fireEvent.click(toggle());
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll.mock.calls[0]?.[0]).toMatchObject({ block: "start" });
    expect(document.activeElement).not.toBe(screen.getByRole("region", { name: "タイムライン" }));
  });
});
