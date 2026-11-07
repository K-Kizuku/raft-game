// @vitest-environment jsdom
import { describe, expect, it } from "vitest";

import { isEngineerToggleKey } from "./engineerKey.ts";
import type { ToggleKeyEvent } from "./engineerKey.ts";

function keyEvent(overrides: Partial<ToggleKeyEvent> = {}): ToggleKeyEvent {
  return {
    key: "e",
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    repeat: false,
    isComposing: false,
    target: null,
    ...overrides,
  };
}

function editable(): HTMLElement {
  const div = document.createElement("div");
  div.setAttribute("contenteditable", "true");
  const span = document.createElement("span");
  div.append(span);
  document.body.append(div);
  return div;
}

describe("isEngineerToggleKey", () => {
  const cases: { name: string; event: () => ToggleKeyEvent; want: boolean }[] = [
    { name: "e", event: () => keyEvent(), want: true },
    { name: "E（Shift）", event: () => keyEvent({ key: "E" }), want: true },
    { name: "Ctrl+e", event: () => keyEvent({ ctrlKey: true }), want: false },
    { name: "Cmd+e", event: () => keyEvent({ metaKey: true }), want: false },
    { name: "Alt+e", event: () => keyEvent({ altKey: true }), want: false },
    { name: "押しっぱなし", event: () => keyEvent({ repeat: true }), want: false },
    { name: "IME の変換中", event: () => keyEvent({ isComposing: true }), want: false },
    { name: "d", event: () => keyEvent({ key: "d" }), want: false },
    { name: "Enter", event: () => keyEvent({ key: "Enter" }), want: false },
    {
      name: "input",
      event: () => keyEvent({ target: document.createElement("input") }),
      want: false,
    },
    {
      name: "textarea",
      event: () => keyEvent({ target: document.createElement("textarea") }),
      want: false,
    },
    {
      name: "select",
      event: () => keyEvent({ target: document.createElement("select") }),
      want: false,
    },
    { name: "contenteditable", event: () => keyEvent({ target: editable() }), want: false },
    {
      name: "contenteditable の中の要素",
      event: () => keyEvent({ target: editable().firstElementChild }),
      want: false,
    },
    {
      name: "button",
      event: () => keyEvent({ target: document.createElement("button") }),
      want: true,
    },
    { name: "document.body", event: () => keyEvent({ target: document.body }), want: true },
    { name: "target が null", event: () => keyEvent({ target: null }), want: true },
  ];

  it.each(cases)("EK-1 $name → $want", ({ event, want }) => {
    expect(isEngineerToggleKey(event())).toBe(want);
  });
});
