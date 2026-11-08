import { describe, expect, it } from "vitest";

import {
  ATTACK_SECONDS,
  SILENT_GAIN,
  SOUND_KINDS,
  SOUND_RECIPES,
  recipeLength,
} from "./recipes.ts";
import { playTones } from "./synth.ts";
import { createFakeAudioContext } from "./testing/fakeAudio.ts";

describe("SOUND_RECIPES", () => {
  it("SR-1 5 つの出来事に、短い音がある", () => {
    expect(SOUND_KINDS).toEqual(["depart", "arrive", "deliver", "confirm", "storm"]);
    for (const kind of SOUND_KINDS) {
      const tones = SOUND_RECIPES[kind];
      expect(tones.length, kind).toBeGreaterThan(0);
      expect(recipeLength(tones), kind).toBeLessThanOrEqual(1);
      for (const tone of tones) {
        expect(tone.from, kind).toBeGreaterThan(0);
        expect(tone.to, kind).toBeGreaterThan(0);
        expect(tone.start, kind).toBeGreaterThanOrEqual(0);
        expect(tone.duration, kind).toBeGreaterThan(ATTACK_SECONDS);
        expect(tone.peak, kind).toBeGreaterThan(0);
        expect(tone.peak, kind).toBeLessThanOrEqual(0.5);
      }
    }
  });

  it("SR-2 種類ごとに音が違う", () => {
    const shapes = SOUND_KINDS.map((kind) => JSON.stringify(SOUND_RECIPES[kind]));
    expect(new Set(shapes).size).toBe(SOUND_KINDS.length);
  });
});

describe("playTones", () => {
  it("SY-1 音符ごとに発振器→ゲイン→出口とつなぎ、今の時刻から鳴らして止める", () => {
    const fake = createFakeAudioContext(10);
    playTones(fake.context, SOUND_RECIPES.confirm);

    expect(fake.oscillators).toHaveLength(SOUND_RECIPES.confirm.length);
    expect(fake.gains).toHaveLength(SOUND_RECIPES.confirm.length);
    SOUND_RECIPES.confirm.forEach((tone, i) => {
      const osc = fake.oscillators[i];
      const gain = fake.gains[i];
      if (osc === undefined || gain === undefined) throw new Error("missing node");
      expect(osc.type).toBe(tone.wave);
      expect(osc.connectedTo).toBe(gain);
      expect(gain.connectedTo).toBe(fake.destination);
      expect(osc.startAt).toBeCloseTo(10 + tone.start);
      expect(osc.stopAt).toBeGreaterThan(10 + tone.start + tone.duration);
      expect(osc.frequency.calls[0]).toEqual({
        method: "set",
        value: tone.from,
        time: 10 + tone.start,
      });
    });
  });

  it("SY-2 エンベロープ: 0 から立ち上げ、peak×volume まで上げて、ほぼ無音まで下げる", () => {
    const fake = createFakeAudioContext(2);
    const [tone] = SOUND_RECIPES.arrive;
    if (tone === undefined) throw new Error("no tone");
    playTones(fake.context, [tone], 0.5);

    const calls = fake.gains[0]?.gain.calls ?? [];
    expect(calls.map((c) => c.method)).toEqual(["set", "linear", "exp"]);
    expect(calls[0]).toEqual({ method: "set", value: 0, time: 2 });
    expect(calls[1]?.value).toBeCloseTo(tone.peak * 0.5);
    expect(calls[1]?.time).toBeCloseTo(2 + ATTACK_SECONDS);
    expect(calls[2]?.value).toBe(SILENT_GAIN);
    expect(calls[2]?.time).toBeCloseTo(2 + tone.duration);
  });

  it("SY-3 周波数が変わる音符だけ、周波数を滑らかに変える", () => {
    const fake = createFakeAudioContext(0);
    playTones(fake.context, SOUND_RECIPES.deliver);
    const [first, second] = SOUND_RECIPES.deliver;
    if (first === undefined || second === undefined) throw new Error("no tone");
    expect(first.from).not.toBe(first.to);
    expect(fake.oscillators[0]?.frequency.calls.map((c) => c.method)).toEqual(["set", "exp"]);
    expect(fake.oscillators[0]?.frequency.calls[1]?.value).toBe(first.to);
    expect(second.from).toBe(second.to);
    expect(fake.oscillators[1]?.frequency.calls.map((c) => c.method)).toEqual(["set"]);
  });
});
