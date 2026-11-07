// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setShowRaftTerms, termsPreference } from "../../ui/terms/preference.ts";
import { HowToScreen } from "./HowToScreen.tsx";

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

function items(): (string | null)[] {
  return screen.getAllByRole("listitem").map((item) => item.textContent);
}

describe("HowToScreen", () => {
  it("HT-1 オフでは T-401 の文言のまま", () => {
    render(<HowToScreen />);
    expect(items()).toEqual([
      "島の港には、ほかの島あての手紙がたまります。",
      "船に手紙を積み、行き先を決めて出航しましょう。",
      "お祭りの準備（花火や屋台の予約）を、島のみんなで確定させると得点になります。",
    ]);
  });

  it("HT-2 オンでは「手紙」「確定」に併記する", () => {
    act(() => {
      setShowRaftTerms(true);
    });
    render(<HowToScreen />);
    expect(items()).toEqual([
      "島の港には、ほかの島あての手紙（Message）がたまります。",
      "船に手紙を積み、行き先を決めて出航しましょう。",
      "お祭りの準備（花火や屋台の予約）を、島のみんなで確定（Commit）させると得点になります。",
    ]);
  });

  it("HT-K1 「キーボード操作」の見出しと、Space・1〜4・E の説明がある", () => {
    render(<HowToScreen />);
    const heading = screen.getByRole("heading", { level: 2, name: "キーボード操作" });
    const list = heading.nextElementSibling;
    expect(list?.tagName).toBe("DL");
    const terms = Array.from(list?.querySelectorAll("dt") ?? []).map((dt) => dt.textContent);
    const details = Array.from(list?.querySelectorAll("dd") ?? []).map((dd) => dd.textContent);
    expect(terms).toEqual(["Space", "1〜4", "E"]);
    expect(details).toEqual([
      "一時停止・再開（ボタンにフォーカスがあるときは、そのボタンを押します）",
      "船を選ぶ（フェリー操作パネルの左から順）",
      "エンジニアモードを開く・閉じる",
    ]);
    const keys = Array.from(list?.querySelectorAll("kbd") ?? []).map((kbd) => kbd.textContent);
    expect(keys).toEqual(["Space", "1", "4", "E"]);
    expect(screen.queryByText("D")).toBeNull();
  });
});
