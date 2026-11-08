// @vitest-environment jsdom
import type * as DndKit from "@dnd-kit/core";
import type { DndContextProps, DragEndEvent } from "@dnd-kit/core";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createElement } from "react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Ack, ServerEvent, Snapshot } from "../../protocol/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { gameStore } from "../../state/index.ts";
import { GameCommandProvider } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { setShowRaftTerms } from "../terms/preference.ts";
import { CARGO_DROP_ID, PORT_DROP_ID, RELAY_DROP_ID } from "./dnd.ts";
import { FerryPanel } from "./FerryPanel.tsx";
import { pressIsland } from "./islandPress.ts";
import {
  ferryLetter,
  makeFerrySnapshot,
  portLetter,
  relayLetter,
  withFerry1,
  withRelayCap,
} from "./testing/world.ts";

// jsdom にドラッグの座標の計算は無いので、DndContext の onDragEnd だけを捕まえる（元の DndContext を使う）。
const dnd = vi.hoisted(() => ({
  onDragEnd: null as ((event: DragEndEvent) => void) | null,
  accessibility: undefined as DndContextProps["accessibility"],
}));
vi.mock("@dnd-kit/core", async (importOriginal) => {
  const actual = await importOriginal<typeof DndKit>();
  function DndContext(props: DndContextProps): ReactElement {
    dnd.onDragEnd = props.onDragEnd ?? null;
    dnd.accessibility = props.accessibility;
    return createElement(actual.DndContext, props);
  }
  return { ...actual, DndContext };
});

const OK: Ack = { cmdId: "c-1", ok: true, t: 29000 };

let nowMs = 1000;
let seq = 0;

beforeEach(() => {
  gameStore.getState().reset();
  nowMs = 1000;
  seq = 0;
  dnd.onDragEnd = null;
  dnd.accessibility = undefined;
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
  vi.spyOn(performance, "now").mockImplementation(() => nowMs);
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  vi.useRealTimers();
  vi.restoreAllMocks();
  setShowRaftTerms(false);
});

function load(snapshot: Snapshot = makeFerrySnapshot(), ferryId: string | null = "f1"): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, performance.now());
    gameStore.getState().selectFerry(ferryId);
  });
}

type EventBody = ServerEvent extends infer E
  ? E extends ServerEvent
    ? Omit<E, "seq">
    : never
  : never;

function events(...bodies: EventBody[]): void {
  const list = bodies.map((body) => ({ ...body, seq: ++seq }) as ServerEvent);
  act(() => {
    gameStore
      .getState()
      .applyEvents({ t: 29000, speed: 1, paused: false, endsAt: 180000 }, list, performance.now());
  });
}

function moved(
  letterId: number,
  loc: { type: "ferry"; ferryId: string } | { type: "port"; islandId: number },
): EventBody {
  return { type: "letter.moved", t: 29000, letterId, loc };
}

function okSender(): ReturnType<typeof vi.fn<GameCommandSender>> {
  return vi.fn<GameCommandSender>(() => Promise.resolve(OK));
}

function renderPanel(send: GameCommandSender | null): ReturnType<typeof render> {
  return render(
    <GameCommandProvider send={send}>
      <FerryPanel />
    </GameCommandProvider>,
  );
}

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

function button(name: string | RegExp): HTMLElement {
  return screen.getByRole("button", { name });
}

function autoloadButton(): HTMLElement {
  const el = document.querySelector<HTMLElement>(".ferry-panel__autoload");
  if (el === null) throw new Error("おまかせのボタンが無い");
  return el;
}

function departButton(): HTMLElement {
  const el = document.querySelector<HTMLElement>(".ferry-panel__depart");
  if (el === null) throw new Error("出航のボタンが無い");
  return el;
}

function cargoList(): HTMLElement {
  const el = document.querySelector<HTMLElement>(".ferry-panel__cargo");
  if (el === null) throw new Error("積み荷が無い");
  return el;
}

function portArea(): HTMLElement {
  const el = document.querySelector<HTMLElement>(".ferry-panel__port");
  if (el === null) throw new Error("港が無い");
  return el;
}

function statusText(): string | null | undefined {
  return document.querySelector(".ferry-panel__status")?.textContent;
}

// 文字が複数の要素に分かれていても、textContent が一致する一番内側の要素を探す。
function byText(text: string): HTMLElement[] {
  return screen.queryAllByText(
    (_content, el) =>
      el !== null &&
      el.textContent === text &&
      ![...el.children].some((child) => child.textContent === text),
  );
}

function describedText(el: HTMLElement): string {
  const ids = (el.getAttribute("aria-describedby") ?? "").split(" ").filter((id) => id !== "");
  return ids.map((id) => document.getElementById(id)?.textContent ?? "").join(" ");
}

const HEARTBEAT_LOAD = "お元気ですか？（→ 二の島）を積む";
const APPEND_LOAD = "この内容を記録して！（→ 三の島）を積む";
const REQUEST_LOAD = "住民の依頼書（→ 三の島）を積む";

// f1 に手紙を積んだ Snapshot（letters は差し替え、f1 は島 1 に停泊）。
function loadedSnapshot(cargo: { id: number; from: number; to: number }[]): Snapshot {
  const base = makeFerrySnapshot();
  const onBoard = cargo.map((c) => ferryLetter(c.id, "heartbeat", c.from, c.to, "f1"));
  return withFerry1(
    { cargo: cargo.map((c) => c.id) },
    { letters: [...base.letters.filter((l) => !cargo.some((c) => c.id === l.id)), ...onBoard] },
  );
}

