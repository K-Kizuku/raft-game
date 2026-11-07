// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SessionNotice } from "./SessionNotice.tsx";

afterEach(() => {
  cleanup();
});

describe("SessionNotice", () => {
  it("idle は何も出さない", () => {
    const { container } = render(
      <SessionNotice state={{ status: "idle" }} allowOffline onOffline={() => {}} />,
    );
    expect(container.innerHTML).toBe("");
  });

  it("pending は「ステージを準備しています…」", () => {
    render(<SessionNotice state={{ status: "pending" }} allowOffline onOffline={() => {}} />);
    expect(screen.getByRole("status").textContent).toBe("ステージを準備しています…");
  });

  it("error は文言と、allowOffline のときだけ開発用のボタン", () => {
    const onOffline = vi.fn();
    const { rerender } = render(
      <SessionNotice
        state={{ status: "error", kind: "budget_exhausted" }}
        allowOffline
        onOffline={onOffline}
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain("本日の受付は終了しました。");
    fireEvent.click(screen.getByRole("button", { name: "サーバーなしで進む（開発用）" }));
    expect(onOffline).toHaveBeenCalledTimes(1);

    rerender(
      <SessionNotice
        state={{ status: "error", kind: "budget_exhausted" }}
        allowOffline={false}
        onOffline={onOffline}
      />,
    );
    expect(screen.getByRole("alert")).toBeDefined();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
