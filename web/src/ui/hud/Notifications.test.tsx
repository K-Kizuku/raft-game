// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ServerEvent, Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { setShowRaftTerms } from "../terms/preference.ts";
import { Notifications } from "./Notifications.tsx";
import { ISLAND_COLORS, island, makeRequest, makeSnapshot } from "./testing/world.ts";

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

function notice(
  seq: number,
  t: number,
  islandId: number,
  key: string,
  params: Record<string, unknown> = {},
): ServerEvent {
  return { seq, t, type: "notice", islandId, key, params };
}

const FOUR: ServerEvent[] = [
  notice(1, 20000, 1, "island.silent"),
  notice(2, 21000, 2, "leader.elected"),
  notice(3, 22000, 3, "request.succeeded", { reqId: "r-1" }),
  notice(4, 23000, 2, "storm.forecast", { routeIds: ["1-2"], at: 33000 }),
];

function load(events: ServerEvent[], snapshot: Snapshot = makeSnapshot()): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, performance.now());
    const { t, speed, paused, endsAt } = snapshot.clock;
    gameStore.getState().applyEvents({ t, speed, paused, endsAt }, events, performance.now());
  });
}

function withRequest(clock: Partial<Snapshot["clock"]> = {}): Snapshot {
  const base = makeSnapshot({ requests: [makeRequest("r-1", 3, { resource: "stall:main" })] });
  return { ...base, clock: { ...base.clock, ...clock } };
}

function items(): { speaker: string | null | undefined; text: string | null | undefined }[] {
  return [...document.querySelectorAll(".notices > li")].map((li) => ({
    speaker: li.querySelector("strong")?.textContent,
    text: li.querySelector(".notice-text")?.textContent,
  }));
}

function advance(ms: number): void {
  nowMs += ms;
  act(() => {
    vi.advanceTimersByTime(250);
  });
}

describe("Notifications", () => {
  it("NO-1 world が null・通知なしなら何も描かない", () => {
    const view = render(<Notifications />);
    expect(view.container.innerHTML).toBe("");
    load([]);
    expect(view.container.innerHTML).toBe("");
  });

  it("NO-2 新しい 3 件を新しい順に、島の郵便局長のセリフとして出す", () => {
    load(FOUR, withRequest());
    render(<Notifications />);
    expect(items()).toEqual([
      { speaker: "二の島の郵便局長", text: "「10 秒後に一の島と二の島の間が荒れるぞ！」" },
      { speaker: "三の島の郵便局長", text: "「やった！ 屋台が取れたよ！」" },
      { speaker: "二の島の郵便局長", text: "「これからは二の島が代表です。よろしく！」" },
    ]);
    const face = document.querySelector(".notices > li .notice-face");
    expect(face?.getAttribute("aria-hidden")).toBe("true");
    expect(face?.textContent).toBe("二");
    expect((face as HTMLElement | null)?.style.backgroundColor).toBe(
      hexToRgb(ISLAND_COLORS[2] ?? ""),
    );
    expect(screen.getByRole("list").getAttribute("aria-live")).toBeNull();
  });

  it("NO-2 用語の併記がオンなら Leader を添える", () => {
    setShowRaftTerms(true);
    load([notice(1, 28000, 2, "leader.elected")]);
    render(<Notifications />);
    expect(items()[0]?.text).toBe("「これからは二の島が代表（Leader）です。よろしく！」");
  });

  it("NO-3 ゲーム内の 10 秒より古い通知は消え、一時停止中は消えない", () => {
    load(FOUR, withRequest());
    const view = render(<Notifications />);
    advance(2500); // simNow 31500
    expect(items().map((i) => i.speaker)).toEqual(["二の島の郵便局長", "三の島の郵便局長"]);
    advance(2000); // simNow 33500
    expect(view.container.innerHTML).toBe("");
    cleanup();

    nowMs = 1000;
    load(FOUR, withRequest({ paused: true }));
    render(<Notifications />);
    advance(10000);
    expect(items()).toHaveLength(3);
    expect(items().map((i) => i.speaker)).toEqual([
      "二の島の郵便局長",
      "三の島の郵便局長",
      "二の島の郵便局長",
    ]);
  });

  it("NO-4 知らない key の通知は出さず、ほかの通知は出る", () => {
    load(
      [
        notice(1, 25000, 1, "island.silent"),
        notice(2, 26000, 2, "future.key"),
        notice(3, 27000, 9, "node.campaign"),
      ],
      makeSnapshot(),
    );
    render(<Notifications />);
    expect(items()).toEqual([
      { speaker: "島 9の郵便局長", text: "「代表と連絡がつかない…わたしが代表に立候補します！」" },
      { speaker: "一の島の郵便局長", text: "「最近、だれからも手紙が来ないなあ…」" },
    ]);
  });

  it("NO-4 知らない key だけなら何も描かない", () => {
    load([notice(1, 28000, 1, "future.key")]);
    const view = render(<Notifications />);
    expect(view.container.innerHTML).toBe("");
  });

  it("NO-P1 郵便局長のある島は「〇〇島の{名前}」と口調のセリフ、ほかの島は今のまま", () => {
    const base = makeSnapshot();
    const islands = base.stage.islands.map((i) =>
      i.id === 2
        ? island(i.id, i.name, i.color, i.layout.x, i.layout.y, {
            name: "テスト局長",
            tone: "polite",
          })
        : i,
    );
    load([notice(1, 27000, 1, "island.silent"), notice(2, 28000, 2, "island.silent")], {
      ...base,
      stage: { ...base.stage, islands },
    });
    render(<Notifications />);
    expect(items()).toEqual([
      { speaker: "二の島のテスト局長", text: "「最近、だれからも手紙が来ません…」" },
      { speaker: "一の島の郵便局長", text: "「最近、だれからも手紙が来ないなあ…」" },
    ]);
    expect(document.querySelector(".notices > li .notice-face")?.textContent).toBe("二");
  });
});

function hexToRgb(hex: string): string {
  const n = Number.parseInt(hex.slice(1), 16);
  return `rgb(${String((n >> 16) & 255)}, ${String((n >> 8) & 255)}, ${String(n & 255)})`;
}
