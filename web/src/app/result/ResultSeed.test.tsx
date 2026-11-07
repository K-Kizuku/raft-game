// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { gameStore } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { ResultSeed } from "./ResultSeed.tsx";

const COPIED = "コピーしました。";
const COPY_FAILED = "コピーできませんでした。URL を選んでコピーしてください。";

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");

function loadWorld(seed: number): void {
  const { snapshot } = loadMiniRound();
  snapshot.session = { ...snapshot.session, seed };
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function setClipboard(value: unknown): void {
  Object.defineProperty(navigator, "clipboard", { value, configurable: true });
}

function expectedUrl(stageId: string, seed: number): string {
  return `${window.location.origin}/stages/${stageId}?seed=${String(seed)}`;
}

function copyButton(): HTMLElement {
  return screen.getByRole("button", { name: "URL をコピー" });
}

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  if (originalClipboard === undefined) {
    Reflect.deleteProperty(navigator, "clipboard");
  } else {
    Object.defineProperty(navigator, "clipboard", originalClipboard);
  }
});

describe("ResultSeed", () => {
  it("RD-1 シードと説明と、このシードで遊ぶ URL を出す", () => {
    loadWorld(42);
    const { container } = render(<ResultSeed stageId="stage1" />);

    const section = screen.getByRole("region", { name: "同じ条件で遊ぶ" });
    expect(section.classList.contains("result-section")).toBe(true);
    expect(section.classList.contains("result-seed")).toBe(true);
    expect(screen.getByText(/このラウンドのシード:/).textContent).toBe("このラウンドのシード: 42");
    expect(container.querySelector(".result-seed-value")?.textContent).toBe("42");
    expect(
      screen.getByText(
        "同じシードなら、依頼と嵐は同じ時刻に起きます。結果が変わるのは船の動かし方だけです。「同じ条件でもう一度」は、このシードで始めます。",
      ),
    ).toBeDefined();
    expect(container.querySelector("code")?.textContent).toBe(expectedUrl("stage1", 42));
    expect(screen.getByText(/このシードで遊ぶ URL:/).textContent).toContain(
      expectedUrl("stage1", 42),
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("RD-1 シード 0 も出す", () => {
    loadWorld(0);
    const { container } = render(<ResultSeed stageId="demo-izu" />);
    expect(container.querySelector(".result-seed-value")?.textContent).toBe("0");
    expect(container.querySelector("code")?.textContent).toBe(expectedUrl("demo-izu", 0));
  });

  it("RD-2 world が null なら何も描かない", () => {
    const { container } = render(<ResultSeed stageId="stage1" />);
    expect(container.innerHTML).toBe("");
  });

  it("RD-3 「URL をコピー」で URL を書き込み、「コピーしました。」", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
    setClipboard({ writeText });
    loadWorld(42);
    render(<ResultSeed stageId="stage1" />);

    fireEvent.click(copyButton());
    expect((await screen.findByRole("status")).textContent).toBe(COPIED);
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(expectedUrl("stage1", 42));

    // 押すたびに文を置き直す。
    const first = screen.getByRole("status");
    fireEvent.click(copyButton());
    await waitFor(() => {
      expect(screen.getByRole("status")).not.toBe(first);
    });
    expect(screen.getByRole("status").textContent).toBe(COPIED);
    expect(writeText).toHaveBeenCalledTimes(2);
  });

  it("RD-3 clipboard が無ければ失敗の文", () => {
    setClipboard(undefined);
    loadWorld(42);
    render(<ResultSeed stageId="stage1" />);

    fireEvent.click(copyButton());
    expect(screen.getByRole("status").textContent).toBe(COPY_FAILED);
  });

  it("RD-3 writeText が無ければ失敗の文", () => {
    setClipboard({});
    loadWorld(42);
    render(<ResultSeed stageId="stage1" />);

    fireEvent.click(copyButton());
    expect(screen.getByRole("status").textContent).toBe(COPY_FAILED);
  });

  it("RD-3 writeText が拒否すると失敗の文", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>(async () => {
      throw new DOMException("denied", "NotAllowedError");
    });
    setClipboard({ writeText });
    loadWorld(42);
    render(<ResultSeed stageId="stage1" />);

    fireEvent.click(copyButton());
    expect((await screen.findByRole("status")).textContent).toBe(COPY_FAILED);
    expect(writeText).toHaveBeenCalledWith(expectedUrl("stage1", 42));
  });
});
