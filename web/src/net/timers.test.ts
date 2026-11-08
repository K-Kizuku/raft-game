import { describe, expect, it } from "vitest";

import { createManualTimers } from "./timers.ts";

describe("createManualTimers", () => {
  it("TM-1: 期限の順（同じなら登録の順）に実行し、実行中の now() は期限の時刻", () => {
    const timers = createManualTimers(1000);
    const log: string[] = [];
    timers.setTimeout(() => log.push(`b@${timers.now()}`), 20);
    timers.setTimeout(() => log.push(`a@${timers.now()}`), 10);
    timers.setTimeout(() => log.push(`c@${timers.now()}`), 20);
    const cancelled = timers.setTimeout(() => log.push("x"), 5);
    timers.clearTimeout(cancelled);
    timers.advance(15);
    expect(log).toEqual(["a@1010"]);
    expect(timers.now()).toBe(1015);
    timers.advance(100);
    expect(log).toEqual(["a@1010", "b@1020", "c@1020"]);
    expect(timers.now()).toBe(1115);

    timers.setTimeout(() => {
      log.push(`z@${timers.now()}`);
      timers.setTimeout(() => log.push(`z2@${timers.now()}`), 0);
    }, 0);
    timers.advance(0);
    expect(log.slice(3)).toEqual(["z@1115", "z2@1115"]);
    expect(timers.pendingCount()).toBe(0);
  });

  it("TM-2: setInterval は advance(1000) で 1000/ms 回実行され、clearInterval で止まる", () => {
    const timers = createManualTimers();
    let count = 0;
    const id = timers.setInterval(() => count++, 100);
    timers.setTimeout(() => {}, 5000);
    expect(timers.pendingCount()).toBe(2);
    timers.advance(1000);
    expect(count).toBe(10);
    timers.clearInterval(id);
    expect(timers.pendingCount()).toBe(1);
    timers.advance(1000);
    expect(count).toBe(10);
  });

  it("TM-3: 自分を 0 ms で登録し続けるタイマーで advance が投げる", () => {
    const timers = createManualTimers();
    const loop = () => {
      timers.setTimeout(loop, 0);
    };
    timers.setTimeout(loop, 0);
    expect(() => timers.advance(0)).toThrow(Error);
  });
});
