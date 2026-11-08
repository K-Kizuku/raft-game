// @vitest-environment jsdom
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Clock, Letter, Loc, ServerEvent } from "../../protocol/index.ts";
import { createGameStore, gameStore } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { EMPTY_REFLECTION_TALLY } from "../../state/reflection.ts";
import { setShowRaftTerms } from "../../ui/terms/preference.ts";
import type { StageSummary } from "../types.ts";
import { ReflectionSection } from "./ReflectionSection.tsx";
import { createReflectionTracker } from "./reflectionTracker.ts";
import { ResultScreen } from "./ResultScreen.tsx";
import { FULL_RESULT } from "./testing/results.ts";

const STAGE: StageSummary = {
  id: "mini",
  title: "ミニ",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 60000,
};

let seq = 1000;

function letter(id: number, loc: Loc, label: Letter["label"], from: number, to: number): Letter {
  return { id, kind: "raft", from, to, createdAt: 0, loc, label };
}

function clock(t: number): Clock {
  return { t, speed: 1, paused: false, endsAt: 60000 };
}

function events(t: number, list: readonly Record<string, unknown>[]): void {
  const full = list.map((e) => ({ ...e, seq: ++seq, t }) as unknown as ServerEvent);
  act(() => {
    gameStore.getState().applyEvents(clock(t), full, t);
  });
}

function startRound(sessionId: string): void {
  const { snapshot } = loadMiniRound();
  snapshot.session = { ...snapshot.session, id: sessionId };
  seq = snapshot.lastSeq;
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

// 一の島の港に手紙を置いたまま積まずに出航し、二の島の港で代表への返事を 20 秒待たせる。
function playStuckRound(): void {
  startRound("reflection_1");
  events(1000, [
    { type: "letter.created", letter: letter(1, { type: "port", islandId: 1 }, "append", 1, 3) },
    {
      type: "letter.created",
      letter: letter(2, { type: "port", islandId: 2 }, "heartbeat_resp", 2, 1),
    },
  ]);
  events(2000, [
    { type: "ferry.departed", ferryId: "f1", from: 1, to: 2, departAt: 2000, arriveAt: 6000 },
  ]);
  events(21000, []);
}

function reflection(): HTMLElement {
  return screen.getByRole("region", { name: "振り返り" });
}

function count(name: string): string {
  return reflection().querySelector(`[data-reflection="${name}"]`)?.textContent ?? "";
}

beforeEach(() => {
  gameStore.getState().reset();
  setShowRaftTerms(false);
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  setShowRaftTerms(false);
});

describe("ReflectionSection", () => {
  it("リザルトに振り返りの節があり、集めた数と次に試すことの一言が出る", () => {
    playStuckRound();
    act(() => {
      gameStore.getState().setRoundResult(FULL_RESULT);
    });
    render(
      <ResultScreen
        stage={STAGE}
        nextStage={null}
        onRetry={vi.fn()}
        onNextStage={vi.fn()}
        onStageSelect={vi.fn()}
      />,
    );

    expect(count("emptyDepartures")).toBe("1 回（一の島 1）");
    expect(count("replyWait")).toBe("20.0 秒（二の島の港）");
    expect(count("expiredRequests")).toBe("0 件");
    expect(count("overflow")).toBe("0 通");

    const note = within(reflection()).getByRole("note", { name: "次はこうしてみよう" });
    const tips = within(note).getAllByRole("listitem");
    expect(tips.map((tip) => tip.dataset["tip"])).toEqual(["emptyDepartures", "replyWait"]);
    expect(tips[0]?.textContent).toContain(
      "通り道の一の島でも、おまかせで積んでから出航しましょう。",
    );
    expect(tips[0]?.textContent).toContain("一の島で、港に手紙があるのに積まずに出航した回が 1 回");
    expect(tips[1]?.textContent).toContain("二の島の港で、代表への返事が最長 20.0 秒待ちました。");
  });

  it("用語の併記がオンのときだけ Raft の用語を添える", () => {
    playStuckRound();
    const { unmount } = render(<ReflectionSection />);
    expect(reflection().textContent).not.toContain("Leader");
    unmount();

    setShowRaftTerms(true);
    render(<ReflectionSection />);
    expect(reflection().textContent).toContain("代表（Leader）への返事の最長の待ち");
    expect(reflection().textContent).toContain("過半数（Quorum）");
  });

  it("詰まりが無ければ、その旨を出す", () => {
    startRound("reflection_2");
    events(1000, []);
    render(<ReflectionSection />);
    expect(count("emptyDepartures")).toBe("0 回");
    expect(count("replyWait")).toBe("—");
    expect(within(reflection()).getByRole("note").textContent).toContain(
      "大きな詰まりは見つかりませんでした。",
    );
  });

  it("新しいラウンドでは前のラウンドの数を持ち越さない", () => {
    playStuckRound();
    act(() => {
      gameStore.getState().reset();
    });
    startRound("reflection_3");
    render(<ReflectionSection />);
    expect(count("emptyDepartures")).toBe("0 回");
  });
});

describe("createReflectionTracker", () => {
  it("ストアの world の移り変わりを数え、reset で空に戻し、dispose で止まる", () => {
    const store = createGameStore();
    const tracker = createReflectionTracker(store);
    const listener = vi.fn();
    tracker.subscribe(listener);
    expect(tracker.getSnapshot()).toBe(EMPTY_REFLECTION_TALLY);

    const { snapshot } = loadMiniRound();
    store.getState().applySnapshot(snapshot, 0);
    const port1: Loc = { type: "port", islandId: 1 };
    store.getState().applyEvents(
      clock(1000),
      [
        {
          type: "letter.created",
          seq: snapshot.lastSeq + 1,
          t: 1000,
          letter: letter(1, port1, "append", 1, 2),
        },
      ],
      1000,
    );
    store.getState().applyEvents(
      clock(2000),
      [
        {
          type: "ferry.departed",
          seq: snapshot.lastSeq + 2,
          t: 2000,
          ferryId: "f1",
          from: 1,
          to: 2,
          departAt: 2000,
          arriveAt: 6000,
        },
      ],
      2000,
    );
    expect(tracker.getSnapshot().emptyDepartures).toEqual({ 1: 1 });
    expect(listener).toHaveBeenCalled();

    const calls = listener.mock.calls.length;
    store.getState().selectIsland(1);
    expect(listener).toHaveBeenCalledTimes(calls);

    store.getState().reset();
    expect(tracker.getSnapshot()).toBe(EMPTY_REFLECTION_TALLY);

    tracker.dispose();
    store.getState().applySnapshot(snapshot, 0);
    expect(tracker.getSnapshot()).toBe(EMPTY_REFLECTION_TALLY);
  });
});
