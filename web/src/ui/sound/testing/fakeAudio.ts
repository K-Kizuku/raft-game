// テストのための偽の AudioContext（T-434）。sound のテストだけが import する（本番のコードから import しない）。
import type { EngineContext } from "../engine.ts";

export type ParamCall = Readonly<{ method: string; value: number; time: number }>;

export type FakeOscillator = {
  type: OscillatorType;
  frequency: { calls: ParamCall[] };
  connectedTo: unknown;
  startAt: number | null;
  stopAt: number | null;
};

export type FakeGain = {
  gain: { calls: ParamCall[] };
  connectedTo: unknown;
};

function param(calls: ParamCall[]): Record<string, unknown> {
  const record = (method: string) => (value: number, time: number) => {
    calls.push({ method, value, time });
  };
  return {
    calls,
    setValueAtTime: record("set"),
    linearRampToValueAtTime: record("linear"),
    exponentialRampToValueAtTime: record("exp"),
  };
}

export type FakeAudioContext = {
  context: EngineContext;
  oscillators: FakeOscillator[];
  gains: FakeGain[];
  destination: object;
  resumes: number;
  setTime(seconds: number): void;
  setState(state: AudioContextState): void;
};

export function createFakeAudioContext(currentTime = 0): FakeAudioContext {
  const oscillators: FakeOscillator[] = [];
  const gains: FakeGain[] = [];
  const destination = { name: "destination" };
  let time = currentTime;
  let state: AudioContextState = "running";
  const fake: FakeAudioContext = {
    context: undefined as unknown as EngineContext,
    oscillators,
    gains,
    destination,
    resumes: 0,
    setTime(seconds) {
      time = seconds;
    },
    setState(next) {
      state = next;
    },
  };
  const context = {
    get currentTime() {
      return time;
    },
    get state() {
      return state;
    },
    destination,
    resume() {
      fake.resumes++;
      state = "running";
      return Promise.resolve();
    },
    createOscillator() {
      const osc: FakeOscillator & Record<string, unknown> = {
        type: "sine",
        frequency: param([]) as FakeOscillator["frequency"],
        connectedTo: null,
        startAt: null,
        stopAt: null,
        connect(node: unknown) {
          osc.connectedTo = node;
          return node;
        },
        start(t: number) {
          osc.startAt = t;
        },
        stop(t: number) {
          osc.stopAt = t;
        },
      };
      oscillators.push(osc);
      return osc;
    },
    createGain() {
      const gain: FakeGain & Record<string, unknown> = {
        gain: param([]) as FakeGain["gain"],
        connectedTo: null,
        connect(node: unknown) {
          gain.connectedTo = node;
          return node;
        },
      };
      gains.push(gain);
      return gain;
    },
  };
  fake.context = context as unknown as EngineContext;
  return fake;
}
