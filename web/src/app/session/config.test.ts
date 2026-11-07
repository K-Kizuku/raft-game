import { describe, expect, it } from "vitest";

import { turnstileSiteKeyFrom } from "./config.ts";

const KEY = "1x00000000000000000000AA";

describe("turnstileSiteKeyFrom", () => {
  it.each<[string, string | undefined]>([
    ["undefined", undefined],
    ["空", ""],
    ["空白だけ", "   "],
    ["途中に空白", "a b"],
    ["129 文字", "a".repeat(129)],
    ["記号", "<x>"],
  ])("C-1 %s → null", (_label, value) => {
    expect(turnstileSiteKeyFrom(value)).toBeNull();
  });

  it.each<[string, string]>([
    ["そのまま", KEY],
    ["前後に空白", `  ${KEY}\n`],
    ["128 文字", "a".repeat(128)],
  ])("C-1 %s → 値", (_label, value) => {
    expect(turnstileSiteKeyFrom(value)).toBe(value.trim());
  });
});
