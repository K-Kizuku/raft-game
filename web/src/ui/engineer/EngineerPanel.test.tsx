// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Letter, NodeDetail, ServerEvent, Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
// 用語の設定は、設定画面のチェックボックスで切り替える（パネルは設定を使わない。EP-7）。
import { SettingsScreen } from "../../app/settings/SettingsScreen.tsx";
import { EngineerPanel } from "./EngineerPanel.tsx";
import { EngineerSubscriptionProvider } from "./EngineerSubscription.tsx";
import type { EngineerSubscriber } from "./EngineerSubscription.tsx";

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  setTermsSetting(false);
});

// 設定画面の「Raft の用語を併記する」を show にする。
function setTermsSetting(show: boolean): void {
  const view = render(<SettingsScreen />);
  const checkbox = within(view.container).getByRole<HTMLInputElement>("checkbox", {
    name: "Raft の用語を併記する",
  });
  if (checkbox.checked !== show) fireEvent.click(checkbox);
  expect(checkbox.checked).toBe(show);
  view.unmount();
}

let miniEvents: ServerEvent[] = [];
let applied = 0;

// mini の記録のスナップショットと、seq 1〜n のイベントを入れる。
function loadMini(n: number): void {
  const { snapshot, events } = loadMiniRound();
  miniEvents = events;
  applied = 0;
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
  advance(n);
}

// 続きのイベントを seq n まで入れる。
function advance(n: number): void {
  const events = miniEvents.slice(applied, n);
  applied = n;
  const last = events[events.length - 1];
  if (last === undefined) return;
  act(() => {
    gameStore
      .getState()
      .applyEvents({ t: last.t, speed: 1, paused: false, endsAt: 60000 }, events, 0);
  });
}

function detail(id: number, vote: number): NodeDetail {
  return {
    id,
    role: id === 1 ? "leader" : "follower",
    term: 2,
    vote,
    lead: 1,
    commit: 0,
    applied: 0,
    lastIndex: 1,
    log: [],
    ledger: {},
    electionEstimate: { ticksSinceLeaderContact: 0, note: "" },
  };
}

function toggle(): HTMLElement {
  return screen.getByRole("button", { name: "E エンジニアモード" });
}

function panel(): HTMLElement {
  return screen.getByRole("complementary", { name: "エンジニアパネル" });
}

function queryPanel(): HTMLElement | null {
  return screen.queryByRole("complementary", { name: "エンジニアパネル" });
}

function open(): void {
  fireEvent.click(toggle());
}

function nodeTable(): HTMLElement {
  return screen.getByRole("table", { name: "ノード一覧" });
}

function rowCells(id: number): string[] {
  const row = nodeTable().querySelector(`tr[data-node="${String(id)}"]`);
  if (row === null) throw new Error(`島 ${String(id)} の行が無い`);
  return [...row.querySelectorAll("td")].map((td) => td.textContent);
}

function fieldValue(name: string): string | null {
  const dts = [...panel().querySelectorAll("dl dt")];
  const dt = dts.find((d) => d.textContent === name);
  return dt?.nextElementSibling?.textContent ?? null;
}