describe("FerryPanel", () => {
  it("FP-1 world が null なら「フェリー操作（準備中）」、reset で戻る", () => {
    render(<FerryPanel />);
    expect(screen.getByText("フェリー操作（準備中）")).toBeDefined();
    load(makeFerrySnapshot(), null);
    expect(screen.queryByText("フェリー操作（準備中）")).toBeNull();
    act(() => {
      gameStore.getState().reset();
    });
    expect(screen.getByText("フェリー操作（準備中）")).toBeDefined();
  });

  it("FP-2 船の一覧と選択", () => {
    load(makeFerrySnapshot(), null);
    render(<FerryPanel />);
    const group = screen.getByRole("group", { name: "船を選ぶ" });
    const tabs = within(group).getAllByRole("button");
    expect(tabs.map((t) => t.querySelector(".ferry-panel__tab-name")?.textContent)).toEqual([
      "⛴ ごとう丸",
      "⛴ しまかぜ",
    ]);
    expect(byText("⚓ 一の島 停泊中")).toHaveLength(1);
    expect(byText("→ 三の島 あと 0:02")).toHaveLength(1);
    expect(screen.getByText("積み荷 0/4")).toBeDefined();
    expect(screen.getByText("積み荷 1/4")).toBeDefined();
    expect(screen.getByText("船を選ぶと、積み込みと出航の操作がここに出ます。")).toBeDefined();
    expect(tabs[0]?.getAttribute("aria-pressed")).toBe("false");

    fireEvent.click(button(/ごとう丸/));
    expect(gameStore.getState().selection.ferryId).toBe("f1");
    expect(button(/ごとう丸/).getAttribute("aria-pressed")).toBe("true");
    expect(button(/しまかぜ/).getAttribute("aria-pressed")).toBe("false");
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("ごとう丸 ⚓ 一の島 停泊中");
  });

  it("FP-2 船が 0 隻・故障中", () => {
    load(makeFerrySnapshot({ ferries: [] }), null);
    const { unmount } = render(<FerryPanel />);
    expect(screen.getByText("船がありません。")).toBeDefined();
    unmount();
    load(makeFerrySnapshot(), "f1");
    events({ type: "ferry.broken", t: 29000, ferryId: "f1", until: 60000 });
    render(<FerryPanel />);
    expect(within(button(/ごとう丸/)).getByText("故障中")).toBeDefined();
  });

  it("FP-3 港の手紙を押して積む: 表示はイベントで変わる", async () => {
    load();
    const send = okSender();
    renderPanel(send);
    expect(within(cargoList()).getAllByText("空き")).toHaveLength(4);

    fireEvent.click(button(HEARTBEAT_LOAD));
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith("cmd.ferry.load", { ferryId: "f1", letterIds: [2] });
    await flush();

    // 押しただけでは港のまま（反映待ち）。
    const waiting = within(portArea()).getByRole("button", { name: HEARTBEAT_LOAD });
    expect(waiting.getAttribute("aria-disabled")).toBe("true");
    expect(within(portArea()).getByText("反映待ち")).toBeDefined();
    expect(statusText()).toBe("手紙を積みました。");
    fireEvent.click(waiting);
    expect(send).toHaveBeenCalledTimes(1);
    // ほかの手紙は押せる。
    expect(button(APPEND_LOAD).getAttribute("aria-disabled")).toBeNull();

    events(moved(2, { type: "ferry", ferryId: "f1" }));
    expect(within(portArea()).queryByRole("button", { name: HEARTBEAT_LOAD })).toBeNull();
    const slots = within(cargoList()).getAllByRole("listitem");
    expect(
      within(slots[0] ?? document.body)
        .getByRole("button")
        .getAttribute("aria-label"),
    ).toBe("お元気ですか？（→ 二の島）を降ろす");
    expect(screen.getByRole("heading", { level: 3, name: "積み荷 1/4" })).toBeDefined();
    expect(within(cargoList()).getAllByText("空き")).toHaveLength(3);
    expect(screen.queryByText("反映待ち")).toBeNull();
  });

  it("FP-4 積み荷を押して降ろす・違う島で生まれた手紙は降ろせない", async () => {
    load(
      loadedSnapshot([
        { id: 30, from: 1, to: 3 },
        { id: 40, from: 2, to: 3 },
      ]),
    );
    const send = okSender();
    renderPanel(send);

    const foreign = within(cargoList()).getAllByRole("button")[1];
    if (foreign === undefined) throw new Error("積み荷の 2 つ目が無い");
    expect(foreign.getAttribute("aria-disabled")).toBe("true");
    expect(foreign.getAttribute("title")).toBe("この島で生まれた手紙だけ降ろせます");
    expect(describedText(foreign)).toContain("この島で生まれた手紙だけ降ろせます");
    fireEvent.click(foreign);
    expect(send).not.toHaveBeenCalled();

    const own = within(cargoList()).getAllByRole("button")[0];
    if (own === undefined) throw new Error("積み荷の 1 つ目が無い");
    expect(own.getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(own);
    expect(send).toHaveBeenCalledWith("cmd.ferry.unload", { ferryId: "f1", letterIds: [30] });
    await flush();
    expect(statusText()).toBe("手紙を港に戻しました。");
    events(moved(30, { type: "port", islandId: 1 }));
    expect(
      within(portArea()).getByRole("button", { name: "お元気ですか？（→ 三の島）を積む" }),
    ).toBeDefined();
    expect(screen.getByRole("heading", { level: 3, name: "積み荷 1/4" })).toBeDefined();
  });

  it("FP-5 行き先を選んでおまかせと出航", async () => {
    load();
    const send = okSender();
    renderPanel(send);
    expect(autoloadButton().getAttribute("aria-disabled")).toBe("true");
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
    expect(screen.getAllByText("行き先を選んでください。")).toHaveLength(2);
    expect(describedText(departButton())).toBe("行き先を選んでください。");
    fireEvent.click(autoloadButton());
    expect(send).not.toHaveBeenCalled();
    expect(screen.getAllByRole("radio").every((r) => !(r as HTMLInputElement).checked)).toBe(true);

    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect((screen.getByRole("radio", { name: "二の島" }) as HTMLInputElement).checked).toBe(true);
    expect(autoloadButton().getAttribute("aria-disabled")).toBeNull();
    expect(departButton().getAttribute("aria-disabled")).toBeNull();
    expect(screen.queryByText("行き先を選んでください。")).toBeNull();
    const pile2 = document.querySelector<HTMLElement>('.ferry-panel__pile[data-to="2"]');
    const pile3 = document.querySelector<HTMLElement>('.ferry-panel__pile[data-to="3"]');
    expect(pile2?.querySelector(".ferry-panel__pile-to")?.textContent).toBe("→ 二の島行き先宛て");
    expect(within(pile2 ?? document.body).getByText("行き先宛て")).toBeDefined();
    expect(within(pile3 ?? document.body).queryByText("行き先宛て")).toBeNull();

    fireEvent.click(autoloadButton());
    expect(send).toHaveBeenLastCalledWith("cmd.ferry.autoload", { ferryId: "f1", to: 2 });
    await flush();
    expect(statusText()).toBe("おまかせで積みました。");

    fireEvent.click(departButton());
    expect(send).toHaveBeenLastCalledWith("cmd.ferry.depart", { ferryId: "f1", to: 2 });
    await flush();
    expect(statusText()).toBe("二の島へ出航しました。");
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("FP-6 停泊の時間が終わるまで出航は押せない", () => {
    load(withFerry1({ readyAt: 30500 }));
    renderPanel(okSender());
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
    expect(describedText(departButton())).toBe("出航まで あと 0:02");
    expect(autoloadButton().getAttribute("aria-disabled")).toBeNull();
    expect(document.querySelector(".ferry-panel__ready")?.textContent).toBe("出航まで あと 0:02");

    nowMs += 1600;
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(departButton().getAttribute("aria-disabled")).toBeNull();
    expect(document.querySelector(".ferry-panel__ready")).toBeNull();
  });

  it("FP-6 一時停止中は残りが減らず、押せないまま", () => {
    const base = withFerry1({ readyAt: 30500 });
    load({ ...base, clock: { ...base.clock, paused: true } });
    renderPanel(okSender());
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    nowMs += 5000;
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
    expect(describedText(departButton())).toBe("出航まで あと 0:02");
  });

  it("FP-7 嵐の航路: 選べておまかせはできるが、出航は押せない", () => {
    load(
      makeFerrySnapshot({
        routesState: [
          { id: "1-2", blockedUntil: 40000 },
          { id: "1-3", blockedUntil: null },
          { id: "2-3", blockedUntil: null },
        ],
      }),
    );
    const send = okSender();
    renderPanel(send);
    const radio = screen.getByRole("radio", { name: "二の島 嵐で通れません" });
    expect(radio.closest("label")?.textContent).toContain("⛈ 嵐で通れません");
    fireEvent.click(radio);
    expect(autoloadButton().getAttribute("aria-disabled")).toBeNull();
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
    expect(describedText(departButton())).toBe("嵐で航路が通れません。");
    fireEvent.click(departButton());
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(autoloadButton());
    expect(send).toHaveBeenCalledWith("cmd.ferry.autoload", { ferryId: "f1", to: 2 });
  });

  it("FP-7 嵐の予報は「☁ 嵐の予報」", () => {
    load();
    events({ type: "forecast", t: 29000, event: "storm", at: 35000, routeIds: ["1-3"] });
    renderPanel(okSender());
    expect(screen.getByRole("radio", { name: "三の島 嵐の予報" })).toBeDefined();
  });

  it("FP-8 航行中の船は操作できない", () => {
    load(makeFerrySnapshot(), "f2");
    renderPanel(okSender());
    expect(byText("→ 三の島 あと 0:02")).toHaveLength(2);
    expect(
      screen.getByText("航行中は操作できません。着いたら積み込みと出航ができます。"),
    ).toBeDefined();
    expect(within(cargoList()).getByText("→ 三の島")).toBeDefined();
    expect(within(cargoList()).queryAllByRole("button")).toHaveLength(0);
    expect(screen.getAllByRole("button")).toHaveLength(2);
    expect(screen.queryByRole("radio")).toBeNull();
    expect(document.querySelector(".ferry-panel__autoload")).toBeNull();
    expect(document.querySelector(".ferry-panel__depart")).toBeNull();
    expect(document.querySelector(".ferry-panel__port")).toBeNull();
    expect(document.querySelector(".ferry-panel__destinations")).toBeNull();
  });

  it("FP-9 Provider の外では押せない", () => {
    load();
    render(<FerryPanel />);
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    for (const el of [button(HEARTBEAT_LOAD), autoloadButton(), departButton()]) {
      expect(el.getAttribute("aria-disabled")).toBe("true");
      expect(el.hasAttribute("disabled")).toBe(false);
      fireEvent.click(el);
    }
    expect(statusText()).toBe("");
  });

  it.each<[Snapshot["phase"], string]>([
    ["intro", "航行の時間になると操作できます。"],
    ["opening", "航行の時間になると操作できます。"],
    ["ended", "ラウンドは終わりました。"],
  ])("FP-9 phase %s では押せず「%s」", (phase, text) => {
    load(makeFerrySnapshot({ phase }));
    const send = okSender();
    renderPanel(send);
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect(screen.getAllByText(text)).toHaveLength(1);
    for (const el of [button(HEARTBEAT_LOAD), autoloadButton(), departButton()]) {
      expect(el.getAttribute("aria-disabled")).toBe("true");
      expect(describedText(el)).toContain(text);
      fireEvent.click(el);
    }
    expect(send).not.toHaveBeenCalled();
  });

  it("FP-9 送っている間はほかの操作も押せず、フォーカスが残る", () => {
    load();
    const send = vi.fn<GameCommandSender>(() => new Promise<Ack>(() => {}));
    renderPanel(send);
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    const first = button(HEARTBEAT_LOAD);
    first.focus();
    fireEvent.click(first);
    expect(send).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(first);
    for (const el of [
      button(HEARTBEAT_LOAD),
      button(APPEND_LOAD),
      autoloadButton(),
      departButton(),
    ]) {
      expect(el.getAttribute("aria-disabled")).toBe("true");
      fireEvent.click(el);
    }
    expect(send).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(first);
  });

  type FailRow = [string, "load" | "autoload" | "depart", () => Promise<Ack>, string];
  it.each<FailRow>([
    [
      "積む・capacity_exceeded",
      "load",
      () => Promise.resolve({ ...OK, ok: false, code: "capacity_exceeded" }),
      "積み荷がいっぱいです。",
    ],
    [
      "積む・letter_not_here",
      "load",
      () => Promise.resolve({ ...OK, ok: false, code: "letter_not_here" }),
      "その手紙はもう港にありません。",
    ],
    [
      "おまかせ・letter_not_here",
      "autoload",
      () => Promise.resolve({ ...OK, ok: false, code: "letter_not_here" }),
      "港に二の島宛ての手紙がありません。",
    ],
    [
      "出航・route_blocked",
      "depart",
      () => Promise.resolve({ ...OK, ok: false, code: "route_blocked" }),
      "嵐で二の島への航路が通れません。",
    ],
    [
      "出航・ferry_not_ready",
      "depart",
      () => Promise.resolve({ ...OK, ok: false, code: "ferry_not_ready" }),
      "停泊の時間がまだ終わっていません。少し待ってください。",
    ],
    [
      "積む・reject",
      "load",
      () => Promise.reject(new Error("closed")),
      "サーバーにつながっていないため、積み込みできませんでした。",
    ],
  ])("FP-10 %s の文", async (_name, op, result, text) => {
    load();
    const send = vi.fn<GameCommandSender>(result);
    renderPanel(send);
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    const target =
      op === "load"
        ? button(HEARTBEAT_LOAD)
        : op === "autoload"
          ? autoloadButton()
          : departButton();
    fireEvent.click(target);
    await flush();
    expect(statusText()).toBe(text);
    // 失敗の後は反映待ちにならず、もう一度押せる。
    expect(target.getAttribute("aria-disabled")).toBeNull();
    expect(screen.queryByText("反映待ち")).toBeNull();
    fireEvent.click(target);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("FP-10 送っている間にアンマウントしても警告・例外が出ない", async () => {
    load();
    const error = vi.spyOn(console, "error");
    let resolve: (ack: Ack) => void = () => {};
    const send = vi.fn<GameCommandSender>(
      () =>
        new Promise<Ack>((r) => {
          resolve = r;
        }),
    );
    const { unmount } = renderPanel(send);
    fireEvent.click(button(HEARTBEAT_LOAD));
    unmount();
    resolve(OK);
    await flush();
    expect(error).not.toHaveBeenCalled();
  });

  it("FP-10 選んだ船が変わったら結果の文を消す", async () => {
    load();
    renderPanel(okSender());
    fireEvent.click(button(HEARTBEAT_LOAD));
    await flush();
    expect(statusText()).toBe("手紙を積みました。");
    fireEvent.click(button(/しまかぜ/));
    expect(statusText()).toBe("");
    fireEvent.click(button(/ごとう丸/));
    expect(statusText()).toBe("");
  });

  it("FP-11 出航・到着したら行き先は選び直し", () => {
    load();
    renderPanel(okSender());
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect(departButton().getAttribute("aria-disabled")).toBeNull();
    events(
      {
        type: "ferry.departed",
        t: 29000,
        ferryId: "f1",
        from: 1,
        to: 2,
        departAt: 29000,
        arriveAt: 35000,
      },
      { type: "ferry.arrived", t: 35000, ferryId: "f1", islandId: 2, readyAt: 29000 },
    );
    const radios = screen.getAllByRole("radio") as HTMLInputElement[];
    expect(radios.map((r) => r.labels?.[0]?.textContent?.trim())).toEqual(["一の島", "三の島"]);
    expect(radios.every((r) => !r.checked)).toBe(true);
    expect(autoloadButton().getAttribute("aria-disabled")).toBe("true");
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
  });

  it("FP-11 別の船を選んで戻ると行き先は選び直し", () => {
    load();
    renderPanel(okSender());
    fireEvent.click(screen.getByRole("radio", { name: "三の島" }));
    fireEvent.click(button(/しまかぜ/));
    fireEvent.click(button(/ごとう丸/));
    expect((screen.getByRole("radio", { name: "三の島" }) as HTMLInputElement).checked).toBe(false);
    expect(departButton().getAttribute("aria-disabled")).toBe("true");
  });

  it("FP-12 積んだ手紙が消えたら、同じ位置・1 つ前・見出しにフォーカスを移す", async () => {
    load();
    renderPanel(okSender());

    const append = button(APPEND_LOAD);
    append.focus();
    fireEvent.click(append);
    await flush();
    events(moved(5, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement).toBe(button(REQUEST_LOAD));

    fireEvent.click(button(REQUEST_LOAD));
    await flush();
    events(moved(9, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement).toBe(button(HEARTBEAT_LOAD));

    fireEvent.click(button(HEARTBEAT_LOAD));
    await flush();
    events(moved(2, { type: "ferry", ferryId: "f1" }));
    const heading = screen.getByRole("heading", { level: 3, name: "一の島の港の手紙" });
    expect(document.activeElement).toBe(heading);
    expect(screen.getByText("港に手紙はありません。")).toBeDefined();
  });

  it("FP-12 積み荷から降ろした手紙が消えたら、積み荷の同じ位置に移す", async () => {
    load(
      loadedSnapshot([
        { id: 30, from: 1, to: 3 },
        { id: 31, from: 1, to: 2 },
      ]),
    );
    renderPanel(okSender());
    const first = within(cargoList()).getAllByRole("button")[0];
    first?.focus();
    if (first === undefined) throw new Error("積み荷が無い");
    fireEvent.click(first);
    await flush();
    events(moved(30, { type: "port", islandId: 1 }));
    expect(document.activeElement?.getAttribute("data-letter-id")).toBe("31");
  });

  it("FP-12 フォーカスがパネルの外にあれば動かさない", async () => {
    load();
    render(
      <>
        <button type="button">外</button>
        <GameCommandProvider send={okSender()}>
          <FerryPanel />
        </GameCommandProvider>
      </>,
    );
    const outside = screen.getByRole("button", { name: "外" });
    outside.focus();
    fireEvent.click(button(APPEND_LOAD));
    await flush();
    events(moved(5, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement).toBe(outside);
  });

  it("FP-13 ドラッグ＆ドロップで積む・外に落としたら何もしない", async () => {
    load();
    const send = okSender();
    renderPanel(send);
    expect(dnd.onDragEnd).not.toBeNull();
    for (const el of within(portArea()).getAllByRole("button")) {
      expect(el.hasAttribute("aria-roledescription")).toBe(false);
    }

    act(() => {
      dnd.onDragEnd?.({
        active: { id: "letter:port:2", data: { current: { letterId: 2, source: "port" } } },
        over: null,
      } as unknown as DragEndEvent);
    });
    expect(send).not.toHaveBeenCalled();

    act(() => {
      dnd.onDragEnd?.({
        active: { id: "letter:port:2", data: { current: { letterId: 2, source: "port" } } },
        over: { id: CARGO_DROP_ID },
      } as unknown as DragEndEvent);
    });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith("cmd.ferry.load", { ferryId: "f1", letterIds: [2] });
    await flush();
    expect(statusText()).toBe("手紙を積みました。");
  });

  it("FP-13 支援技術への日本語の案内", () => {
    load();
    renderPanel(okSender());
    const a11y = dnd.accessibility;
    expect(a11y?.screenReaderInstructions?.draggable).toBe(
      "Enter か Space で積み込み・荷降ろしをします。マウスではドラッグでもできます。",
    );
    const active = {
      id: "letter:port:2",
      data: { current: { letterId: 2, source: "port" } },
    } as unknown as DragEndEvent["active"];
    const over = (id: string): DragEndEvent["over"] => ({ id }) as unknown as DragEndEvent["over"];
    const ann = a11y?.announcements;
    expect(ann?.onDragStart({ active })).toBe("お元気ですか？を持ち上げました。");
    expect(ann?.onDragOver({ active, over: over(CARGO_DROP_ID) })).toBe("積み荷の上です。");
    expect(ann?.onDragOver({ active, over: over("ferry-port") })).toBe("港の上です。");
    expect(ann?.onDragOver({ active, over: null })).toBeUndefined();
    expect(ann?.onDragEnd({ active, over: null })).toBe("お元気ですか？を置きました。");
    expect(ann?.onDragCancel({ active, over: null })).toBe(
      "お元気ですか？のドラッグをやめました。",
    );
  });

  it("FP-14 用語の併記", () => {
    setShowRaftTerms(true);
    load();
    renderPanel(okSender());
    expect(button("お元気ですか？（MsgHeartbeat）（→ 二の島）を積む")).toBeDefined();
    const heading = screen.getByRole("heading", { level: 3, name: /港の/ });
    expect(heading.textContent).toContain("手紙（Message）");
  });

  it("手紙のボタンには portLetter の宛先の色の縁が付く", () => {
    load(makeFerrySnapshot({ letters: [portLetter(2, "heartbeat", 1, 9)] }));
    renderPanel(okSender());
    const btn = button("お元気ですか？（→ 島 9）を積む");
    expect(btn.querySelector('[data-part="border"]')?.getAttribute("stroke")).toBe("#9CA3AF");
  });
});

// 一直線の航路（1-2-3。1-3 は無い）。Stage 1 の直島–豊島–小豆島と同じ形（#318）。
function lineSnapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  const base = makeFerrySnapshot(overrides);
  return {
    ...base,
    stage: { ...base.stage, routes: base.stage.routes.filter((r) => r.id !== "1-3") },
    routesState: base.routesState.filter((r) => r.id !== "1-3"),
  };
}

function currentDestination(): string | null | undefined {
  return document.querySelector(".ferry-panel__destination-current")?.textContent;
}

describe("行き先の選び方と経由の手紙（#318）", () => {
  it("FP-15 行き先を大きく出す: 未選択なら選び方、選ぶと「行き先: 二の島」", () => {
    load(lineSnapshot());
    renderPanel(okSender());
    expect(currentDestination()).toBe(
      "行き先: 未選択 下のボタンか、マップで隣の島を押して選びます",
    );
    expect(screen.getAllByRole("radio").map((r) => r.closest("label")?.textContent)).toEqual([
      " 二の島",
    ]);
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect(currentDestination()).toBe("行き先: 二の島");
  });

  it("FP-16 隣でない島宛ての手紙があると、経由の案内を出し、行き先が経由の島なら山に「行き先経由」", () => {
    load(lineSnapshot());
    renderPanel(okSender());
    const hints = document.querySelector(".ferry-panel__via-hints");
    expect(hints?.textContent).toBe("三の島あての手紙は二の島を経由して運びます。まず二の島へ");
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    const pile3 = document.querySelector<HTMLElement>('.ferry-panel__pile[data-to="3"]');
    expect(pile3?.querySelector(".ferry-panel__pile-to")?.textContent).toBe("→ 三の島行き先経由");
    const pile2 = document.querySelector<HTMLElement>('.ferry-panel__pile[data-to="2"]');
    expect(pile2?.querySelector(".ferry-panel__pile-to")?.textContent).toBe("→ 二の島行き先宛て");
  });

  it("FP-16 すべての宛先が隣なら経由の案内を出さない", () => {
    load();
    renderPanel(okSender());
    expect(document.querySelector(".ferry-panel__via-hints")).toBeNull();
  });

  it("FP-17 マップで隣の島を押すと行き先になる。隣でない島・航行中は何もしない", () => {
    load(lineSnapshot());
    renderPanel(okSender());
    act(() => {
      pressIsland(3);
    });
    expect(currentDestination()).toContain("未選択");
    act(() => {
      pressIsland(2);
    });
    expect(currentDestination()).toBe("行き先: 二の島");
    expect((screen.getByRole("radio", { name: "二の島" }) as HTMLInputElement).checked).toBe(true);
    expect(autoloadButton().getAttribute("aria-disabled")).toBeNull();

    // 航行中の船を選んでいるときは何もしない（例外も出ない）。
    act(() => {
      gameStore.getState().selectFerry("f2");
    });
    act(() => {
      pressIsland(2);
    });
    expect(document.querySelector(".ferry-panel__destination-current")).toBeNull();
  });

  it("FP-18 積み荷の経由の手紙に「経由」の札（停泊中・航行中）", () => {
    const base = lineSnapshot();
    load(
      withFerry1(
        { cargo: [30, 31] },
        {
          stage: base.stage,
          routesState: base.routesState,
          letters: [
            ferryLetter(30, "append", 1, 3, "f1"),
            ferryLetter(31, "heartbeat", 1, 2, "f1"),
          ],
        },
      ),
    );
    renderPanel(okSender());
    const buttons = within(cargoList()).getAllByRole("button");
    expect(buttons[0]?.getAttribute("aria-label")).toBe(
      "この内容を記録して！（二の島経由 → 三の島）を降ろす",
    );
    expect(buttons[0]?.querySelector(".ferry-panel__letter-to")?.textContent).toBe(
      "二の島経由 → 三の島",
    );
    expect(buttons[0]?.querySelector(".ferry-panel__via")?.textContent).toBe("二の島経由");
    expect(buttons[1]?.getAttribute("aria-label")).toBe("お元気ですか？（→ 二の島）を降ろす");
    expect(buttons[1]?.querySelector(".ferry-panel__via")).toBeNull();
  });

  it("FP-18 航行中: 向かっている島が宛先でない手紙は、向かっている島の経由", () => {
    const base = lineSnapshot();
    load(
      withFerry1(
        { state: "sailing", from: 1, to: 2, departAt: 28000, arriveAt: 31750, cargo: [30, 31] },
        {
          stage: base.stage,
          routesState: base.routesState,
          letters: [
            ferryLetter(30, "append", 1, 3, "f1"),
            ferryLetter(31, "heartbeat", 1, 2, "f1"),
          ],
        },
      ),
    );
    renderPanel(okSender());
    const letters = cargoList().querySelectorAll(".ferry-panel__cargo-letter");
    expect(letters[0]?.querySelector(".ferry-panel__letter-to")?.textContent).toBe(
      "二の島経由 → 三の島",
    );
    expect(letters[1]?.querySelector(".ferry-panel__letter-to")?.textContent).toBe("→ 二の島");
  });
});

// 中継倉庫（積み替え。FR-FERRY-06・T-722）。f1 は島 1 に停泊。
describe("中継倉庫の区画（T-722）", () => {
  // 島 1 の倉庫に 40（宛先 3）・41（宛先 2）・42（宛先 3）、島 2 の倉庫に 43。積み荷は 50（島 1 生まれ）・51（島 2 生まれ）。
  function relaySnapshot(relayCap: number, relayIds: number[] = [40, 41, 42]): Snapshot {
    const base = makeFerrySnapshot();
    const relay = [
      relayLetter(40, "append", 2, 3, 1),
      relayLetter(41, "heartbeat", 3, 2, 1),
      relayLetter(42, "vote", 2, 3, 1),
    ].filter((l) => relayIds.includes(l.id));
    return withRelayCap(
      withFerry1(
        { cargo: [50, 51] },
        {
          letters: [
            ...base.letters,
            ferryLetter(50, "heartbeat", 1, 2, "f1"),
            ferryLetter(51, "append", 2, 3, "f1"),
            ...relay,
            relayLetter(43, "append", 3, 1, 2),
          ],
        },
      ),
      relayCap,
    );
  }

  function relayArea(): HTMLElement {
    const el = document.querySelector<HTMLElement>(".ferry-panel__relay");
    if (el === null) throw new Error("倉庫が無い");
    return el;
  }

  function relayButtons(): HTMLElement[] {
    return within(relayArea()).getAllByRole("button");
  }

  function viaCargoButton(): HTMLElement {
    const el = cargoList().querySelector<HTMLElement>('button[data-letter-id="51"]');
    if (el === null) throw new Error("経由の手紙が無い");
    return el;
  }

  function relayMoved(letterId: number, islandId: number): EventBody {
    return { type: "letter.moved", t: 29000, letterId, loc: { type: "relay", islandId } };
  }

  it("FP-R1 relayCap > 0 の停泊中の船では、港と積み荷の間に倉庫の区画が出る", () => {
    load(relaySnapshot(12));
    renderPanel(okSender());
    const sections = [...document.querySelectorAll<HTMLElement>(".ferry-panel__section")];
    expect(sections.map((s) => s.querySelector("h3")?.textContent)).toEqual([
      "一の島の港の手紙",
      "📦 一の島の倉庫 3/12",
      "積み荷 2/4",
    ]);
    expect(sections[1]?.classList.contains("ferry-panel__relay-section")).toBe(true);
    expect(sections[1]?.querySelector("h3")?.getAttribute("tabindex")).toBe("-1");
    expect(
      within(sections[1] ?? document.body).getByText(
        "預けた手紙は、宛先に着くまで島では読まれません",
      ),
    ).toBeDefined();
    const piles = [...relayArea().querySelectorAll<HTMLElement>(".ferry-panel__pile")];
    expect(piles.map((p) => p.dataset["to"])).toEqual(["2", "3"]);
    expect(
      piles.map((p) =>
        [...p.querySelectorAll("button[data-letter-id]")].map((b) =>
          b.getAttribute("data-letter-id"),
        ),
      ),
    ).toEqual([["41"], ["40", "42"]]);
    // 別の島の倉庫の手紙（43）は出ない。
    expect(document.querySelector('button[data-letter-id="43"]')).toBeNull();
    // 行き先の山には「行き先宛て」。
    fireEvent.click(screen.getByRole("radio", { name: "二の島" }));
    expect(piles[0]?.querySelector(".ferry-panel__pile-to")?.textContent).toBe(
      "→ 二の島行き先宛て",
    );
  });

  it("FP-R1 倉庫が空なら「倉庫に手紙はありません。」", () => {
    load(relaySnapshot(12, []));
    renderPanel(okSender());
    expect(within(relayArea()).getByText("倉庫に手紙はありません。")).toBeDefined();
    expect(screen.getByRole("heading", { level: 3, name: "一の島の倉庫 0/12" })).toBeDefined();
  });

  it("FP-R1 relayCap 0・航行中では倉庫の区画を出さない", () => {
    load(relaySnapshot(0));
    const { unmount } = renderPanel(okSender());
    expect(document.querySelector(".ferry-panel__relay-section")).toBeNull();
    expect(document.querySelector(".ferry-panel__relay")).toBeNull();
    unmount();
    load(relaySnapshot(12), "f2");
    renderPanel(okSender());
    expect(document.querySelector(".ferry-panel__relay-section")).toBeNull();
  });

  it("FP-R2 経由の手紙を押すと cmd.ferry.unload を送り、ok で「手紙を倉庫に預けました。」", async () => {
    load(relaySnapshot(12));
    const send = okSender();
    renderPanel(send);
    const via = viaCargoButton();
    expect(via.getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(via);
    expect(send).toHaveBeenCalledWith("cmd.ferry.unload", { ferryId: "f1", letterIds: [51] });
    await flush();
    expect(statusText()).toBe("手紙を倉庫に預けました。");
    // 反映待ち。イベントで倉庫に入る。
    expect(viaCargoButton().getAttribute("aria-disabled")).toBe("true");
    events(relayMoved(51, 1));
    expect(cargoList().querySelector('button[data-letter-id="51"]')).toBeNull();
    expect(relayArea().querySelector('button[data-letter-id="51"]')).not.toBeNull();
    expect(screen.getByRole("heading", { level: 3, name: "一の島の倉庫 4/12" })).toBeDefined();
  });

  it("FP-R2 経由の手紙を倉庫・港へドラッグすると cmd.ferry.unload で、文は預け入れ", async () => {
    load(relaySnapshot(12));
    const send = okSender();
    renderPanel(send);
    act(() => {
      dnd.onDragEnd?.({
        active: { id: "letter:cargo:51", data: { current: { letterId: 51, source: "cargo" } } },
        over: { id: RELAY_DROP_ID },
      } as unknown as DragEndEvent);
    });
    expect(send).toHaveBeenCalledWith("cmd.ferry.unload", { ferryId: "f1", letterIds: [51] });
    await flush();
    expect(statusText()).toBe("手紙を倉庫に預けました。");
  });

  it("FP-R3 倉庫の手紙を押すと cmd.relay.load を送り、ok で「倉庫の手紙を積みました。」", async () => {
    load(relaySnapshot(12));
    const send = okSender();
    renderPanel(send);
    const first = relayButtons()[0];
    if (first === undefined) throw new Error("倉庫の手紙が無い");
    expect(first.getAttribute("aria-label")).toBe("お元気ですか？（→ 二の島）を積む");
    fireEvent.click(first);
    expect(send).toHaveBeenCalledWith("cmd.relay.load", { ferryId: "f1", letterIds: [41] });
    await flush();
    expect(statusText()).toBe("倉庫の手紙を積みました。");
    expect(within(relayArea()).getByText("反映待ち")).toBeDefined();
  });

  it("FP-R3 倉庫の手紙を積み荷へドラッグすると cmd.relay.load。港へは何もしない", async () => {
    load(relaySnapshot(12));
    const send = okSender();
    renderPanel(send);
    const drag = (over: string): void => {
      act(() => {
        dnd.onDragEnd?.({
          active: { id: "letter:relay:40", data: { current: { letterId: 40, source: "relay" } } },
          over: { id: over },
        } as unknown as DragEndEvent);
      });
    };
    drag(PORT_DROP_ID);
    expect(send).not.toHaveBeenCalled();
    drag(CARGO_DROP_ID);
    expect(send).toHaveBeenCalledWith("cmd.relay.load", { ferryId: "f1", letterIds: [40] });
    await flush();
    expect(statusText()).toBe("倉庫の手紙を積みました。");
    expect(
      dnd.accessibility?.announcements?.onDragOver({
        active: { id: "letter:relay:40", data: { current: { letterId: 40, source: "relay" } } },
        over: { id: RELAY_DROP_ID },
      } as unknown as DragEndEvent),
    ).toBe("倉庫の上です。");
  });

  it.each<[string, "deposit" | "relayLoad", string, string]>([
    ["預ける・relay_full", "deposit", "relay_full", "この島の倉庫はいっぱいです。"],
    [
      "倉庫から積む・letter_not_here",
      "relayLoad",
      "letter_not_here",
      "その手紙はもう倉庫にありません。",
    ],
    ["預ける・letter_not_here", "deposit", "letter_not_here", "その手紙はもう船にありません。"],
    ["倉庫から積む・capacity_exceeded", "relayLoad", "capacity_exceeded", "積み荷がいっぱいです。"],
  ])("FP-R4 %s の文", async (_name, op, code, text) => {
    load(relaySnapshot(12));
    const send = vi.fn<GameCommandSender>(() => Promise.resolve({ ...OK, ok: false, code } as Ack));
    renderPanel(send);
    const target = op === "deposit" ? viaCargoButton() : relayButtons()[0];
    if (target === undefined) throw new Error("ボタンが無い");
    fireEvent.click(target);
    await flush();
    expect(statusText()).toBe(text);
    expect(screen.queryByText("反映待ち")).toBeNull();
  });

  it("FP-R5 倉庫がいっぱいなら経由の手紙は押せず「倉庫がいっぱいです」。送信元の手紙は港へ戻せる", () => {
    load(relaySnapshot(3));
    const send = okSender();
    renderPanel(send);
    const via = viaCargoButton();
    expect(via.getAttribute("aria-disabled")).toBe("true");
    expect(describedText(via)).toContain("倉庫がいっぱいです");
    expect(via.getAttribute("title")).toBe("倉庫がいっぱいです");
    fireEvent.click(via);
    act(() => {
      dnd.onDragEnd?.({
        active: { id: "letter:cargo:51", data: { current: { letterId: 51, source: "cargo" } } },
        over: { id: RELAY_DROP_ID },
      } as unknown as DragEndEvent);
    });
    expect(send).not.toHaveBeenCalled();
    const own = cargoList().querySelector<HTMLElement>('button[data-letter-id="50"]');
    expect(own?.getAttribute("aria-disabled")).toBeNull();
    if (own !== null) fireEvent.click(own);
    expect(send).toHaveBeenCalledWith("cmd.ferry.unload", { ferryId: "f1", letterIds: [50] });
  });

  it("FP-R5 relayCap 0 なら経由の手紙は「この島で生まれた手紙だけ降ろせます」", () => {
    load(relaySnapshot(0));
    renderPanel(okSender());
    const via = viaCargoButton();
    expect(via.getAttribute("aria-disabled")).toBe("true");
    expect(describedText(via)).toContain("この島で生まれた手紙だけ降ろせます");
  });

  it("FP-R6 倉庫から積んだ手紙が消えたら、倉庫の同じ位置・1 つ前・見出しにフォーカスを移す", async () => {
    load(relaySnapshot(12));
    renderPanel(okSender());
    const heading = screen.getByRole("heading", { level: 3, name: "一の島の倉庫 3/12" });

    // 並びは 41・40・42。真ん中（40）を積む → 同じ位置の 42。
    const middle = relayButtons()[1];
    if (middle === undefined) throw new Error("倉庫の 2 つ目が無い");
    middle.focus();
    fireEvent.click(middle);
    await flush();
    events(moved(40, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement?.getAttribute("data-letter-id")).toBe("42");

    // 最後（42）を積む → 1 つ前の 41。
    fireEvent.click(document.activeElement as HTMLElement);
    await flush();
    events(moved(42, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement?.getAttribute("data-letter-id")).toBe("41");

    // 最後の 1 通 → 見出し。
    fireEvent.click(document.activeElement as HTMLElement);
    await flush();
    events(moved(41, { type: "ferry", ferryId: "f1" }));
    expect(document.activeElement).toBe(heading);
    expect(within(relayArea()).getByText("倉庫に手紙はありません。")).toBeDefined();
  });

  it("FP-R7 倉庫の区画があっても、チュートリアルが指す className が残る", () => {
    load(relaySnapshot(12));
    renderPanel(okSender());
    for (const cls of [
      "ferry-panel",
      "ferry-panel__tabs",
      "ferry-panel__port",
      "ferry-panel__cargo",
      "ferry-panel__destinations",
      "ferry-panel__autoload",
      "ferry-panel__depart",
      "ferry-panel__status",
    ]) {
      expect(document.querySelector(`.${cls}`), cls).not.toBeNull();
    }
  });
});

// 記録（web/src/net/fixtures の mini-record）をストアに入れて、封筒のスロットが並ぶことを確かめる（T-422）。
describe("記録を入れたときの封筒のスロット（T-422）", () => {
  function playUntil(lastSeq: number): void {
    const { snapshot, events: recorded } = loadMiniRound();
    const list = recorded.filter((e) => e.seq <= lastSeq);
    act(() => {
      const state = gameStore.getState();
      state.applySnapshot(snapshot, performance.now());
      state.applyEvents(
        { t: list.at(-1)?.t ?? 0, speed: 1, paused: false, endsAt: 60000 },
        list,
        performance.now(),
      );
      state.selectFerry("f1");
    });
  }

  function slots(): Element[] {
    return [...document.querySelectorAll(".ferry-panel__cargo > .ferry-panel__slot")];
  }

  it("FP-T422-1 停泊中: 港の手紙は封筒のボタン、積み荷は定員の数の空きのスロット", () => {
    playUntil(8);
    renderPanel(okSender());
    const port = document.querySelector(".ferry-panel__port");
    expect(port?.querySelectorAll("button.ferry-panel__letter")).toHaveLength(2);
    expect(slots()).toHaveLength(4);
    expect(slots().every((slot) => slot.classList.contains("ferry-panel__slot--empty"))).toBe(true);
  });

  it("FP-T422-2 航行中: 積んだ手紙は押せない封筒、残りは空きのスロット", () => {
    playUntil(11);
    renderPanel(okSender());
    expect(slots()).toHaveLength(4);
    expect(
      document.querySelectorAll(".ferry-panel__cargo .ferry-panel__cargo-letter"),
    ).toHaveLength(1);
    expect(document.querySelectorAll(".ferry-panel__cargo .ferry-panel__slot--empty")).toHaveLength(
      3,
    );
    expect(document.querySelector(".ferry-panel__cargo button")).toBeNull();
  });
});
