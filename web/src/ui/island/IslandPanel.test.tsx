// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { ISLAND_COLORS, makeRequest, makeSnapshot, portLetter } from "../hud/testing/world.ts";
import { setShowRaftTerms } from "../terms/preference.ts";
import { IslandPanel } from "./IslandPanel.tsx";

let nowMs = 1000;

beforeEach(() => {
  gameStore.getState().reset();
  nowMs = 1000;
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
  vi.spyOn(performance, "now").mockImplementation(() => nowMs);
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  vi.useRealTimers();
  vi.restoreAllMocks();
  setShowRaftTerms(false);
});

function load(snapshot: Snapshot, islandId: number | null = 1): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, performance.now());
    gameStore.getState().selectIsland(islandId);
  });
}

function pileRows(): {
  to: string | null | undefined;
  count: string | null | undefined;
  ids: string[];
}[] {
  return [...document.querySelectorAll(".island-pile")].map((li) => ({
    to: li.querySelector(".island-pile-to")?.textContent,
    count: li.querySelector(".island-pile-count")?.textContent,
    ids: [...li.querySelectorAll("svg")].map((svg) => svg.getAttribute("data-label") ?? ""),
  }));
}

function requestRow(id: string): HTMLElement {
  const row = document.querySelector<HTMLElement>(`[data-request="${id}"]`);
  if (row === null) throw new Error(`依頼 ${id} の行が無い`);
  return row;
}

