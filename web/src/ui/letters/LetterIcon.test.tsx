// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { Letter, LetterEntry } from "../../protocol/index.ts";
import { setShowRaftTerms } from "../terms/preference.ts";
import { tokens } from "../theme/tokens.ts";
import { LetterIcon } from "./LetterIcon.tsx";
import { LETTER_KINDS } from "./letterKinds.ts";

afterEach(() => {
  cleanup();
  setShowRaftTerms(false);
});

function letter(overrides: Partial<Letter>): Letter {
  return {
    id: 1,
    kind: "raft",
    from: 1,
    to: 2,
    createdAt: 1000,
    loc: { type: "port", islandId: 1 },
    label: "heartbeat",
    ...overrides,
  };
}

// WCAG 2 の相対輝度とコントラスト比（#RRGGBB）。
function relativeLuminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = channels.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
}

function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

function entry(index: number): LetterEntry {
  return { index, term: 1, type: "normal", summary: `e${index}` };
}

function raft(entries: LetterEntry[]): NonNullable<Letter["raft"]> {
  return {
    type: "MsgApp",
    term: 1,
    logTerm: 1,
    index: 1,
    commit: 0,
    entries,
    reject: false,
    rejectHint: 0,
    stale: false,
  };
}

describe("LetterIcon", () => {
  it("LI-1 名前を aria-label にし、併記がオンなら Msg* を添える", () => {
    render(<LetterIcon letter={letter({ label: "heartbeat" })} />);
    const icon = screen.getByRole("img", { name: "お元気ですか？" });
    expect(icon.getAttribute("data-shape")).toBe("circle");
    expect(icon.getAttribute("data-label")).toBe("heartbeat");
    expect(icon.getAttribute("data-mark")).toBe("none");
    expect(icon.querySelector("title")?.textContent).toBe("お元気ですか？");

    act(() => {
      setShowRaftTerms(true);
    });
    const shown = screen.getByRole("img", { name: "お元気ですか？（MsgHeartbeat）" });
    expect(shown.querySelector("title")?.textContent).toBe("お元気ですか？（MsgHeartbeat）");
    cleanup();

    render(<LetterIcon letter={letter({ kind: "request", label: "request" })} />);
    const request = screen.getByRole("img", { name: "住民の依頼書" });
    expect(request.getAttribute("data-shape")).toBe("star");
  });

  it("LI-2 append は記録の件数を出し、borderColor で縁の色", () => {
    const view = render(
      <LetterIcon
        letter={letter({ label: "append", raft: raft([entry(1), entry(2), entry(3)]) })}
        borderColor="#123456"
      />,
    );
    expect(view.container.querySelector('[data-part="count"]')?.textContent).toBe("3");
    expect(screen.getByText("3")).toBeDefined();
    const border = view.container.querySelector('[data-part="border"]');
    expect(border?.getAttribute("stroke")).toBe("#123456");
    cleanup();

    const noRaft = render(<LetterIcon letter={letter({ label: "append" })} />);
    expect(noRaft.container.querySelector('[data-part="count"]')).toBeNull();
    expect(noRaft.container.querySelector('[data-part="border"]')).toBeNull();
    cleanup();

    const empty = render(<LetterIcon letter={letter({ label: "append", raft: raft([]) })} />);
    expect(empty.container.querySelector('[data-part="count"]')).toBeNull();
    cleanup();

    const heartbeat = render(
      <LetterIcon letter={letter({ label: "heartbeat", raft: raft([entry(1)]) })} />,
    );
    expect(heartbeat.container.querySelector('[data-part="count"]')).toBeNull();
    expect(heartbeat.container.querySelector("text")).toBeNull();
  });

  it("知らない label は「その他の手紙」のひし形", () => {
    const unknown = letter({ label: "future_label" as Letter["label"] });
    render(<LetterIcon letter={unknown} />);
    const icon = screen.getByRole("img", { name: "その他の手紙" });
    expect(icon.getAttribute("data-shape")).toBe("diamond");
  });

  it("LI-3 縁の輪は borderColor の島の色そのまま、形の線は白・紙・パネルの地と 3:1 以上（WCAG 1.4.11）", () => {
    const labels = Object.keys(LETTER_KINDS) as Letter["label"][];
    for (const label of labels) {
      const view = render(<LetterIcon letter={letter({ label })} borderColor="#FFEE00" />);
      const svg = view.container.querySelector("svg");
      expect(svg?.querySelector('[data-part="border"]')?.getAttribute("stroke"), label).toBe(
        "#FFEE00",
      );
      // 形は title・縁の輪のあとの最初の要素。
      const shape = svg?.querySelector(":scope > :not(title):not([data-part])");
      const stroke = shape?.getAttribute("stroke") ?? "";
      expect(stroke, label).toMatch(/^#[0-9a-f]{6}$/);
      // 背景は白の塗り・縁取りの形の中の塗りと封筒（--color-paper）・パネルの地（--color-paper-deep）。
      for (const background of ["#ffffff", tokens["--color-paper"], tokens["--color-paper-deep"]]) {
        expect(
          contrastRatio(stroke, background),
          `${label} の線と ${background}`,
        ).toBeGreaterThanOrEqual(3);
      }
      cleanup();
    }
  });
});
