// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { TitleScreen } from "./TitleScreen.tsx";

afterEach(() => {
  cleanup();
});

describe("TitleScreen", () => {
  it("TS-1 タイトル・一言の説明・メニューのリンク", () => {
    render(<TitleScreen />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("合意、乗船中。");
    expect(screen.getByText("嵐の島々に、手紙で合意を届けよう")).toBeDefined();
    const nav = screen.getByRole("navigation", { name: "メニュー" });
    expect(nav).toBeDefined();
    for (const [name, href] of [
      ["遊ぶ", "/stages"],
      ["遊び方", "/howto"],
      ["クレジット", "/credits"],
      ["設定", "/settings"],
    ] as const) {
      const link = screen.getByRole("link", { name });
      expect(link.getAttribute("href")).toBe(href);
      expect(link.textContent).toBe(name);
    }
    // 主ボタンは「遊ぶ」だけ
    expect(screen.getByRole("link", { name: "遊ぶ" }).classList.contains("btn-primary")).toBe(true);
    expect(
      screen.getAllByRole("link").filter((a) => a.classList.contains("btn-primary")),
    ).toHaveLength(1);
  });

  it("TS-2 タイトルの絵とメニューの絵は aria-hidden で、支援技術に読まれない", () => {
    const { container } = render(<TitleScreen />);

    const svgs = Array.from(container.querySelectorAll("svg"));
    expect(container.querySelector("svg.title-art")).not.toBeNull();
    expect(svgs.length).toBeGreaterThanOrEqual(5);
    for (const svg of svgs) {
      expect(svg.getAttribute("aria-hidden")).toBe("true");
      expect(svg.getAttribute("focusable")).toBe("false");
      // 文字を持たない（支援技術・本文に混ざらない）
      expect(svg.textContent).toBe("");
    }
    expect(screen.queryByRole("img")).toBeNull();
  });
});
