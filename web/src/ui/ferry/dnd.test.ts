import { describe, expect, it } from "vitest";

import { CARGO_DROP_ID, PORT_DROP_ID, RELAY_DROP_ID, dragId, dropCommand } from "./dnd.ts";

describe("dropCommand", () => {
  it.each<[string, unknown, string | number | null, ReturnType<typeof dropCommand>]>([
    ["港 → 積み荷", { letterId: 2, source: "port" }, CARGO_DROP_ID, { op: "load", letterIds: [2] }],
    [
      "積み荷 → 港",
      { letterId: 7, source: "cargo" },
      PORT_DROP_ID,
      { op: "unload", letterIds: [7] },
    ],
    ["港 → 港", { letterId: 2, source: "port" }, PORT_DROP_ID, null],
    ["積み荷 → 積み荷", { letterId: 7, source: "cargo" }, CARGO_DROP_ID, null],
    ["港 → 外（null）", { letterId: 2, source: "port" }, null, null],
    ["積み荷 → 外（null）", { letterId: 7, source: "cargo" }, null, null],
    ["港 → 知らない ID", { letterId: 2, source: "port" }, "map", null],
    ["港 → 数の ID", { letterId: 2, source: "port" }, 3, null],
    ["data が undefined", undefined, CARGO_DROP_ID, null],
    ["data が null", null, CARGO_DROP_ID, null],
    ["letterId が文字列", { letterId: "2", source: "port" }, CARGO_DROP_ID, null],
    ["letterId だけ", { letterId: 2 }, CARGO_DROP_ID, null],
    ["source が sea", { letterId: 2, source: "sea" }, CARGO_DROP_ID, null],
  ])("DD-1 %s", (_name, data, overId, want) => {
    expect(dropCommand("f1", data, overId)).toEqual(want);
  });

  it.each<[string, unknown, string | number | null, ReturnType<typeof dropCommand>]>([
    [
      "倉庫 → 積み荷",
      { letterId: 30, source: "relay" },
      CARGO_DROP_ID,
      { op: "relayLoad", letterIds: [30] },
    ],
    [
      "積み荷 → 倉庫",
      { letterId: 7, source: "cargo" },
      RELAY_DROP_ID,
      { op: "unload", letterIds: [7] },
    ],
    [
      "積み荷 → 港",
      { letterId: 7, source: "cargo" },
      PORT_DROP_ID,
      { op: "unload", letterIds: [7] },
    ],
    ["港 → 倉庫", { letterId: 2, source: "port" }, RELAY_DROP_ID, null],
    ["倉庫 → 港", { letterId: 30, source: "relay" }, PORT_DROP_ID, null],
    ["倉庫 → 倉庫", { letterId: 30, source: "relay" }, RELAY_DROP_ID, null],
    ["倉庫 → 外（null）", { letterId: 30, source: "relay" }, null, null],
  ])("DN-R1 %s", (_name, data, overId, want) => {
    expect(dropCommand("f1", data, overId)).toEqual(want);
  });

  it("DD-1 dragId は港と積み荷で違う", () => {
    expect(dragId({ letterId: 2, source: "port" })).toBe("letter:port:2");
    expect(dragId({ letterId: 2, source: "cargo" })).toBe("letter:cargo:2");
    expect(dragId({ letterId: 2, source: "relay" })).toBe("letter:relay:2");
    expect(CARGO_DROP_ID).not.toBe(PORT_DROP_ID);
    expect(RELAY_DROP_ID).toBe("ferry-relay");
  });
});
