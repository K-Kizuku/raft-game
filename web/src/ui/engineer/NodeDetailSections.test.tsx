// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { LogEntry, NodeDetail } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { ENGINEER_LOG_LIMIT } from "./nodeDetailView.ts";
import { ProgressDetailSection, RaftLogSection } from "./NodeDetailSections.tsx";

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

function loadWorld(): World {
  act(() => {
    gameStore.getState().applySnapshot(loadMiniRound().snapshot, 0);
  });
  const world = gameStore.getState().world;
  if (world === null) throw new Error("world が無い");
  return world;
}

function entry(
  index: number,
  committed: boolean,
  summary = `deliver r-${String(index)}`,
): LogEntry {
  return { index, term: 2, type: "normal", summary, committed };
}

function detail(overrides: Partial<NodeDetail> = {}): NodeDetail {
  return {
    id: 2,
    role: "follower",
    term: 2,
    vote: 1,
    lead: 1,
    commit: 6,
    applied: 6,
    lastIndex: 7,
    log: [entry(5, true), entry(6, true), entry(7, false)],
    ledger: {},
    electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
    ...overrides,
  };
}

function setDetail(d: NodeDetail | null): void {
  act(() => {
    gameStore.getState().setNodeDetail(d);
  });
}

function bodyRows(table: HTMLElement): HTMLTableRowElement[] {
  return [...table.querySelectorAll<HTMLTableRowElement>("tbody tr")];
}

function cells(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].map((td) => td.textContent);
}

const NOT_YET = "ノードの詳細はまだ届いていません（サーバーにつながっているときだけ表示します）。";

