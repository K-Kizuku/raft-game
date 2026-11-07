// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { StageSummary } from "../types.ts";
import { StageSelectScreen } from "./StageSelectScreen.tsx";

const STAGES: StageSummary[] = [
  {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    demo: false,
  },
  {
    id: "demo-izu",
    title: "伊豆の島じま",
    region: "伊豆諸島",
    islands: 5,
    ferries: 2,
    durationMs: 210000,
    demo: true,
  },
];

afterEach(() => {
  cleanup();
});

function buttons(): HTMLButtonElement[] {
  return STAGES.map((stage) =>
    screen.getByRole<HTMLButtonElement>("button", { name: `${stage.title}を選ぶ` }),
  );
}

describe("StageSelectScreen", () => {
  it("SS-1 「<タイトル>を選ぶ」で onSelect にそのステージを渡す", () => {
    const onSelect = vi.fn();
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages: STAGES }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy={false}
      />,
    );

    for (const button of buttons()) {
      expect(button.disabled).toBe(false);
    }
    fireEvent.click(screen.getByRole("button", { name: "伊豆の島じまを選ぶ" }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(STAGES[1]);
  });

  it("SS-1 busy のとき、すべての「<タイトル>を選ぶ」が disabled", () => {
    const onSelect = vi.fn();
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages: STAGES }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy
      />,
    );

    for (const button of buttons()) {
      expect(button.disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(onSelect).not.toHaveBeenCalled();
  });
});
