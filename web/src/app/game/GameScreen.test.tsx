// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { RoundContext } from "../types.ts";
import { GameScreen } from "./GameScreen.tsx";

afterEach(() => {
  cleanup();
});

const round: RoundContext = {
  stage: {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
  },
  session: null,
  finished: false,
};

describe("GameScreen", () => {
  it("GM-1 領域とスロットを置き、<h1> がステージのタイトル", () => {
    render(<GameScreen round={round} onRoundEnd={() => {}} />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("はじめての郵便船");
    for (const name of ["状況", "マップ", "フェリー操作"]) {
      const region = screen.getByRole("region", { name });
      expect(within(region).getByText(`${name}（準備中）`)).toBeDefined();
    }
    const island = screen.getByRole("complementary", { name: "島の詳細" });
    expect(within(island).getByText("島の詳細（準備中）")).toBeDefined();
    const notifications = screen.getByRole("region", { name: "お知らせ" });
    expect(notifications.getAttribute("aria-live")).toBe("polite");
  });

  it("GM-2 session が null なら「リザルトへ（仮）」で onRoundEnd を呼ぶ", () => {
    const onRoundEnd = vi.fn();
    render(<GameScreen round={round} onRoundEnd={onRoundEnd} />);

    expect(screen.getByText(/サーバーに接続していません/)).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "リザルトへ（仮）" }));
    expect(onRoundEnd).toHaveBeenCalledTimes(1);
  });

  it("GM-2 session があるときは仮のボタンと文言を出さない", () => {
    render(
      <GameScreen
        round={{ ...round, session: { sessionId: "s-1", token: "t-1", wsPath: "/ws/s-1" } }}
        onRoundEnd={() => {}}
      />,
    );

    expect(screen.queryByRole("button", { name: "リザルトへ（仮）" })).toBeNull();
    expect(screen.queryByText(/サーバーに接続していません/)).toBeNull();
  });
});