describe("RaftLogSection・ProgressDetailSection", () => {
  it("ND-1 詳細が無ければ、その旨の文だけ", () => {
    const world = loadWorld();
    const view = render(<RaftLogSection world={world} targetId={2} />);
    expect(screen.getByRole("heading", { name: "Raft ログ" })).not.toBeNull();
    expect(screen.getByText(NOT_YET)).not.toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
    view.unmount();

    render(<ProgressDetailSection world={world} targetId={2} />);
    expect(screen.getByRole("heading", { name: "Progress" })).not.toBeNull();
    expect(screen.getByText(NOT_YET)).not.toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("ND-2 Raft ログは新しい順で、Commit 済みに印と区切りの行", () => {
    const world = loadWorld();
    setDetail(detail());
    render(<RaftLogSection world={world} targetId={2} />);
    expect(screen.getByRole("heading", { name: "Raft ログ: 2 二の島" })).not.toBeNull();
    const table = screen.getByRole("table", { name: "Raft ログ" });
    expect([...table.querySelectorAll("th")].map((th) => th.textContent)).toEqual([
      "Index",
      "Term",
      "Type",
      "Summary",
      "Commit",
    ]);
    const rows = bodyRows(table);
    expect(rows.map(cells)).toEqual([
      ["7", "2", "normal", "deliver r-7", "未"],
      ["Commit Index 6"],
      ["6", "2", "normal", "deliver r-6", "✓"],
      ["5", "2", "normal", "deliver r-5", "✓"],
    ]);
    expect(rows.map((r) => r.dataset.committed ?? null)).toEqual(["false", null, "true", "true"]);
    expect(rows[1]?.className).toBe("engineer-log-commit");
    expect(rows[1]?.querySelector("td")?.colSpan).toBe(5);
    // 最初の index が 5（1 より大きい）なので、省いた旨を出す。
    expect(screen.getByText("古いエントリは省いています（末尾の 3 件）。")).not.toBeNull();
  });

  it("ND-2 区切りの行の有無・省いた旨・空のログ", () => {
    const world = loadWorld();
    // すべて Commit 済み → 区切りは一番上。最初の index が 1 より大きい → 省いた旨。
    setDetail(detail({ commit: 13, log: [entry(12, true), entry(13, true, "")] }));
    const view = render(<RaftLogSection world={world} targetId={2} />);
    let rows = bodyRows(screen.getByRole("table", { name: "Raft ログ" }));
    expect(rows.map(cells)).toEqual([
      ["Commit Index 13"],
      ["13", "2", "normal", "—", "✓"],
      ["12", "2", "normal", "deliver r-12", "✓"],
    ]);
    expect(screen.getByText("古いエントリは省いています（末尾の 2 件）。")).not.toBeNull();

    // Commit が 0 → 区切りを出さない。
    setDetail(detail({ commit: 0, log: [entry(1, false)] }));
    rows = bodyRows(screen.getByRole("table", { name: "Raft ログ" }));
    expect(rows.map(cells)).toEqual([["1", "2", "normal", "deliver r-1", "未"]]);

    // 行の中に Commit 済みが無い → 区切りを出さない（commit は画面で計算し直さない）。
    setDetail(detail({ commit: 6, log: [entry(5, false), entry(6, false)] }));
    rows = bodyRows(screen.getByRole("table", { name: "Raft ログ" }));
    expect(rows.every((r) => r.dataset.committed === "false")).toBe(true);
    expect(rows).toHaveLength(2);

    // 最初の index が 1 なら省いた旨を出さない。
    setDetail(detail({ commit: 1, log: [entry(1, true)] }));
    expect(screen.queryByText(/古いエントリは省いています/)).toBeNull();

    setDetail(detail({ log: [] }));
    expect(screen.getByText("ログはまだありません。")).not.toBeNull();
    expect(screen.queryByRole("table", { name: "Raft ログ" })).toBeNull();
    view.unmount();
  });

  it("ND-3 detail.id が targetId と違えば届いていない扱い", () => {
    const world = loadWorld();
    setDetail(detail({ id: 3 }));
    render(<RaftLogSection world={world} targetId={2} />);
    expect(screen.getByText(NOT_YET)).not.toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByText(/Raft ログ:/)).toBeNull();
  });

  it("ND-4 Leader なら Progress の表。recentActive が偽の行に印。Node のボタンで島を選ぶ", () => {
    const world = loadWorld();
    setDetail(
      detail({
        id: 1,
        role: "leader",
        lead: 1,
        lastIndex: 7,
        progress: [
          { id: 2, match: 4, next: 5, state: "probe", recentActive: false },
          { id: 3, match: 7, next: 8, state: "replicate", recentActive: true },
        ],
      }),
    );
    render(<ProgressDetailSection world={world} targetId={1} />);
    expect(
      screen.getByRole("heading", { name: "Progress（Leader から見た複製の進み具合）" }),
    ).not.toBeNull();
    const table = screen.getByRole("table", { name: "Progress" });
    expect([...table.querySelectorAll("th")].map((th) => th.textContent)).toEqual([
      "Node",
      "Match",
      "Next",
      "State",
      "Active",
      "遅れ",
    ]);
    const rows = bodyRows(table);
    expect(rows.map(cells)).toEqual([
      ["2 二の島", "4", "5", "Probe", "no", "3"],
      ["3 三の島", "7", "8", "Replicate", "yes", "0"],
    ]);
    expect(rows.map((r) => r.dataset.inactive ?? null)).toEqual(["true", null]);
    expect(screen.queryByText(/Leader ではないので/)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "3 三の島" }));
    expect(gameStore.getState().selection.islandId).toBe(3);
  });

  it("ND-4 Follower のいない Leader は「Follower はいません。」", () => {
    const world = loadWorld();
    setDetail(detail({ id: 1, role: "leader", lead: 1, progress: [] }));
    render(<ProgressDetailSection world={world} targetId={1} />);
    expect(screen.getByText("Follower はいません。")).not.toBeNull();
    expect(screen.queryByRole("table", { name: "Progress" })).toBeNull();
  });

  it("ND-5 Follower なら Leader でない旨と、Leader を選ぶボタン", () => {
    const world = loadWorld();
    setDetail(detail({ lead: 1 }));
    const view = render(<ProgressDetailSection world={world} targetId={2} />);
    expect(
      screen.getByText("このノードは Leader ではないので、Progress はありません。"),
    ).not.toBeNull();
    expect(screen.queryByRole("table", { name: "Progress" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Leader（1 一の島）を見る" }));
    expect(gameStore.getState().selection.islandId).toBe(1);

    // Leader を知らない（lead が 0）ならボタンを出さない。
    setDetail(detail({ lead: 0 }));
    expect(screen.queryByRole("button", { name: /Leader（/ })).toBeNull();
    view.unmount();
  });
  it("ND-6 Raft ログは新しい順に ENGINEER_LOG_LIMIT 件まで。古いものは出さず、その旨を 1 行", () => {
    const world = loadWorld();
    const total = ENGINEER_LOG_LIMIT + 4;
    const log: LogEntry[] = [];
    for (let i = 1; i <= total; i++) log.push(entry(i, i <= total - 2));
    setDetail(detail({ commit: total - 2, lastIndex: total, log }));
    render(<RaftLogSection world={world} targetId={2} />);
    const rows = bodyRows(screen.getByRole("table", { name: "Raft ログ" })).filter(
      (r) => r.dataset.index !== undefined,
    );
    expect(rows.map((r) => Number(r.dataset.index))).toEqual(
      Array.from({ length: ENGINEER_LOG_LIMIT }, (_, i) => total - i),
    );
    // Commit 済みの最大の index（total − 2）の上に区切り。
    expect(screen.getByText(`Commit Index ${String(total - 2)}`)).not.toBeNull();
    expect(
      screen.getByText(
        `新しい ${String(ENGINEER_LOG_LIMIT)} 件だけ出しています（古いエントリは省いています）。`,
      ),
    ).not.toBeNull();
    expect(screen.queryByText(/末尾の/)).toBeNull();
  });
});
