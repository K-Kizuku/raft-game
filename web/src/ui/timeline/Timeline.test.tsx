// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { EMPTY_TIMELINE } from "../../state/index.ts";
import { Timeline } from "./Timeline.tsx";
import { buildTimelineView, type TimelineView } from "./timelineView.ts";

const COLORS: Record<number, string> = { 1: "rgb(1, 1, 1)", 2: "rgb(2, 2, 2)", 3: "rgb(3, 3, 3)" };
const termColor = (term: number): string => COLORS[term] ?? "rgb(0, 0, 0)";

function sampleView(): TimelineView {
  return buildTimelineView({
    timeline: {
      ...EMPTY_TIMELINE,
      sessionId: "s1",
      baseTerm: 1,
      maxTerm: 3,
      maxCommit: 2,
      items: [
        { kind: "elected", seq: 1, t: 0, id: 1, term: 1 },
        { kind: "commit", seq: 2, t: 5000, index: 1 },
        { kind: "term", seq: 3, t: 30000, term: 2, id: 2 },
        { kind: "stepped_down", seq: 4, t: 35000, id: 1, term: 1, reason: "check_quorum" },
        { kind: "resync", seq: 9, t: 50000, initial: false, leaders: [{ id: 2, term: 3 }] },
        { kind: "term", seq: 9, t: 50000, term: 3, id: 2 },
        { kind: "commit", seq: 9, t: 50000, index: 2 },
      ],
    },
    endT: 120000,
    islands: [
      { id: 1, name: "一の島", color: "rgb(10, 0, 0)" },
      { id: 2, name: "とても長い名前の島です", color: "rgb(0, 10, 0)" },
    ],
  });
}

afterEach(() => {
  cleanup();
});

describe("Timeline", () => {
  it("TC-1 図と一覧（各行に m:ss の時刻）", () => {
    const view = sampleView();
    render(<Timeline view={view} termColor={termColor} />);
    const figure = screen.getByRole("img", { name: "Term・Leader・Commit のタイムライン" });
    expect(figure.tagName.toLowerCase()).toBe("svg");
    const items = within(screen.getByRole("list", { name: "出来事の一覧" })).getAllByRole(
      "listitem",
    );
    expect(items).toHaveLength(view.lines.length);
    for (const item of items) {
      expect(item.querySelector("time")?.textContent).toMatch(/^\d+:\d{2}$/);
    }
    expect(items[0]?.textContent).toContain("0:00");
    expect(items[0]?.textContent).toContain("一の島 が Leader になった（Term 1）");
    // つなぎ直しの印・open の区間の点線の縁・長い名前の省略と title
    expect(figure.querySelectorAll(".timeline-resync")).toHaveLength(1);
    expect(figure.querySelectorAll(".timeline-open-edge")).toHaveLength(1);
    const longRow = figure.querySelector('[data-island="2"]');
    expect(longRow?.querySelector("title")?.textContent).toBe("とても長い名前の島です");
    expect(longRow?.querySelector("text")?.textContent).toBe("とても長い名…");
    expect(figure.querySelectorAll(".timeline-commit-point")).toHaveLength(2);
  });

  it("TC-2 lines が空なら「記録がありません。」だけ", () => {
    const view = buildTimelineView({ timeline: EMPTY_TIMELINE, endT: 60000, islands: [] });
    const { container } = render(<Timeline view={view} termColor={termColor} />);
    expect(screen.getByText("記録がありません。")).toBeTruthy();
    expect(container.querySelector("svg")).toBeNull();
    expect(screen.queryByRole("list")).toBeNull();
  });

  it("TC-3 Term の帯の fill が termColor(term) の値", () => {
    const view = sampleView();
    const { container } = render(<Timeline view={view} termColor={termColor} />);
    const bands = [...container.querySelectorAll(".timeline-term-band")];
    expect(bands.map((band) => band.getAttribute("data-term"))).toEqual(["1", "2", "3"]);
    for (const band of bands) {
      expect(band.getAttribute("fill")).toBe(termColor(Number(band.getAttribute("data-term"))));
    }
  });
});
