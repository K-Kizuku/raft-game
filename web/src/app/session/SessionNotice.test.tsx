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

  it("見た目（T-424）: 作成中と失敗を紙の札のクラスで分け、文言は変えない", () => {
    const { rerender } = render(
      <SessionNotice state={{ status: "pending" }} allowOffline onOffline={() => {}} />,
    );
    expect(screen.getByRole("status").className).toBe("session-notice session-notice-pending");

    rerender(
      <SessionNotice
        state={{ status: "error", kind: "budget_exhausted" }}
        allowOffline
        onOffline={() => {}}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert.className).toBe("session-notice session-notice-error");
    expect(alert.querySelector("p")?.textContent).toBe("本日の受付は終了しました。");
    expect(
      screen
        .getByRole("button", { name: "サーバーなしで進む（開発用）" })
        .classList.contains("btn"),
    ).toBe(true);
  });
});
