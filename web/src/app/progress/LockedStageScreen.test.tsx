// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { StageCatalogEntry } from "../types.ts";
import { LockedStageScreen } from "./LockedStageScreen.tsx";

function entry(id: string, title: string, unlock: string | null): StageCatalogEntry {
  return {
    id,
    title,
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    demo: false,
    intro: { text: "紹介文", provenance: "creative", source: null },
    learning: [],
    unlock,
  };
}

afterEach(() => {
  cleanup();
});

describe("LockedStageScreen", () => {
  it("LS-1 見出し・条件の文・2 つのリンク", () => {
    const requires = entry("stage1", "はじめての郵便船", null);
    const stage = entry("stage2", "島から返事が来ない！", "stage1");
    render(<LockedStageScreen stage={stage} requires={requires} />);

    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.textContent).toBe("このステージはまだ遊べません。");
    expect(heading.getAttribute("tabindex")).toBe("-1");
    expect(
      screen.getByText(
        "「島から返事が来ない！」は、「はじめての郵便船」を★1 以上でクリアすると遊べます。",
      ),
    ).toBeDefined();
    expect(screen.getByRole("link", { name: "「はじめての郵便船」へ" }).getAttribute("href")).toBe(
      "/stages/stage1",
    );
    expect(screen.getByRole("link", { name: "ステージ選択へ" }).getAttribute("href")).toBe(
      "/stages",
    );
  });
});
