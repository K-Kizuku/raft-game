// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { Clock, ServerEvent } from "../protocol/index.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import { gameStore, useGameStore } from "./store.ts";

const CLOCK: Clock = { t: 1000, speed: 1, paused: false, endsAt: 60000 };

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

describe("useGameStore", () => {
  it("HK-1 選んだ値が変わったときだけ描き直す", () => {
    let renders = 0;
    function Score() {
      renders++;
      const total = useGameStore((s) => s.world?.score.total ?? null);
      return <p>{total === null ? "なし" : String(total)}</p>;
    }

    const view = render(<Score />);
    expect(view.container.textContent).toBe("なし");

    act(() => {
      gameStore.getState().applySnapshot(loadMiniRound().snapshot, 0);
    });
    expect(view.container.textContent).toBe("0");
    const afterSnapshot = renders;

    const created: ServerEvent = {
      seq: 1,
      t: 1000,
      type: "letter.created",
      letter: {
        id: 1,
        kind: "raft",
        from: 1,
        to: 2,
        createdAt: 1000,
        loc: { type: "port", islandId: 1 },
        label: "heartbeat",
      },
    };
    act(() => {
      gameStore.getState().applyEvents(CLOCK, [created], 10);
    });
    expect(renders).toBe(afterSnapshot);

    const score: ServerEvent = {
      seq: 2,
      t: 2000,
      type: "score.updated",
      total: 100,
      successes: 1,
      criticalSecured: [],
    };
    act(() => {
      gameStore.getState().applyEvents(CLOCK, [score], 20);
    });
    expect(view.container.textContent).toBe("100");
    expect(renders).toBe(afterSnapshot + 1);
  });
});
