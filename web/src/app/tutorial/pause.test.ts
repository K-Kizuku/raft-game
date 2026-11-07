import { describe, expect, it } from "vitest";

import { INITIAL_PAUSE_MEMORY, onPauseAck, planPause } from "./pause.ts";
import type { PauseInput, PauseMemory } from "./pause.ts";

function memory(overrides: Partial<PauseMemory> = {}): PauseMemory {
  return { ...INITIAL_PAUSE_MEMORY, ...overrides };
}

function input(overrides: Partial<PauseInput> = {}): PauseInput {
  return { want: true, stepKey: "T1", paused: false, sailing: true, canSend: true, ...overrides };
}

describe("planPause", () => {
  const cases: readonly {
    name: string;
    memory: PauseMemory;
    input: PauseInput;
    send: boolean | null;
    expect: Partial<PauseMemory>;
  }[] = [
    {
      name: "止めたい・動いている・持っていない → 止める",
      memory: memory(),
      input: input(),
      send: true,
      expect: { owned: true, expected: true },
    },
    {
      name: "止めたい・expected: true・まだ動いている → 送らない",
      memory: memory({ owned: true, expected: true }),
      input: input(),
      send: null,
      expect: { owned: true, expected: true },
    },
    {
      name: "止めたい・止まった → expected: null",
      memory: memory({ owned: true, expected: true }),
      input: input({ paused: true }),
      send: null,
      expect: { owned: true, expected: null },
    },
    {
      name: "止めたくない・持っている・止まっている → 再開",
      memory: memory({ owned: true }),
      input: input({ want: false, paused: true }),
      send: false,
      expect: { owned: false, expected: false },
    },
    {
      name: "止めたくない・持っていない・止まっている（プレイヤーの ⏸）→ 送らない",
      memory: memory(),
      input: input({ want: false, paused: true }),
      send: null,
      expect: { owned: false, expected: null },
    },
    {
      name: "止めたい・プレイヤーが止めている → 送らず、持たない",
      memory: memory(),
      input: input({ paused: true }),
      send: null,
      expect: { owned: false, expected: null },
    },
  ];

  it.each(cases)("PA-1 $name", (c) => {
    const planned = planPause(c.memory, c.input);
    expect(planned.send).toBe(c.send);
    expect(planned.memory).toMatchObject(c.expect);
  });

  it("PA-2 持っていて時計が動き出した → 忘れて、同じ段では止め直さない。次の段では止める", () => {
    const first = planPause(memory({ owned: true }), input({ stepKey: "T2" }));
    expect(first.send).toBeNull();
    expect(first.memory).toMatchObject({ owned: false, releasedStep: "T2" });
    const again = planPause(first.memory, input({ stepKey: "T2" }));
    expect(again.send).toBeNull();
    const next = planPause(again.memory, input({ stepKey: "T3" }));
    expect(next.send).toBe(true);
    expect(next.memory.owned).toBe(true);
  });

  it("PA-3 canSend が偽・sailing が偽 → 送らない（sailing が偽なら owned: false）", () => {
    expect(planPause(memory(), input({ canSend: false })).send).toBeNull();
    const notSailing = planPause(
      memory({ owned: true, expected: true }),
      input({ sailing: false }),
    );
    expect(notSailing.send).toBeNull();
    expect(notSailing.memory).toMatchObject({ owned: false, expected: null });
    const ended = planPause(memory({ owned: true }), input({ want: false, sailing: false }));
    expect(ended.send).toBeNull();
  });

  it("PA-4 ack の失敗 → 待ちをやめ、持たず、同じ段では送り直さない", () => {
    const sent = planPause(memory(), input({ stepKey: "T4" }));
    expect(sent.send).toBe(true);
    const failed = onPauseAck(sent.memory, false, "T4");
    expect(failed).toMatchObject({ expected: null, owned: false, failedStep: "T4" });
    expect(planPause(failed, input({ stepKey: "T4" })).send).toBeNull();
    expect(planPause(failed, input({ stepKey: "T5" })).send).toBe(true);
    // 成功なら何も変えない。
    expect(onPauseAck(sent.memory, true, "T4")).toBe(sent.memory);
  });

  it("PA-5 T2 から T3 への切り替え（どちらも止めたい）→ 送らない", () => {
    const owned = memory({ owned: true });
    const planned = planPause(owned, input({ stepKey: "T3", paused: true }));
    expect(planned.send).toBeNull();
    expect(planned.memory).toBe(owned);
  });

  it("自分が解いた段は、広げ直しても止め直さない", () => {
    const released = planPause(
      memory({ owned: true }),
      input({ want: false, stepKey: "T6", paused: true }),
    );
    expect(released.send).toBe(false);
    const shown = planPause(released.memory, input({ stepKey: "T6", paused: false }));
    expect(shown.send).toBeNull();
  });

  it("再開を送った後、表れる前の時計では判断を変えない", () => {
    const released = planPause(memory({ owned: true }), input({ want: false, paused: true }));
    const next = planPause(released.memory, input({ stepKey: "T2", paused: true }));
    expect(next.send).toBeNull();
    const resumed = planPause(next.memory, input({ stepKey: "T2", paused: false }));
    expect(resumed.send).toBe(true);
  });
});
