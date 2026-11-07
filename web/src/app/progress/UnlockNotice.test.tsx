// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { StageCatalogEntry } from "../types.ts";
import { UnlockNotice } from "./UnlockNotice.tsx";

function entry(id: string, title: string): StageCatalogEntry {
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
    unlock: "stage1",
  };
}

afterEach(() => {
  cleanup();
});

describe("UnlockNotice", () => {
  it("UN-1 空なら何も出さない", () => {
    const { container } = render(<UnlockNotice stages={[]} />);

    expect(container.innerHTML).toBe("");
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("UN-1 1 件", () => {
    render(<UnlockNotice stages={[entry("stage2", "島から返事が来ない！")]} />);

    const status = screen.getByRole("status");
    expect(status.className).toBe("unlock-notice");
    expect(status.textContent).toBe("🎉「島から返事が来ない！」が遊べるようになりました！");
  });

  it("UN-1 2 件なら「、」でつなぐ", () => {
    render(
      <UnlockNotice
        stages={[entry("stage2", "島から返事が来ない！"), entry("stage3", "嵐の海")]}
      />,
    );

    expect(screen.getByRole("status").textContent).toBe(
      "🎉「島から返事が来ない！」、「嵐の海」が遊べるようになりました！",
    );
  });
});
