import { afterEach, describe, expect, it, vi } from "vitest";

import { engineerModeStore } from "./engineerMode.ts";

afterEach(() => {
  engineerModeStore.getState().setOpen(false);
});

describe("engineerModeStore", () => {
  it("EM-1 初期値は閉じていて、setOpen(true) で開く。同じ値の setOpen では購読者を呼ばない", () => {
    expect(engineerModeStore.getState().open).toBe(false);
    const listener = vi.fn();
    const unsubscribe = engineerModeStore.subscribe(listener);
    engineerModeStore.getState().setOpen(true);
    expect(engineerModeStore.getState().open).toBe(true);
    expect(listener).toHaveBeenCalledTimes(1);
    engineerModeStore.getState().setOpen(true);
    expect(listener).toHaveBeenCalledTimes(1);
    engineerModeStore.getState().setOpen(false);
    expect(engineerModeStore.getState().open).toBe(false);
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
  });
});
