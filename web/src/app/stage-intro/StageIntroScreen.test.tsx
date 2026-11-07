// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { StageSummary } from "../types.ts";
import { StageIntroScreen } from "./StageIntroScreen.tsx";

const STAGE: StageSummary = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
  demo: false,
};

afterEach(() => {
  cleanup();
});

function departButton(): HTMLButtonElement {
  return screen.getByRole<HTMLButtonElement>("button", { name: "出航する" });
}

describe("StageIntroScreen", () => {
  it("SI-1 busy でなければ「出航する」で onDepart を呼ぶ", () => {
    const onDepart = vi.fn();
    render(<StageIntroScreen stage={STAGE} onDepart={onDepart} busy={false} />);

    expect(departButton().disabled).toBe(false);
    fireEvent.click(departButton());
    expect(onDepart).toHaveBeenCalledTimes(1);
  });

  it("SI-1 busy のとき「出航する」が disabled", () => {
    const onDepart = vi.fn();
    render(<StageIntroScreen stage={STAGE} onDepart={onDepart} busy />);

    expect(departButton().disabled).toBe(true);
    fireEvent.click(departButton());
    expect(onDepart).not.toHaveBeenCalled();
  });
});
