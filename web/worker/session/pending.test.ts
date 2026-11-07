import { describe, expect, it } from "vitest";

import { PENDING_COMMAND_TTL_MS } from "./limits.ts";
import { PendingCommands } from "./pending.ts";

describe("R-5 PendingCommands", () => {
  it("has・take・size・bytes", () => {
    const pending = new PendingCommands();
    expect(pending.size).toBe(0);
    expect(pending.bytes).toBe(0);
    pending.add("c1", { cmd: "a", bytes: 10 }, 0);
    pending.add("c2", { cmd: "b", bytes: 5 }, 0);
    expect(pending.has("c1")).toBe(true);
    expect(pending.has("c3")).toBe(false);
    expect(pending.size).toBe(2);
    expect(pending.bytes).toBe(15);

    expect(pending.take("c1")).toEqual({ cmd: "a", bytes: 10 });
    expect(pending.has("c1")).toBe(false);
    expect(pending.take("c1")).toBeUndefined();
    expect(pending.size).toBe(1);
    expect(pending.bytes).toBe(5);
  });

  it("purge は 30 秒を過ぎたものだけを捨てる", () => {
    const pending = new PendingCommands();
    pending.add("old", { cmd: "a", bytes: 3 }, 1000);
    pending.add("new", { cmd: "b", bytes: 4 }, 2000);
    pending.purge(1000 + PENDING_COMMAND_TTL_MS - 1);
    expect(pending.size).toBe(2);
    pending.purge(1000 + PENDING_COMMAND_TTL_MS);
    expect(pending.has("old")).toBe(false);
    expect(pending.has("new")).toBe(true);
    expect(pending.bytes).toBe(4);
  });

  it("clear", () => {
    const pending = new PendingCommands();
    pending.add("c1", { cmd: "a", bytes: 3 }, 0);
    pending.clear();
    expect(pending.size).toBe(0);
    expect(pending.bytes).toBe(0);
    expect(pending.has("c1")).toBe(false);
  });
});
