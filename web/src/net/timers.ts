// 時刻とタイマーの差し込み口（T-402）。net のコードはここからだけ時刻とタイマーを得る。

export type TimerId = number;

export type Timers = {
  now(): number; // ミリ秒。単調に増える（ブラウザでは performance.now()）
  setTimeout(fn: () => void, ms: number): TimerId;
  clearTimeout(id: TimerId): void;
  setInterval(fn: () => void, ms: number): TimerId;
  clearInterval(id: TimerId): void;
};

// browserTimers は performance.now と globalThis の setTimeout などを使う。
export const browserTimers: Timers = {
  now: () => performance.now(),
  setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms) as unknown as TimerId,
  clearTimeout: (id) => globalThis.clearTimeout(id),
  setInterval: (fn, ms) => globalThis.setInterval(fn, ms) as unknown as TimerId,
  clearInterval: (id) => globalThis.clearInterval(id),
};

export type ManualTimers = Timers & {
  advance(ms: number): void; // 時刻を ms 進め、その間に期限が来たタイマーを順に実行する
  pendingCount(): number; // 待っているタイマーの数（setInterval を含む）
};

const MAX_RUNS_PER_ADVANCE = 100_000;

type ManualTimer = {
  id: TimerId;
  fn: () => void;
  due: number;
  order: number; // 登録（置き直し）の順
  interval: number | null;
};

// createManualTimers はテストと開発用の決定的なタイマー。start の既定は 0。
export function createManualTimers(start = 0): ManualTimers {
  let now = start;
  let nextId = 1;
  let nextOrder = 1;
  const timers = new Map<TimerId, ManualTimer>();

  function add(fn: () => void, ms: number, interval: boolean): TimerId {
    const id = nextId++;
    const delay = interval ? Math.max(1, ms) : Math.max(0, ms);
    timers.set(id, {
      id,
      fn,
      due: now + delay,
      order: nextOrder++,
      interval: interval ? delay : null,
    });
    return id;
  }

  function nextDue(limit: number): ManualTimer | null {
    let best: ManualTimer | null = null;
    for (const timer of timers.values()) {
      if (timer.due > limit) {
        continue;
      }
      if (
        best === null ||
        timer.due < best.due ||
        (timer.due === best.due && timer.order < best.order)
      ) {
        best = timer;
      }
    }
    return best;
  }

  return {
    now: () => now,
    setTimeout: (fn, ms) => add(fn, ms, false),
    clearTimeout: (id) => {
      timers.delete(id);
    },
    setInterval: (fn, ms) => add(fn, ms, true),
    clearInterval: (id) => {
      timers.delete(id);
    },
    advance(ms) {
      const target = now + Math.max(0, ms);
      let runs = 0;
      for (;;) {
        const timer = nextDue(target);
        if (timer === null) {
          break;
        }
        runs++;
        if (runs > MAX_RUNS_PER_ADVANCE) {
          throw new Error("createManualTimers: too many timers in one advance");
        }
        now = timer.due;
        if (timer.interval === null) {
          timers.delete(timer.id);
        } else {
          timer.due = now + timer.interval;
          timer.order = nextOrder++;
        }
        timer.fn();
      }
      now = target;
    },
    pendingCount: () => timers.size,
  };
}
