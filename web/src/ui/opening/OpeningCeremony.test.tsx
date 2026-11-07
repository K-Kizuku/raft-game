// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { StrictMode } from "react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { gameStore } from "../../state/index.ts";
import type { GameState } from "../../state/index.ts";
import { setShowRaftTerms } from "../terms/preference.ts";
import { OpeningCeremony, openingCeremonyState, useOpeningCeremonyActive } from "./index.ts";

type Snapshot = Parameters<GameState["applySnapshot"]>[0];
type Clock = Parameters<GameState["applyEvents"]>[0];
type ServerEvent = Parameters<GameState["applyEvents"]>[1][number];

function ActiveProbe(): ReactElement {
  const active = useOpeningCeremonyActive();
  return <output data-testid="active">{String(active)}</output>;
}

function renderCeremony(): ReturnType<typeof render> {
  return render(
    <>
      <OpeningCeremony />
      <ActiveProbe />
    </>,
  );
}

function clockAt(t: number): Clock {
  return { t, speed: 1, paused: false, endsAt: 60000 };
}

function loadSnapshot(snapshot: Snapshot): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function applySeq(events: readonly ServerEvent[], from: number, to: number, t = 0): void {
  act(() => {
    gameStore.getState().applyEvents(
      clockAt(t),
      events.filter((e) => e.seq >= from && e.seq <= to),
      0,
    );
  });
}

function banner(): HTMLElement | null {
  return screen.queryByRole("status", { name: "開会式" });
}

function active(): string {
  return screen.getByTestId("active").textContent ?? "";
}

