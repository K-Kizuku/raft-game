// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { Letter, LetterEntry } from "../../protocol/index.ts";
import { setShowRaftTerms } from "../terms/preference.ts";
import { LetterIcon } from "./LetterIcon.tsx";

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
});
