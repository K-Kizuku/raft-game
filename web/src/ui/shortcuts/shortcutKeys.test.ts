// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";

import { shortcutActionOf } from "./shortcutKeys.ts";
import type { ShortcutKeyEvent } from "./shortcutKeys.ts";

function key(code: string, overrides: Partial<ShortcutKeyEvent> = {}): ShortcutKeyEvent {
  return {
    code,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: document.body,
    ...overrides,
  };
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("shortcutActionOf", () => {
  it("SK-1 Space はデモでなければ pause、デモなら null", () => {
    expect(shortcutActionOf(key("Space"), false)).toEqual({ kind: "pause" });
    expect(shortcutActionOf(key("Space"), true)).toBeNull();
  });

  it("SK-2 Digit1〜4・Numpad1〜4 は ferry の 0〜3、ほかは null", () => {
    for (const demo of [false, true]) {
      for (let i = 0; i < 4; i += 1) {
        const n = String(i + 1);
        expect(shortcutActionOf(key(`Digit${n}`), demo)).toEqual({ kind: "ferry", index: i });
        expect(shortcutActionOf(key(`Numpad${n}`), demo)).toEqual({ kind: "ferry", index: i });
      }
      expect(shortcutActionOf(key("Digit5"), demo)).toBeNull();
      expect(shortcutActionOf(key("Digit0"), demo)).toBeNull();
      expect(shortcutActionOf(key("Numpad5"), demo)).toBeNull();
      expect(shortcutActionOf(key("KeyE"), demo)).toBeNull();
      expect(shortcutActionOf(key("KeyD"), demo)).toBeNull();
      expect(shortcutActionOf(key("toString"), demo)).toBeNull();
    }
  });

  it("SK-3 修飾キー・押しっぱなし・IME の変換中・既定の動きが止められたときは null", () => {
    const flags = [
      "ctrlKey",
      "metaKey",
      "altKey",
      "repeat",
      "isComposing",
      "defaultPrevented",
    ] as const;
    for (const flag of flags) {
      expect(shortcutActionOf(key("Space", { [flag]: true }), false)).toBeNull();
      expect(shortcutActionOf(key("Digit1", { [flag]: true }), false)).toBeNull();
    }
    // Shift は判定に関係ない（型に含めない）。Shift+1 の keydown をそのまま渡しても船 1。
    const shifted = new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true });
    expect(shortcutActionOf(shifted, false)).toEqual({ kind: "ferry", index: 0 });
  });

  it("SK-4 入力欄・contenteditable の中では null", () => {
    const input = document.createElement("input");
    const textarea = document.createElement("textarea");
    const select = document.createElement("select");
    const editable = document.createElement("div");
    editable.setAttribute("contenteditable", "true");
    const inner = document.createElement("span");
    editable.append(inner);
    document.body.append(input, textarea, select, editable);
    for (const target of [input, textarea, select, editable, inner]) {
      expect(shortcutActionOf(key("Space", { target }), false)).toBeNull();
      expect(shortcutActionOf(key("Digit1", { target }), false)).toBeNull();
    }
  });

  it("SK-5 ボタンにフォーカスがあるとき Space は null、Digit1 は ferry", () => {
    const button = document.createElement("button");
    const link = document.createElement("a");
    link.href = "#x";
    const tab = document.createElement("div");
    tab.setAttribute("role", "button");
    document.body.append(button, link, tab);
    for (const target of [button, link, tab]) {
      expect(shortcutActionOf(key("Space", { target }), false)).toBeNull();
      expect(shortcutActionOf(key("Digit1", { target }), false)).toEqual({
        kind: "ferry",
        index: 0,
      });
    }
  });
});
