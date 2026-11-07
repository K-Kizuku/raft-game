// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setShowRaftTerms, termsPreference } from "../../ui/terms/preference.ts";
import { HowToScreen } from "./HowToScreen.tsx";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    termsPreference.reload();
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function items(): (string | null)[] {
  return screen.getAllByRole("listitem").map((item) => item.textContent);
}

describe("HowToScreen", () => {
  it("HT-1 オフでは T-401 の文言のまま", () => {
    render(<HowToScreen />);
    expect(items()).toEqual([
      "島の港には、ほかの島あての手紙がたまります。",
      "船に手紙を積み、行き先を決めて出航しましょう。",
      "お祭りの準備（花火や屋台の予約）を、島のみんなで確定させると得点になります。",
    ]);
  });

  it("HT-2 オンでは「手紙」「確定」に併記する", () => {
    act(() => {
      setShowRaftTerms(true);
    });
    render(<HowToScreen />);
    expect(items()).toEqual([
      "島の港には、ほかの島あての手紙（Message）がたまります。",
      "船に手紙を積み、行き先を決めて出航しましょう。",
      "お祭りの準備（花火や屋台の予約）を、島のみんなで確定（Commit）させると得点になります。",
    ]);
  });
});
