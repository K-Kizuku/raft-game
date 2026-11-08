import { describe, expect, it } from "vitest";

import { queueReplayInput, takeQueuedReplayInput } from "./pending.ts";

describe("pending", () => {
  it("RS-12 入れた入力ログを 1 回だけ取り出せる", () => {
    expect(takeQueuedReplayInput()).toBeNull();
    queueReplayInput('{"a":1}');
    expect(takeQueuedReplayInput()).toBe('{"a":1}');
    expect(takeQueuedReplayInput()).toBeNull();
  });

  it("後から入れたもので置き換える", () => {
    queueReplayInput("first");
    queueReplayInput("second");
    expect(takeQueuedReplayInput()).toBe("second");
    expect(takeQueuedReplayInput()).toBeNull();
  });
});
