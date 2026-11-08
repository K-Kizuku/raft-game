// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createGameStore } from "../../state/index.ts";
import { armSoundUnlock, createSoundEngine, type SoundEngine } from "./engine.ts";
import { SOUND_RECIPES, type SoundKind } from "./recipes.ts";
import { createFakeAudioContext } from "./testing/fakeAudio.ts";
import { CLOCK, delivered, departed, requestUpdated, soundSnapshot } from "./testing/snapshot.ts";
import { createSoundThrottle } from "./throttle.ts";
import { useGameSounds } from "./useGameSounds.ts";

afterEach(() => {
  cleanup();
});

function setup(options: { enabled?: boolean; create?: () => null } = {}) {
  const fake = createFakeAudioContext();
  let enabled = options.enabled ?? true;
  let now = 1000;
  const createContext = vi.fn(options.create ?? (() => fake.context));
  const engine = createSoundEngine({
    createContext,
    isEnabled: () => enabled,
    now: () => now,
    throttle: createSoundThrottle({ minGapMs: 200, windowMs: 1000, maxPerWindow: 3 }),
    volume: 1,
  });
  return {
    fake,
    engine,
    createContext,
    setEnabled(value: boolean) {
      enabled = value;
    },
    advance(ms: number) {
      now += ms;
    },
  };
}

describe("createSoundEngine", () => {
  it("EN-1 最初の操作（unlock）までは AudioContext を作らず、鳴らさない", () => {
    const { fake, engine, createContext } = setup();
    expect(engine.play("depart")).toBe(false);
    expect(createContext).not.toHaveBeenCalled();
    expect(engine.unlocked()).toBe(false);

    engine.unlock();
    expect(engine.unlocked()).toBe(true);
    expect(engine.play("depart")).toBe(true);
    expect(fake.oscillators).toHaveLength(SOUND_RECIPES.depart.length);
  });

  it("EN-2 音を消していれば鳴らさない", () => {
    const { fake, engine, setEnabled } = setup({ enabled: false });
    engine.unlock();
    expect(engine.play("arrive")).toBe(false);
    expect(fake.oscillators).toHaveLength(0);
    setEnabled(true);
    expect(engine.play("arrive")).toBe(true);
  });

  it("EN-3 間引きを通ったものだけ鳴らす", () => {
    const { fake, engine, advance } = setup();
    engine.unlock();
    const kinds: SoundKind[] = ["deliver", "deliver", "deliver"];
    expect(kinds.map((kind) => engine.play(kind))).toEqual([true, false, false]);
    advance(250);
    expect(engine.play("deliver")).toBe(true);
    expect(fake.oscillators).toHaveLength(SOUND_RECIPES.deliver.length * 2);
  });

  it("EN-4 止まっている（suspended）AudioContext は再開する", () => {
    const { fake, engine } = setup();
    fake.setState("suspended");
    engine.unlock();
    expect(fake.resumes).toBe(1);
    fake.setState("suspended");
    engine.play("confirm");
    expect(fake.resumes).toBe(2);
  });

  it("EN-5 Web Audio が無いブラウザでは何もしない（作り直さない）", () => {
    const { engine, createContext } = setup({ create: () => null });
    engine.unlock();
    engine.unlock();
    expect(createContext).toHaveBeenCalledTimes(1);
    expect(engine.unlocked()).toBe(false);
    expect(engine.play("storm")).toBe(false);
  });
});

describe("armSoundUnlock", () => {
  it("AU-1 最初のクリック・キーで unlock し、リスナーを外す", () => {
    const { engine, createContext } = setup();
    const target = new EventTarget();
    armSoundUnlock(engine, target, false);
    expect(engine.unlocked()).toBe(false);

    target.dispatchEvent(new Event("pointerdown"));
    expect(engine.unlocked()).toBe(true);
    target.dispatchEvent(new Event("keydown"));
    expect(createContext).toHaveBeenCalledTimes(1);
  });

  it("AU-2 キーでも unlock する", () => {
    const { engine } = setup();
    const target = new EventTarget();
    armSoundUnlock(engine, target, false);
    target.dispatchEvent(new Event("keydown"));
    expect(engine.unlocked()).toBe(true);
  });

  it("AU-3 すでにページで操作があったなら、すぐに unlock する", () => {
    const { engine } = setup();
    const target = new EventTarget();
    const add = vi.spyOn(target, "addEventListener");
    armSoundUnlock(engine, target, true);
    expect(engine.unlocked()).toBe(true);
    expect(add).not.toHaveBeenCalled();
  });

  it("AU-4 外す関数を呼ぶと、操作があっても unlock しない", () => {
    const { engine } = setup();
    const target = new EventTarget();
    const remove = armSoundUnlock(engine, target, false);
    remove();
    target.dispatchEvent(new Event("pointerdown"));
    expect(engine.unlocked()).toBe(false);
  });
});

describe("useGameSounds", () => {
  function fakeEngine(): SoundEngine & { played: SoundKind[] } {
    const played: SoundKind[] = [];
    return {
      played,
      unlock: () => undefined,
      unlocked: () => true,
      play(kind) {
        played.push(kind);
        return true;
      },
    };
  }

  it("GS-1 ストアのイベントで鳴らし、snapshot では鳴らさない", () => {
    const store = createGameStore();
    const engine = fakeEngine();
    const { unmount } = renderHook(() => {
      useGameSounds(store, engine);
    });

    act(() => {
      store.getState().applySnapshot(soundSnapshot(), 0);
    });
    expect(engine.played).toEqual([]);

    act(() => {
      store.getState().applyEvents(CLOCK, [departed(11), delivered(12)], 1);
    });
    expect(engine.played).toEqual(["depart", "deliver"]);

    // 選択の変更など、世界が変わらないときは鳴らさない
    act(() => {
      store.getState().selectIsland(1);
    });
    expect(engine.played).toEqual(["depart", "deliver"]);

    unmount();
    act(() => {
      store.getState().applyEvents(CLOCK, [requestUpdated(13, "succeeded")], 2);
    });
    expect(engine.played).toEqual(["depart", "deliver"]);
  });
});