function advance(ms: number): void {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

// aria-hidden の飾り（色の丸・👑）を除いた文字。
function visibleText(element: Element): string {
  const copy = element.cloneNode(true) as Element;
  for (const hidden of copy.querySelectorAll('[aria-hidden="true"]')) {
    hidden.remove();
  }
  return copy.textContent ?? "";
}

function message(): HTMLElement {
  const element = banner()?.querySelector<HTMLElement>(".opening-ceremony__message");
  if (element === null || element === undefined) throw new Error("メッセージが無い");
  return element;
}

// <summary> を押す。jsdom は toggle を後のタスクで出すので、時間を進めずにそれを流す。
function clickSummary(summary: HTMLElement): void {
  act(() => {
    fireEvent.click(summary);
  });
  advance(0);
}

// mini の 1 ラウンド目を、開会式のイベント（seq 1〜6）まで入れる。
function startMini(): ServerEvent[] {
  const { snapshot, events } = loadMiniRound();
  loadSnapshot(snapshot);
  applySeq(events, 1, 6);
  return events;
}

beforeEach(() => {
  vi.useFakeTimers();
  act(() => {
    setShowRaftTerms(false);
  });
  gameStore.getState().reset();
  openingCeremonyState.reset();
});

afterEach(() => {
  cleanup();
  act(() => {
    setShowRaftTerms(false);
  });
  gameStore.getState().reset();
  openingCeremonyState.reset();
  vi.useRealTimers();
});

describe("OpeningCeremony", () => {
  it("OC-1 開会式のイベントで約 3 秒だけ出る", () => {
    renderCeremony();
    const { snapshot, events } = loadMiniRound();
    loadSnapshot(snapshot);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");

    applySeq(events, 1, 6);
    expect(banner()).not.toBeNull();
    expect(within(banner() as HTMLElement).getByRole("heading", { name: "開会式" })).toBeTruthy();
    expect(visibleText(message())).toBe("代表に一の島が選ばれました");
    expect(active()).toBe("true");

    advance(2999);
    expect(banner()).not.toBeNull();
    expect(active()).toBe("true");
    advance(1);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
    expect(openingCeremonyState.getSnapshot().doneKeys.has("mock_mini_1:5")).toBe(true);
    expect(openingCeremonyState.getSnapshot().showingKey).toBeNull();
  });

  it("OC-2 出ている間に後のイベントが来ても 1 回だけ出る", () => {
    renderCeremony();
    const events = startMini();
    expect(banner()).not.toBeNull();
    advance(1000);
    applySeq(events, 7, 29, 30000);
    expect(banner()).not.toBeNull();
    advance(2000);
    expect(banner()).toBeNull();
    applySeq(events, 30, 30, 60000);
    advance(5000);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
  });

  it("OC-3 「閉じる」ですぐ消える", () => {
    renderCeremony();
    const heading = document.createElement("h1");
    heading.tabIndex = -1;
    const screenRoot = document.createElement("div");
    screenRoot.className = "game-screen";
    screenRoot.append(heading);
    document.body.append(screenRoot);
    try {
      startMini();
      const close = screen.getByRole("button", { name: "閉じる" });
      act(() => {
        close.focus();
      });
      fireEvent.click(close);
      expect(banner()).toBeNull();
      expect(active()).toBe("false");
      // フォーカスは document.body に落とさず、ゲーム画面の見出しへ移す
      expect(document.activeElement).toBe(heading);
      expect(openingCeremonyState.getSnapshot().showingKey).toBeNull();
      expect(openingCeremonyState.getSnapshot().doneKeys.has("mock_mini_1:5")).toBe(true);
      advance(5000);
      expect(banner()).toBeNull();
    } finally {
      screenRoot.remove();
    }
  });

  it("OC-4 「選挙の記録」を開いている間は消えない", () => {
    renderCeremony();
    startMini();
    const details = banner()?.querySelector("details") as HTMLDetailsElement;
    const summary = within(details).getByText("選挙の記録");
    advance(2500);
    clickSummary(summary);
    expect(details.open).toBe(true);
    advance(5000);
    expect(banner()).not.toBeNull();
    expect(active()).toBe("true");

    clickSummary(summary);
    expect(details.open).toBe(false);
    advance(999);
    expect(banner()).not.toBeNull();
    advance(1);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
  });

  it("OC-5 ポインタがある間は消えない", () => {
    renderCeremony();
    startMini();
    advance(500);
    fireEvent.pointerEnter(banner() as HTMLElement);
    advance(5000);
    expect(banner()).not.toBeNull();
    fireEvent.pointerLeave(banner() as HTMLElement);
    advance(2499);
    expect(banner()).not.toBeNull();
    advance(1);
    expect(banner()).toBeNull();
  });

  it("OC-5 残りが少ないときは resumeMinMs だけ出す", () => {
    renderCeremony();
    startMini();
    advance(2900);
    fireEvent.pointerEnter(banner() as HTMLElement);
    advance(5000);
    fireEvent.pointerLeave(banner() as HTMLElement);
    advance(999);
    expect(banner()).not.toBeNull();
    advance(1);
    expect(banner()).toBeNull();
  });

  it("OC-5 フォーカスがある間は消えない", () => {
    renderCeremony();
    startMini();
    const close = screen.getByRole("button", { name: "閉じる" });
    act(() => {
      close.focus();
    });
    advance(5000);
    expect(banner()).not.toBeNull();
    act(() => {
      close.blur();
    });
    advance(2999);
    expect(banner()).not.toBeNull();
    advance(1);
    expect(banner()).toBeNull();
  });

  it.each([false, true])("OC-6 選挙の記録は Raft の用語で出る（併記 %s）", (show) => {
    act(() => {
      setShowRaftTerms(show);
    });
    renderCeremony();
    startMini();
    const details = banner()?.querySelector("details") as HTMLElement;
    const record = within(details);
    expect(record.getByText("Term 2")).toBeTruthy();
    expect(record.getByText("Leader: 一の島（ID 1）")).toBeTruthy();
    expect(record.getByText("一の島（ID 1）が Leader になった（Term 2）")).toBeTruthy();

    const table = record.getByRole("table", { name: "各島の状態（いまの値）" });
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toEqual(["ID", "島", "Role", "Term", "Lead"]);
    const rows = within(table)
      .getAllByRole("row")
      .slice(1)
      .map((row) =>
        within(row)
          .getAllByRole("cell")
          .map((c) => c.textContent),
      );
    expect(rows).toEqual([
      ["1", "一の島", "Leader", "2", "1"],
      ["2", "二の島", "Follower", "2", "1"],
      ["3", "三の島", "Follower", "2", "1"],
    ]);
    expect(
      record.getByText("投票（Vote）は、エンジニアパネル（E）で選んだ島の詳細に出ます。"),
    ).toBeTruthy();
  });

  it("OC-7 「代表」の併記は設定に従う", () => {
    act(() => {
      setShowRaftTerms(true);
    });
    renderCeremony();
    startMini();
    expect(visibleText(message())).toBe("代表（Leader）に一の島が選ばれました");
    expect(message().querySelector(".raft-term-note")).not.toBeNull();

    act(() => {
      setShowRaftTerms(false);
    });
    expect(visibleText(message())).toBe("代表に一の島が選ばれました");
    expect(message().querySelector(".raft-term-note")).toBeNull();
  });

  it("OC-8 遅く気づいた結果は出さない", () => {
    renderCeremony();
    const { snapshot, events } = loadMiniRound();
    loadSnapshot(snapshot);
    applySeq(events, 1, 6, 6000);
    expect(banner()).toBeNull();
    expect(openingCeremonyState.getSnapshot().doneKeys.has("mock_mini_1:5")).toBe(true);
    expect(active()).toBe("false");
  });

  it("OC-8 ended のラウンドの結果は出さない", () => {
    renderCeremony();
    const { snapshot, events } = loadMiniRound();
    loadSnapshot(snapshot);
    applySeq(events, 1, 6);
    // 上で出たものとは別に、ended の世界を最初から入れる
    cleanup();
    gameStore.getState().reset();
    openingCeremonyState.reset();
    renderCeremony();
    loadSnapshot(snapshot);
    applySeq(
      events
        .filter((e) => e.seq <= 6 || e.seq === 30)
        .map((e) => (e.seq === 30 ? { ...e, t: 0 } : e)),
      1,
      30,
    );
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
  });

  it("OC-9 演出の途中の再接続のスナップショットでは最後まで出し、前なら出さない", () => {
    renderCeremony();
    const { snapshot, events } = loadMiniRound();
    startMini();
    advance(1000);
    loadSnapshot({
      ...snapshot,
      phase: "sailing",
      clock: { ...snapshot.clock, t: 1000 },
      lastSeq: 6,
    });
    expect(gameStore.getState().world?.leaderChanges).toEqual([]);
    expect(banner()).not.toBeNull();
    expect(active()).toBe("true");
    advance(1999);
    expect(banner()).not.toBeNull();
    advance(1);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");

    // 出る前に sailing のスナップショットが来たら出ない
    cleanup();
    gameStore.getState().reset();
    openingCeremonyState.reset();
    renderCeremony();
    loadSnapshot(snapshot);
    loadSnapshot({
      ...snapshot,
      phase: "sailing",
      clock: { ...snapshot.clock, t: 1000 },
      lastSeq: 6,
    });
    applySeq(events, 7, 8, 1000);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
  });

  it("OC-10 reset・アンマウントですぐ消え、タイマーを残さない", () => {
    const view = renderCeremony();
    startMini();
    expect(banner()).not.toBeNull();
    act(() => {
      gameStore.getState().reset();
    });
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
    expect(openingCeremonyState.getSnapshot().showingKey).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
    view.unmount();

    const again = render(<OpeningCeremony />);
    const { snapshot, events } = loadMiniRound();
    loadSnapshot({ ...snapshot, session: { ...snapshot.session, id: "mock_mini_2" } });
    applySeq(events, 1, 6);
    expect(banner()).not.toBeNull();
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    again.unmount();
    expect(vi.getTimerCount()).toBe(0);
    expect(openingCeremonyState.getSnapshot().showingKey).toBeNull();
    expect(openingCeremonyState.getSnapshot().doneKeys.has("mock_mini_2:5")).toBe(true);
  });

  it("OC-10 StrictMode の二重の effect でも begin・finish が食い違わない", () => {
    const view = render(
      <StrictMode>
        <OpeningCeremony />
        <ActiveProbe />
      </StrictMode>,
    );
    startMini();
    expect(banner()).not.toBeNull();
    expect(openingCeremonyState.getSnapshot().showingKey).toBe("mock_mini_1:5");
    advance(3000);
    expect(banner()).toBeNull();
    expect(active()).toBe("false");
    expect(openingCeremonyState.getSnapshot().showingKey).toBeNull();
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("OC-11 新しいラウンドではもう一度出る", () => {
    renderCeremony();
    startMini();
    advance(3000);
    expect(banner()).toBeNull();

    const { snapshot, events } = loadMiniRound();
    loadSnapshot({ ...snapshot, session: { ...snapshot.session, id: "mock_mini_2" } });
    expect(banner()).toBeNull();
    applySeq(events, 1, 6);
    expect(banner()).not.toBeNull();
    expect(active()).toBe("true");
    advance(3000);
    expect(banner()).toBeNull();
    expect(openingCeremonyState.getSnapshot().doneKeys.has("mock_mini_2:5")).toBe(true);
  });

  it("OC-12 画像・音・canvas を使わない", () => {
    renderCeremony();
    startMini();
    const section = banner() as HTMLElement;
    expect(section.querySelector("img, audio, canvas")).toBeNull();
    const island = section.querySelector(".opening-ceremony__island") as HTMLElement;
    expect(island.querySelector("img, audio, canvas")).toBeNull();
  });
});