describe("IslandPanel", () => {
  it("IP-1 world が null なら「島の詳細（準備中）」、選択なしなら案内", () => {
    render(<IslandPanel />);
    expect(screen.getByText("島の詳細（準備中）")).toBeDefined();
    load(makeSnapshot(), null);
    expect(screen.getByText("マップの島を選ぶと、港の手紙と依頼がここに出ます。")).toBeDefined();
    act(() => {
      gameStore.getState().reset();
    });
    expect(screen.getByText("島の詳細（準備中）")).toBeDefined();
  });

  it("IP-2 島の名前・役割・満足度・宛先別の港の手紙", () => {
    load(
      makeSnapshot({
        letters: [
          portLetter(5, 1, 3, { label: "append" }),
          portLetter(2, 1, 2),
          portLetter(9, 1, 3, { label: "vote" }),
          portLetter(4, 2, 1),
        ],
      }),
    );
    render(<IslandPanel />);
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("一の島");
    expect(screen.getByText("一の島村")).toBeDefined();
    const role = document.querySelector(".island-role");
    expect(role?.textContent).toBe("👑 代表");
    expect(screen.getByRole("meter", { name: "満足度" }).getAttribute("value")).toBe("72");
    expect(pileRows()).toEqual([
      { to: "→ 二の島", count: "1 通", ids: ["heartbeat"] },
      { to: "→ 三の島", count: "2 通", ids: ["append", "vote"] },
    ]);
    const third = document.querySelectorAll(".island-pile")[1];
    const border = third?.querySelector('[data-part="border"]');
    expect(border?.getAttribute("stroke")).toBe(ISLAND_COLORS[3]);
  });

  it.each<[Snapshot["nodes"][number]["role"], string]>([
    ["candidate", "📣 立候補中"],
    ["pre_candidate", "💭 立候補の相談中"],
    ["follower", "一般の島"],
  ])("IP-2 役割 %s は「%s」", (role, want) => {
    const base = makeSnapshot();
    load({ ...base, nodes: base.nodes.map((n) => (n.id === 1 ? { ...n, role } : n)) });
    render(<IslandPanel />);
    expect(document.querySelector(".island-role")?.textContent).toBe(want);
  });

  it("IP-3 依頼の重要・資源・状態・残り時間・期限切れ", () => {
    load(
      makeSnapshot({
        requests: [
          makeRequest("r-a", 1, {
            resource: "firework:a",
            critical: true,
            status: "proposed",
            deadline: 71000,
          }),
          makeRequest("r-exp", 1, { status: "waiting", expired: true }),
          makeRequest("r-ok", 1, { status: "succeeded" }),
          makeRequest("r-unk", 1, {
            resource: "nothing",
            kind: "boat.reserve",
            status: "received",
          }),
          makeRequest("r-other", 2, { status: "waiting" }),
        ],
      }),
    );
    render(<IslandPanel />);
    const a = requestRow("r-a");
    expect(a.textContent).toBe("★重要大花火 A🗳審議中あと 0:42");
    expect(a.querySelector(".island-request-mark")?.getAttribute("aria-hidden")).toBe("true");

    const exp = requestRow("r-exp");
    expect(exp.textContent).toContain("・屋台📮送付待ち");
    expect(exp.textContent).toContain("⌛期限切れ");
    expect(exp.textContent).not.toContain("あと");
    expect(exp.textContent).not.toContain("重要");

    const done = requestRow("r-ok");
    expect(done.textContent).toBe("・屋台✅確定");

    expect(requestRow("r-unk").textContent).toContain("boat.reserve");
    expect(document.querySelector('[data-request="r-other"]')).toBeNull();

    nowMs += 2000;
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(requestRow("r-a").textContent).toContain("あと 0:40");
  });

  it("IP-4 用語の併記は「代表」「手紙」をそれぞれ 1 か所", () => {
    setShowRaftTerms(true);
    load(makeSnapshot({ letters: [portLetter(1, 1, 2)] }));
    const view = render(<IslandPanel />);
    const notes = [...view.container.querySelectorAll("[data-raft-term]")].map((el) =>
      el.getAttribute("data-raft-term"),
    );
    expect(notes.filter((n) => n === "Leader")).toHaveLength(1);
    expect(notes.filter((n) => n === "Message")).toHaveLength(1);
    expect(document.querySelector(".island-role")?.textContent).toBe("👑 代表（Leader）");
    expect(screen.getByRole("heading", { level: 3, name: "港の手紙（Message）" })).toBeDefined();
  });

  it("IP-5 空の港・依頼なし・満足度なし・ステージに無い島・12 通の山", () => {
    load(makeSnapshot({ islandsState: [] }));
    render(<IslandPanel />);
    expect(screen.getByText("港に手紙はありません。")).toBeDefined();
    expect(screen.getByText("依頼はありません。")).toBeDefined();
    expect(document.querySelector(".island-satisfaction")?.textContent).toBe("満足度 —");
    expect(screen.queryByRole("meter", { name: "満足度" })).toBeNull();
    cleanup();

    const base = makeSnapshot();
    load(
      {
        ...base,
        nodes: [
          ...base.nodes,
          { id: 9, role: "follower", term: 2, lead: 1, commit: 0, applied: 0, lastIndex: 0 },
        ],
        letters: [portLetter(1, 9, 7)],
      },
      9,
    );
    render(<IslandPanel />);
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("島 9");
    expect(document.querySelector(".island-role")?.textContent).toBe("一般の島");
    expect(pileRows()[0]?.to).toBe("→ 島 7");
    expect(document.querySelector('[data-part="border"]')?.getAttribute("stroke")).toBe("#9CA3AF");
    cleanup();

    const many = Array.from({ length: 12 }, (_, i) => portLetter(100 + i, 1, 2));
    load(makeSnapshot({ letters: many }));
    render(<IslandPanel />);
    const row = document.querySelector(".island-pile");
    expect(row?.querySelectorAll("svg")).toHaveLength(10);
    expect(row?.querySelector(".island-pile-count")?.textContent).toBe("12 通");
    expect(row?.querySelector(".island-pile-rest")?.textContent).toBe("+2");
  });

  it("新しいスナップショットで選んだ島が無くなったら案内に戻る", () => {
    load(makeSnapshot());
    render(<IslandPanel />);
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("一の島");
    const base = makeSnapshot();
    act(() => {
      gameStore.getState().applySnapshot(
        {
          ...base,
          stage: { ...base.stage, islands: base.stage.islands.filter((i) => i.id !== 1) },
          nodes: base.nodes.filter((n) => n.id !== 1),
        },
        performance.now(),
      );
    });
    expect(screen.getByText("マップの島を選ぶと、港の手紙と依頼がここに出ます。")).toBeDefined();
  });
});
