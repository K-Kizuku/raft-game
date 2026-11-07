// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { termsPreference } from "../../ui/terms/preference.ts";
import { App, type AppDeps } from "../App.tsx";
import { SettingsScreen } from "./SettingsScreen.tsx";

const SAVE_FAILED = "このブラウザには設定を保存できませんでした。ページを閉じると元に戻ります。";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    termsPreference.reload();
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function checkbox(): HTMLInputElement {
  const element = screen.getByRole("checkbox", { name: "Raft の用語を併記する" });
  if (!(element instanceof HTMLInputElement)) {
    throw new Error("checkbox is not an input");
  }
  return element;
}

function example(container: HTMLElement): string | null | undefined {
  return container.querySelector(".settings-terms-example")?.textContent;
}

describe("SettingsScreen", () => {
  it("SE-1 既定", () => {
    const { container } = render(<SettingsScreen />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("設定");
    expect(checkbox().checked).toBe(false);
    expect(checkbox().getAttribute("aria-describedby")).not.toBeNull();
    expect(example(container)).toBe("表示の例：代表・共和国で確定・記録したよ！");
    expect(
      screen.getByText("設定はこのブラウザに保存します（サーバーには送りません）。"),
    ).toBeDefined();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText("設定は準備中です。")).toBeNull();
    expect(screen.getByRole("link", { name: "タイトルへ" }).getAttribute("href")).toBe("/");
  });

  it("SE-1 チェックボックスが説明を指す", () => {
    render(<SettingsScreen />);
    const id = checkbox().getAttribute("aria-describedby") ?? "";
    expect(document.getElementById(id)?.textContent).toBe(
      "オンにすると、ゲームの言葉に Raft の用語を小さく添えます。",
    );
  });

  it("SE-2 切り替えて保存する", () => {
    const { container } = render(<SettingsScreen />);

    fireEvent.click(checkbox());
    expect(checkbox().checked).toBe(true);
    expect(example(container)).toBe(
      "表示の例：代表（Leader）・共和国で確定（Commit）・記録したよ！（MsgAppResp）",
    );
    expect(localStorage.getItem("raft-game.settings")).toBe('{"v":1,"showRaftTerms":true}');

    fireEvent.click(checkbox());
    expect(checkbox().checked).toBe(false);
    expect(example(container)).toBe("表示の例：代表・共和国で確定・記録したよ！");
    expect(localStorage.getItem("raft-game.settings")).toBe('{"v":1,"showRaftTerms":false}');
  });

  it("SE-3 保存した値で始まる", () => {
    localStorage.setItem("raft-game.settings", '{"v":1,"showRaftTerms":true}');
    act(() => {
      termsPreference.reload();
    });
    render(<SettingsScreen />);

    expect(checkbox().checked).toBe(true);
  });

  it("SE-4 保存できなかったことを表示する", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    const { container } = render(<SettingsScreen />);

    fireEvent.click(checkbox());
    expect(checkbox().checked).toBe(true);
    expect(example(container)).toBe(
      "表示の例：代表（Leader）・共和国で確定（Commit）・記録したよ！（MsgAppResp）",
    );
    expect(screen.getByRole("status").textContent).toBe(SAVE_FAILED);
  });

  it("SE-5 画面をまたいで効く", () => {
    window.history.replaceState(null, "", "/settings");
    const loadStages: AppDeps["loadStages"] = () => new Promise<never>(() => undefined);
    render(<App deps={{ loadStages }} />);

    fireEvent.click(checkbox());
    fireEvent.click(screen.getByRole("link", { name: "タイトルへ" }));
    fireEvent.click(screen.getByRole("link", { name: "遊び方" }));
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("遊び方");

    const text = (): string | null => screen.getByRole("list").textContent;
    expect(text()).toContain("手紙（Message）");
    expect(text()).toContain("確定（Commit）");

    act(() => {
      termsPreference.reload();
    });
    expect(text()).toContain("手紙（Message）");
    expect(text()).toContain("確定（Commit）");
  });
});
