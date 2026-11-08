// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SOUND_STORAGE_KEY, soundPreference } from "../../ui/sound/preference.ts";
import { termsPreference } from "../../ui/terms/preference.ts";
import { App, type AppDeps } from "../App.tsx";
import { stageProgress } from "../progress/progress.ts";
import { SettingsScreen } from "./SettingsScreen.tsx";

const SAVE_FAILED = "このブラウザには設定を保存できませんでした。ページを閉じると元に戻ります。";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    termsPreference.reload();
    stageProgress.reload();
    soundPreference.reload();
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

describe("SettingsScreen のステージの進み具合（T-703）", () => {
  const CONFIRM = "本当に消しますか？（ステージ選択のクリア済みの印が消えます）";
  const PROGRESS_SAVE_FAILED =
    "このブラウザには進み具合を保存できませんでした。ページを閉じると元に戻ります。";

  function button(name: string): HTMLButtonElement {
    return screen.getByRole<HTMLButtonElement>("button", { name });
  }

  it("SE-U1 件数を出し、「進み具合を消す」→「消す」で消える", () => {
    act(() => {
      stageProgress.recordResult("stage1", 2);
    });
    render(<SettingsScreen />);

    expect(screen.getByRole("heading", { level: 2, name: "ステージの進み具合" })).toBeDefined();
    expect(screen.getByText("クリアしたステージ: 1")).toBeDefined();
    expect(screen.queryByText(CONFIRM)).toBeNull();

    fireEvent.click(button("進み具合を消す"));
    expect(screen.getByText(CONFIRM)).toBeDefined();
    expect(screen.queryByRole("button", { name: "進み具合を消す" })).toBeNull();

    fireEvent.click(button("消す"));
    expect(screen.getByText("クリアしたステージ: 0")).toBeDefined();
    expect(screen.getByRole("status").textContent).toBe("進み具合を消しました。");
    expect(screen.queryByText(CONFIRM)).toBeNull();
    expect(button("進み具合を消す").disabled).toBe(true);
    expect(localStorage.getItem("raft-game.progress")).toBe('{"v":1,"bestStars":{}}');
    expect(stageProgress.getSnapshot().bestStars).toEqual({});
  });

  it("SE-U1 「やめる」では消えない", () => {
    act(() => {
      stageProgress.recordResult("stage1", 2);
    });
    render(<SettingsScreen />);

    fireEvent.click(button("進み具合を消す"));
    fireEvent.click(button("やめる"));
    expect(screen.queryByText(CONFIRM)).toBeNull();
    expect(screen.getByText("クリアしたステージ: 1")).toBeDefined();
    expect(button("進み具合を消す").disabled).toBe(false);
    expect(screen.queryByRole("status")).toBeNull();
    expect(stageProgress.getSnapshot().bestStars).toEqual({ stage1: 2 });
  });

  it("SE-U2 0 件なら「進み具合を消す」が押せない", () => {
    render(<SettingsScreen />);

    expect(screen.getByText("クリアしたステージ: 0")).toBeDefined();
    expect(button("進み具合を消す").disabled).toBe(true);
    expect(screen.queryByText(PROGRESS_SAVE_FAILED)).toBeNull();
  });

  it("SE-U1 localStorage が使えなくても例外を出さずに画面の上では消え、注記を出す", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    act(() => {
      stageProgress.reload();
      stageProgress.recordResult("stage1", 1);
    });
    render(<SettingsScreen />);

    expect(screen.getByText("クリアしたステージ: 1")).toBeDefined();
    expect(screen.getByText(PROGRESS_SAVE_FAILED)).toBeDefined();
    fireEvent.click(button("進み具合を消す"));
    fireEvent.click(button("消す"));
    expect(screen.getByText("クリアしたステージ: 0")).toBeDefined();
    expect(screen.getByRole("status").textContent).toBe("進み具合を消しました。");
  });
});

describe("SettingsScreen の効果音（T-434）", () => {
  const SOUND_SAVE_FAILED =
    "このブラウザには効果音の設定を保存できませんでした。ページを閉じると元に戻ります。";

  function soundCheckbox(): HTMLInputElement {
    return screen.getByRole<HTMLInputElement>("checkbox", { name: "効果音を鳴らす" });
  }

  it("SE-S1 既定はオンで、切り替えるとブラウザに保存する", () => {
    render(<SettingsScreen />);

    expect(soundCheckbox().checked).toBe(true);
    const id = soundCheckbox().getAttribute("aria-describedby") ?? "";
    expect(document.getElementById(id)?.textContent).toContain(
      "出航・到着・配達・依頼の確定・嵐の予報",
    );

    fireEvent.click(soundCheckbox());
    expect(soundCheckbox().checked).toBe(false);
    expect(localStorage.getItem(SOUND_STORAGE_KEY)).toBe('{"v":1,"enabled":false}');
    // 用語の設定の値は変えない
    expect(localStorage.getItem("raft-game.settings")).toBeNull();

    fireEvent.click(soundCheckbox());
    expect(soundCheckbox().checked).toBe(true);
    expect(localStorage.getItem(SOUND_STORAGE_KEY)).toBe('{"v":1,"enabled":true}');
    expect(screen.queryByText(SOUND_SAVE_FAILED)).toBeNull();
  });

  it("SE-S2 保存した値で始まる", () => {
    localStorage.setItem(SOUND_STORAGE_KEY, '{"v":1,"enabled":false}');
    act(() => {
      soundPreference.reload();
    });
    render(<SettingsScreen />);

    expect(soundCheckbox().checked).toBe(false);
  });

  it("SE-S3 保存できなかったことを表示する", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    render(<SettingsScreen />);

    fireEvent.click(soundCheckbox());
    expect(soundCheckbox().checked).toBe(false);
    expect(screen.getByText(SOUND_SAVE_FAILED)).toBeDefined();
  });
});
