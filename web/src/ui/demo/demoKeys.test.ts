// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";

import { demoActionOf, fastSpeed, isSpaceActivated } from "./demoKeys.ts";
import type { DemoKeyEvent } from "./demoKeys.ts";

function key(code: string, overrides: Partial<DemoKeyEvent> = {}): DemoKeyEvent {
  return {
    code,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: null,
    ...overrides,
  };
}

function element(html: string, selector: string): Element {
  const host = document.createElement("div");
  host.innerHTML = html;
  document.body.append(host);
  const found = host.querySelector(selector);
  if (found === null) throw new Error(`no ${selector}`);
  return found;
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("demoActionOf（DK-1）", () => {
  it("code でデモ操作を返す", () => {
    expect(demoActionOf(key("KeyD"))).toBe("storm");
    expect(demoActionOf(key("KeyF"))).toBe("fast");
    expect(demoActionOf(key("KeyN"))).toBe("skip");
    expect(demoActionOf(key("Space"))).toBe("pause");
    expect(demoActionOf(key("KeyE"))).toBeNull();
    expect(demoActionOf(key("KeyQ"))).toBeNull();
  });

  it("Shift は D の判定に影響しない（DemoKeyEvent に shiftKey が無くても storm）", () => {
    const withShift = { ...key("KeyD"), shiftKey: true } as DemoKeyEvent;
    expect(demoActionOf(withShift)).toBe("storm");
  });

  it("Ctrl・Cmd・Alt と一緒なら null", () => {
    expect(demoActionOf(key("KeyD", { ctrlKey: true }))).toBeNull();
    expect(demoActionOf(key("KeyF", { metaKey: true }))).toBeNull();
    expect(demoActionOf(key("KeyN", { altKey: true }))).toBeNull();
  });

  it("押しっぱなし・IME の変換中・既に処理済みなら null", () => {
    expect(demoActionOf(key("KeyD", { repeat: true }))).toBeNull();
    expect(demoActionOf(key("KeyD", { isComposing: true }))).toBeNull();
    expect(demoActionOf(key("Space", { defaultPrevented: true }))).toBeNull();
  });

  it("入力欄・contenteditable の中なら null", () => {
    expect(demoActionOf(key("KeyD", { target: element("<input>", "input") }))).toBeNull();
    expect(
      demoActionOf(key("KeyD", { target: element("<textarea></textarea>", "textarea") })),
    ).toBeNull();
    expect(
      demoActionOf(key("KeyD", { target: element("<select></select>", "select") })),
    ).toBeNull();
    const editable = element('<div contenteditable="true"><span>文</span></div>', "span");
    expect(demoActionOf(key("KeyD", { target: editable }))).toBeNull();
  });

  it("ボタンにフォーカスがあるとき Space は null、D は storm", () => {
    const button = element("<button>積む</button>", "button");
    expect(demoActionOf(key("Space", { target: button }))).toBeNull();
    expect(demoActionOf(key("KeyD", { target: button }))).toBe("storm");
  });

  it("role=button の div・a[href] のとき Space は null", () => {
    expect(
      demoActionOf(key("Space", { target: element('<div role="button">x</div>', "div") })),
    ).toBeNull();
    expect(demoActionOf(key("Space", { target: element('<a href="#x">x</a>', "a") }))).toBeNull();
  });
});

describe("fastSpeed（DK-2）", () => {
  it("Shift なら 8、なければ 4。無ければ 4、それも無ければ null", () => {
    expect(fastSpeed(false, [0.5, 1, 2, 4, 8])).toBe(4);
    expect(fastSpeed(true, [0.5, 1, 2, 4, 8])).toBe(8);
    expect(fastSpeed(true, [0.5, 1, 2, 4])).toBe(4);
    expect(fastSpeed(false, [0.5, 1, 2])).toBeNull();
    expect(fastSpeed(true, [0.5, 1, 2])).toBeNull();
  });
});

describe("isSpaceActivated（DK-3）", () => {
  it("Space で押される要素なら真", () => {
    expect(isSpaceActivated(element("<button>x</button>", "button"))).toBe(true);
    expect(isSpaceActivated(element('<a href="#x">x</a>', "a"))).toBe(true);
    expect(isSpaceActivated(element("<details><summary>x</summary></details>", "summary"))).toBe(
      true,
    );
    expect(isSpaceActivated(element('<input type="checkbox">', "input"))).toBe(true);
    expect(isSpaceActivated(element('<div role="tab">x</div>', "div"))).toBe(true);
  });

  it("それ以外は偽", () => {
    expect(isSpaceActivated(element("<div>x</div>", "div"))).toBe(false);
    expect(isSpaceActivated(element("<a>x</a>", "a"))).toBe(false);
    expect(isSpaceActivated(document.body)).toBe(false);
    expect(isSpaceActivated(null)).toBe(false);
  });
});
