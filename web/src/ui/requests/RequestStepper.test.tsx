// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { setShowRaftTerms } from "../terms/preference.ts";
import { RequestStepper } from "./RequestStepper.tsx";

afterEach(() => {
  cleanup();
  setShowRaftTerms(false);
});

function steps(): { step: string | null; state: string | null; current: string | null }[] {
  return [...document.querySelectorAll(".request-step")].map((li) => ({
    step: li.getAttribute("data-step"),
    state: li.getAttribute("data-state"),
    current: li.getAttribute("aria-current"),
  }));
}

describe("RequestStepper", () => {
  it("RS-1 5 つの丸・いまの段階（aria-current）・止まっている理由", () => {
    render(<RequestStepper status="proposed" />);
    expect(screen.getByRole("list", { name: "依頼の進み具合" })).toBeDefined();
    expect(steps()).toEqual([
      { step: "received", state: "done", current: null },
      { step: "propose", state: "done", current: null },
      { step: "append", state: "current", current: "step" },
      { step: "commit", state: "todo", current: null },
      { step: "reply", state: "todo", current: null },
    ]);
    expect(document.querySelector(".request-progress-reason")?.textContent).toBe(
      "島々の記録と、代表への返事を待っています",
    );
    expect(document.querySelector(".request-stepper")?.textContent).toBe(
      "✓受付（済み）✓提案（済み）●記録（いまここ）確定（まだ）返事（まだ）",
    );
  });

  it("RS-2 用語の併記がオンなら、提案・記録・確定に Raft の用語を添える", () => {
    setShowRaftTerms(true);
    render(<RequestStepper status="committed" />);
    const labels = [...document.querySelectorAll(".request-step-label")].map(
      (el) => el.textContent,
    );
    expect(labels).toEqual(["受付", "提案（Propose）", "記録（Append）", "確定（Commit）", "返事"]);
  });

  it("RS-3 予約失敗は返事の丸を ✕ にする。compact では名前を読み上げだけにする", () => {
    render(<RequestStepper status="rejected" compact />);
    const progress = document.querySelector(".request-progress");
    expect(progress?.className).toBe("request-progress is-compact");
    expect(progress?.getAttribute("data-outcome")).toBe("rejected");
    expect(steps().map((s) => s.state)).toEqual(["done", "done", "done", "done", "failed"]);
    expect(document.querySelector('[data-step="reply"] .request-step-dot')?.textContent).toBe("✕");
    expect(document.querySelectorAll(".request-step-label")).toHaveLength(0);
    expect(document.querySelector(".request-stepper")?.textContent).toContain("返事（失敗）");
  });
});
