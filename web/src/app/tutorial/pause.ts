// チュートリアルの吹き出しの間の自動の一時停止を決める純粋な関数（T-409・docs/05-game-progression.md §7）。
// 送るのは cmd.game.pause だけ。自分が止めたものだけを解き、プレイヤーが止めた一時停止は解かない。

export type PauseMemory = Readonly<{
  owned: boolean; // 自分が止めた（止める送信をした）
  expected: boolean | null; // 送った値が時計に表れるのを待っている（null なら待っていない）
  releasedStep: string | null; // この段では止め直さない（▶ で再開された・自分で解いた）
  failedStep: string | null; // この段では送り直さない（ack が失敗した）
}>;

export type PauseInput = Readonly<{
  want: boolean; // 止めたい（autoPause・吹き出しが広がっている・航行中）
  stepKey: string | null; // 今の段（畳んでいても入れる。吹き出しが無ければ null）
  paused: boolean; // world.clock.paused
  sailing: boolean; // world.phase === "sailing"
  canSend: boolean; // useGameCommand() が null でない
}>;

export type PausePlan = Readonly<{ memory: PauseMemory; send: boolean | null }>;

export const INITIAL_PAUSE_MEMORY: PauseMemory = {
  owned: false,
  expected: null,
  releasedStep: null,
  failedStep: null,
};

function same(a: PauseMemory, b: PauseMemory): boolean {
  return (
    a.owned === b.owned &&
    a.expected === b.expected &&
    a.releasedStep === b.releasedStep &&
    a.failedStep === b.failedStep
  );
}

function plan(before: PauseMemory, after: PauseMemory, send: boolean | null): PausePlan {
  return { memory: same(before, after) ? before : after, send };
}

// planPause は今の入力で送るものを決める。send: true=止める、false=再開、null=送らない。
export function planPause(memory: PauseMemory, input: PauseInput): PausePlan {
  // 航行中でなければ送らない（ラウンドが終わったら再開も送らない）。
  if (!input.sailing) return plan(memory, { ...memory, owned: false, expected: null }, null);
  let m = memory;
  // 送った値が時計に表れるまでは、時計の値で判断を変えない。
  if (m.expected !== null) {
    if (input.paused !== m.expected) return plan(memory, m, null);
    m = { ...m, expected: null };
  }
  // 自分が止めた後に時計が動き出した（プレイヤーの ▶）→ 忘れて、この段では止め直さない。
  if (m.owned && !input.paused) {
    m = { ...m, owned: false, releasedStep: input.stepKey };
  }
  if (input.want) {
    if (
      !input.canSend ||
      m.owned ||
      input.paused ||
      input.stepKey === m.releasedStep ||
      input.stepKey === m.failedStep
    ) {
      return plan(memory, m, null);
    }
    return plan(memory, { ...m, owned: true, expected: true }, true);
  }
  // 止めたくない。自分が止めていれば解く（プレイヤーの一時停止は解かない）。
  if (m.owned && input.canSend) {
    return plan(
      memory,
      { ...m, owned: false, expected: false, releasedStep: input.stepKey },
      false,
    );
  }
  return plan(memory, m, null);
}

// onPauseAck は送った cmd.game.pause の結果。失敗なら待ちをやめ、自分が止めたことを忘れ、
// その段では送り直さない。
export function onPauseAck(memory: PauseMemory, ok: boolean, stepKey: string | null): PauseMemory {
  if (ok) return memory;
  return { ...memory, owned: false, expected: null, failedStep: stepKey };
}
