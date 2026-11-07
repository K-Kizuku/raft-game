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

const SECTIONS = ["目的", "画面の見方", "操作", "代表と確定", "嵐", "キー操作"];

function headings(): (string | null)[] {
  return screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
}

function section(title: string): HTMLElement {
  const heading = screen.getByRole("heading", { level: 2, name: title });
  const item = heading.closest("li");
  if (item === null) throw new Error(`${title} の節が無い`);
  return item;
}

describe("HowToScreen", () => {
  it("HT-1 6 つの節の見出しが順に出て、「準備中」の文がない", () => {
    const { container } = render(<HowToScreen />);
    expect(headings()).toEqual(SECTIONS);
    expect(container.textContent).not.toContain("準備中");
    // 節ごとに絵が 1 つある（T-424a の部品）
    for (const title of SECTIONS) {
      expect(section(title).querySelector(".howto-step-icon")).not.toBeNull();
    }
  });

  it("HT-2 文言が画面の操作と合っている", () => {
    render(<HowToScreen />);
    expect(section("目的").textContent).toContain("★2: ★1 に加えて、花火の予約がすべて確定する");
    const view = section("画面の見方").textContent;
    for (const word of [
      "残り時間",
      "スコア",
      "フェス準備度",
      "マップ",
      "フェリーの操作パネル",
      "島の詳細",
      "郵便局長",
    ]) {
      expect(view).toContain(word);
    }
    const control = section("操作").textContent;
    for (const word of ["船を選び", "積み", "行き先", "「出航」", "「おまかせ」"]) {
      expect(control).toContain(word);
    }
    const keys = Array.from(section("キー操作").querySelectorAll("dt kbd")).map(
      (k) => k.textContent,
    );
    expect(keys).toEqual(["Space", "1", "4", "E"]);
    expect(section("キー操作").textContent).toContain("エンジニアモード");
  });

  it("HT-3 オフでは Raft の用語を出さない", () => {
    const { container } = render(<HowToScreen />);
    expect(container.querySelector("[data-raft-term]")).toBeNull();
    const leader = section("代表と確定").textContent;
    expect(leader).toContain("代表の島（👑）");
    expect(leader).toContain("「共和国で確定」");
    expect(leader).not.toContain("Leader");
    expect(section("嵐").textContent).not.toContain("Partition");
  });

  it("HT-4 オンでは Raft の用語を併記する", () => {
    act(() => {
      setShowRaftTerms(true);
    });
    render(<HowToScreen />);
    const leader = section("代表と確定").textContent;
    expect(leader).toContain("代表（Leader）");
    expect(leader).toContain("共和国の台帳（Log）");
    expect(leader).toContain("過半数（Quorum）");
    expect(leader).toContain("共和国で確定（Commit）");
    const storm = section("嵐").textContent;
    expect(storm).toContain("嵐（Network Partition）");
    expect(storm).toContain("定期便（Heartbeat）");
    expect(storm).toContain("立候補（Candidate）");
    expect(storm).toContain("票（Vote）");
    expect(section("目的").textContent).toContain("手紙（Message）");
    // 見出しには併記しない
    expect(headings()).toEqual(SECTIONS);
  });

  it("HT-K1 「キー操作」の見出しと、Space・1〜4・E の説明がある", () => {
    render(<HowToScreen />);
    const heading = screen.getByRole("heading", { level: 2, name: "キー操作" });
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
