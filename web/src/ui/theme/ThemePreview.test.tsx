// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "../../app/App.tsx";
import { ThemePreview } from "./ThemePreview.tsx";
import { tokens } from "./tokens.ts";

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

describe("ThemePreview", () => {
  it("色・文字・ボタン・パネル・吹き出しの見本を並べる", () => {
    render(<ThemePreview />);
    expect(screen.getByRole("heading", { level: 1, name: "デザインの見本" })).toBeTruthy();
    for (const name of ["色", "文字", "ボタン", "パネルと通知の吹き出し", "余白と角丸"]) {
      expect(screen.getByRole("heading", { level: 2, name })).toBeTruthy();
    }
    expect(screen.getByText("--color-postal")).toBeTruthy();
    expect(screen.getAllByText(tokens["--color-postal"]).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "出航する" }).className).toContain("btn-primary");
    expect(screen.getByText("花火の予約、まだかなあ").className).toContain("speech-bubble");
  });
});

describe("App の ?theme-preview（開発のときだけ）", () => {
  it("?theme-preview で見本の画面を開く", async () => {
    window.history.replaceState(null, "", "/?theme-preview");
    const loadStages = vi.fn(async () => []);
    render(<App deps={{ loadStages }} />);
    expect(await screen.findByRole("heading", { level: 1, name: "デザインの見本" })).toBeTruthy();
    expect(loadStages).not.toHaveBeenCalled();
  });

  it("?theme-preview がなければ、ふだんの画面を出す", () => {
    render(<App deps={{ loadStages: vi.fn(async () => []) }} />);
    expect(screen.queryByRole("heading", { name: "デザインの見本" })).toBeNull();
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy();
  });
});