describe("EngineerPanel", () => {
  it("EP-1 ボタンと E キーで開閉し、入力欄・修飾キー・押しっぱなしでは切り替えない", () => {
    const view = render(<EngineerPanel />);
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
    expect(toggle().getAttribute("aria-keyshortcuts")).toBe("E");
    expect(queryPanel()).toBeNull();

    open();
    expect(queryPanel()).not.toBeNull();
    expect(toggle().getAttribute("aria-expanded")).toBe("true");
    expect(toggle().getAttribute("aria-controls")).toBe(panel().id);

    fireEvent.keyDown(window, { key: "e" });
    expect(queryPanel()).toBeNull();
    fireEvent.keyDown(window, { key: "E" });
    expect(queryPanel()).not.toBeNull();

    const input = document.createElement("input");
    document.body.append(input);
    input.focus();
    fireEvent.keyDown(input, { key: "e" });
    expect(queryPanel()).not.toBeNull();
    input.remove();

    fireEvent.keyDown(window, { key: "e", ctrlKey: true });
    expect(queryPanel()).not.toBeNull();
    fireEvent.keyDown(window, { key: "e", repeat: true });
    expect(queryPanel()).not.toBeNull();

    fireEvent.click(within(panel()).getByRole("button", { name: "閉じる" }));
    expect(queryPanel()).toBeNull();
    expect(document.activeElement).toBe(toggle());

    view.unmount();
    expect(() => {
      fireEvent.keyDown(window, { key: "e" });
    }).not.toThrow();
  });

  it("EP-2 world が null なら案内だけで、購読しない", () => {
    const subscribe = vi.fn<EngineerSubscriber>();
    render(
      <EngineerSubscriptionProvider subscribe={subscribe}>
        <EngineerPanel />
      </EngineerSubscriptionProvider>,
    );
    open();
    expect(within(panel()).getByText("まだ島の状態が届いていません。")).toBeDefined();
    expect(screen.queryByRole("table")).toBeNull();
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("EP-3 ノード一覧の見出しと値。Vote は全ノードについて node.state の値", () => {
    loadMini(5);
    render(<EngineerPanel />);
    open();
    const headers = within(nodeTable())
      .getAllByRole("columnheader")
      .map((th) => th.textContent);
    expect(headers).toEqual([
      "Node",
      "Role",
      "Term",
      "Vote",
      "Lead",
      "Commit",
      "Applied",
      "Last Index",
    ]);
    expect(rowCells(1)).toEqual(["1 一の島", "Leader", "2", "1", "1", "0", "0", "1"]);
    expect(rowCells(2)).toEqual(["2 二の島", "Follower", "2", "1", "1", "0", "0", "0"]);
    expect(rowCells(3)[3]).toBe("1");
    const leaderRows = nodeTable().querySelectorAll('tr[data-role="leader"]');
    expect([...leaderRows].map((r) => r.getAttribute("data-node"))).toEqual(["1"]);

    // node.detail の vote ではなく node.state の値を出す。
    act(() => {
      gameStore.getState().setNodeDetail(detail(1, 3));
    });
    expect([1, 2, 3].map((id) => rowCells(id)[3])).toEqual(["1", "1", "1"]);

    // 0（投票していない）は「—」。
    const { snapshot } = loadMiniRound();
    act(() => {
      gameStore.getState().applySnapshot(
        {
          ...snapshot,
          nodes: snapshot.nodes.map((n) => (n.id === 3 ? { ...n, term: 1, vote: 3 } : n)),
        },
        0,
      );
    });
    expect([1, 2, 3].map((id) => rowCells(id)[3])).toEqual(["—", "—", "3"]);

    expect(within(panel()).queryByText(/Vote は/)).toBeNull();
  });

  it("EP-3 Provider があっても Vote の注記は出さない。ノードが空なら表を出さない", () => {
    loadMini(5);
    render(
      <EngineerSubscriptionProvider subscribe={vi.fn<EngineerSubscriber>()}>
        <EngineerPanel />
      </EngineerSubscriptionProvider>,
    );
    open();
    expect(within(panel()).queryByText(/Vote は/)).toBeNull();

    const { snapshot } = loadMiniRound();
    act(() => {
      gameStore.getState().applySnapshot({ ...snapshot, nodes: [] }, 0);
    });
    expect(screen.queryByRole("table", { name: "ノード一覧" })).toBeNull();
    expect(within(panel()).getByText("ノードの状態が届いていません。")).toBeDefined();
  });

  it("EP-4 開いている間だけ選んでいるノードを購読する", () => {
    loadMini(5);
    const subscribe = vi.fn<EngineerSubscriber>();
    render(
      <EngineerSubscriptionProvider subscribe={subscribe}>
        <EngineerPanel />
      </EngineerSubscriptionProvider>,
    );
    expect(subscribe).not.toHaveBeenCalled();

    open();
    expect(subscribe).toHaveBeenLastCalledWith({ enabled: true, nodeId: 1 });

    const island2 = within(nodeTable()).getByRole("button", { name: "2 二の島" });
    fireEvent.click(island2);
    expect(gameStore.getState().selection.islandId).toBe(2);
    expect(island2.getAttribute("aria-pressed")).toBe("true");
    expect(subscribe).toHaveBeenLastCalledWith({ enabled: true, nodeId: 2 });

    fireEvent.click(within(nodeTable()).getByRole("button", { name: "2 二の島" }));
    expect(subscribe).toHaveBeenCalledTimes(2);

    act(() => {
      gameStore.getState().setNodeDetail(detail(2, 1));
    });
    fireEvent.click(toggle());
    expect(subscribe).toHaveBeenLastCalledWith({ enabled: false });
    expect(gameStore.getState().nodeDetail).toBeNull();
    expect(subscribe.mock.calls).toEqual([
      [{ enabled: true, nodeId: 1 }],
      [{ enabled: true, nodeId: 2 }],
      [{ enabled: false }],
    ]);
  });

  it("EP-4 開いたままアンマウントすると購読をやめる。world が無くなっても同じ", () => {
    loadMini(5);
    const subscribe = vi.fn<EngineerSubscriber>();
    const view = render(
      <EngineerSubscriptionProvider subscribe={subscribe}>
        <EngineerPanel />
      </EngineerSubscriptionProvider>,
    );
    open();
    act(() => {
      gameStore.getState().reset();
    });
    expect(subscribe.mock.calls).toEqual([[{ enabled: true, nodeId: 1 }], [{ enabled: false }]]);

    loadMini(5);
    expect(subscribe).toHaveBeenLastCalledWith({ enabled: true, nodeId: 1 });
    act(() => {
      gameStore.getState().setNodeDetail(detail(1, 1));
    });
    view.unmount();
    expect(subscribe).toHaveBeenLastCalledWith({ enabled: false });
    expect(subscribe).toHaveBeenCalledTimes(4);
    expect(gameStore.getState().nodeDetail).toBeNull();
  });

  it("EP-4 購読の関数が無ければ何も送らず、閉じたら詳細を消す", () => {
    loadMini(5);
    render(<EngineerPanel />);
    open();
    act(() => {
      gameStore.getState().setNodeDetail(detail(1, 1));
    });
    fireEvent.click(toggle());
    expect(gameStore.getState().nodeDetail).toBeNull();
  });

  it("EP-5 選んだ島の港の手紙を並べ、選ぶと詳細を出す", () => {
    loadMini(9);
    render(<EngineerPanel />);
    open();
    expect(
      within(panel()).getByText("マップで島か船を選ぶと、そこにある手紙が並びます。"),
    ).toBeDefined();

    act(() => {
      gameStore.getState().selectIsland(1);
    });
    expect(within(panel()).getByRole("heading", { name: "港: 一の島" })).toBeDefined();
    const first = within(panel()).getByRole("button", { name: "#1 MsgHeartbeat 1→2" });
    const second = within(panel()).getByRole("button", { name: "#2 MsgApp 1→3" });
    expect(first.querySelector("svg")).not.toBeNull();
    expect(second.querySelector('[data-part="border"]')?.getAttribute("stroke")).toBe("#81B29A");

    fireEvent.click(second);
    expect(second.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(second);
    expect(second.getAttribute("aria-pressed")).toBe("true");
    expect(within(panel()).getByRole("heading", { name: "手紙の詳細 #2" })).toBeDefined();
    expect(within(panel()).getByText("港: 一の島", { selector: "p" })).toBeDefined();
    expect(fieldValue("Type")).toBe("MsgApp");
    expect(fieldValue("To")).toBe("3");
    expect(fieldValue("Entries")).toBe("1");

    const entries = within(panel()).getByRole("table", { name: "Entries" });
    const cells = [...entries.querySelectorAll("tbody td")].map((td) => td.textContent);
    expect(cells).toEqual(["2", "2", "normal", "stall.reserve mini-0001"]);

    fireEvent.click(first);
    expect(within(panel()).getByText("Entries はありません。")).toBeDefined();

    act(() => {
      gameStore.getState().selectIsland(3);
    });
    expect(within(panel()).getByText("手紙はありません。")).toBeDefined();
  });

  it("EP-6 手紙の行方（船・配達・stale・消えた手紙）", () => {
    loadMini(9);
    render(<EngineerPanel />);
    open();
    act(() => {
      gameStore.getState().selectIsland(1);
    });
    fireEvent.click(within(panel()).getByRole("button", { name: "#1 MsgHeartbeat 1→2" }));

    advance(10);
    act(() => {
      gameStore.getState().selectFerry("f1");
    });
    expect(within(panel()).getByRole("heading", { name: "船: テスト丸" })).toBeDefined();
    expect(within(panel()).getByRole("button", { name: "#1 MsgHeartbeat 1→2" })).toBeDefined();
    expect(within(panel()).getByText("船: テスト丸", { selector: "p" })).toBeDefined();

    advance(14);
    expect(within(panel()).getByText("配達済み（0:07、二の島、受け取り）")).toBeDefined();
    expect(fieldValue("Type")).toBe("MsgHeartbeat");

    fireEvent.click(within(panel()).getByRole("button", { name: "#2 MsgApp 1→3" }));
    advance(20);
    const second = within(panel()).getByRole("button", { name: "#2 MsgApp 1→3" });
    expect(second.querySelector('[data-stale="true"]')).not.toBeNull();
    expect(
      within(panel()).getByText("古い手紙（送り主の今の Term より古い Term の手紙）"),
    ).toBeDefined();
    expect(fieldValue("Stale")).toBe("true");
  });

  it("EP-6 処分された手紙と、letterExits の無い世界で消えた手紙", () => {
    loadMini(18);
    render(<EngineerPanel />);
    open();
    act(() => {
      gameStore.getState().selectIsland(1);
    });
    fireEvent.click(within(panel()).getByRole("button", { name: "#3 MsgHeartbeat 1→3" }));
    advance(19);
    expect(within(panel()).getByText("処分（0:12、新しい手紙に差し替え）")).toBeDefined();
    expect(fieldValue("Type")).toBe("MsgHeartbeat");

    const letter: Letter = {
      id: 40,
      kind: "raft",
      from: 2,
      to: 1,
      createdAt: 5000,
      loc: { type: "port", islandId: 1 },
      label: "heartbeat_resp",
    };
    const { snapshot } = loadMiniRound();
    const withLetter: Snapshot = { ...snapshot, letters: [letter] };
    act(() => {
      gameStore.getState().applySnapshot(withLetter, 0);
    });
    fireEvent.click(within(panel()).getByRole("button", { name: "#40 heartbeat_resp 2→1" }));
    expect(fieldValue("Type")).toBe("—");
    act(() => {
      gameStore.getState().applySnapshot(snapshot, 0);
    });
    expect(within(panel()).getByText("この手紙はもうありません。")).toBeDefined();
    expect(panel().querySelector("dl")).toBeNull();
  });

  it("EP-6 一覧は 50 通まで並べ、残りは「+k 通」", () => {
    const { snapshot } = loadMiniRound();
    const letters: Letter[] = [];
    for (let id = 1; id <= 53; id++) {
      letters.push({
        id,
        kind: "raft",
        from: 1,
        to: 9,
        createdAt: 0,
        loc: { type: "port", islandId: 1 },
        label: "heartbeat",
      });
    }
    act(() => {
      gameStore.getState().applySnapshot({ ...snapshot, letters }, 0);
      gameStore.getState().selectIsland(1);
    });
    render(<EngineerPanel />);
    open();
    const buttons = panel().querySelectorAll("button[data-letter]");
    expect(buttons).toHaveLength(50);
    expect(buttons[0]?.querySelector('[data-part="border"]')).toBeNull();
    expect(within(panel()).getByText("+3 通")).toBeDefined();
  });

  it.each([true, false])("EP-7 用語の設定（%s）に関係なく Raft の用語で出す", (show) => {
    setTermsSetting(show);
    loadMini(5);
    render(<EngineerPanel />);
    open();
    expect(rowCells(1)[1]).toBe("Leader");
    expect(rowCells(2)[1]).toBe("Follower");
    expect(panel().textContent).not.toContain("代表");
    expect(panel().textContent).not.toContain("一般の島");
  });
});
