// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { gameStore } from "../../state/index.ts";
import type { GameState } from "../../state/index.ts";
import { GameCommandProvider } from "../../ui/commands/GameCommands.tsx";
import type { GameCommandSender, GameCommandType } from "../../ui/commands/GameCommands.tsx";
import { openingCeremonyState } from "../../ui/opening/index.ts";
import { setShowRaftTerms } from "../../ui/terms/preference.ts";
import { TutorialOverlay } from "./TutorialOverlay.tsx";
import {
  FAR,
  LEADER,
  RELAY,
  ferryLetter,
  sailingFerry,
  stage1Ferry,
  stage1Request,
  stage1Snapshot,
  withFerry,
  withLetters,
  withPaused,
  withRequests,
} from "./testing/stage1World.ts";
import type { Snapshot } from "./testing/stage1World.ts";

type Ack = Awaited<ReturnType<GameCommandSender>>;
type ServerEvent = Parameters<GameState["applyEvents"]>[1][number];

const OK: Ack = { cmdId: "c1", ok: true, t: 0 };

let send = vi.fn(async (type: GameCommandType, payload: unknown): Promise<Ack> => {
  void type;
  void payload;
  return OK;
});
const sender: GameCommandSender = (type, payload) => send(type, payload);

// テストの中の「サーバー」の時計。load は最後に送った一時停止を時計に表す。
let serverPaused = false;
let last: Snapshot = stage1Snapshot();

function load(snapshot: Snapshot, paused: boolean = serverPaused): void {
  last = snapshot;
  act(() => {
    gameStore.getState().applySnapshot(withPaused(snapshot, paused), 0);
  });
}

// サーバーが一時停止を処理した（最後に送った値を時計に表す）。
function sync(): void {
  const pauses = pauseCalls();
  const latest = pauses[pauses.length - 1];
  if (latest !== undefined) serverPaused = latest.paused;
  load(last);
}

function pauseCalls(): { paused: boolean }[] {
  return send.mock.calls
    .filter(([type]) => type === "cmd.game.pause")
    .map(([, payload]) => payload as { paused: boolean });
}

function bubble(): HTMLElement | null {
  return screen.queryByRole("complementary", { name: "チュートリアル" });
}

function stepId(): string | null {
  return bubble()?.dataset.step ?? null;
}

function click(name: string): void {
  act(() => {
    fireEvent.click(screen.getByRole("button", { name }));
  });
}

function Screen({ stageId }: { stageId: string }): ReactElement {
  return (
    <div className="game-screen">
      <h1 tabIndex={-1}>はじめての郵便船</h1>
      <TutorialOverlay stageId={stageId} />
    </div>
  );
}

function renderOverlay(stageId = "stage1", provider = true): ReturnType<typeof render> {
  return render(
    provider ? (
      <GameCommandProvider send={sender}>
        <Screen stageId={stageId} />
      </GameCommandProvider>
    ) : (
      <Screen stageId={stageId} />
    ),
  );
}

const base = stage1Snapshot();
const withRequest = withRequests(base, [stage1Request("r1", LEADER)]);
const withCargo = withLetters(withFerry(withRequest, stage1Ferry({ cargo: [5] })), [
  ferryLetter(5, LEADER, RELAY),
]);
const sailing = withLetters(withFerry(withRequest, sailingFerry(LEADER, RELAY, [5])), [
  ferryLetter(5, LEADER, RELAY),
]);
// 豊島から返事を積んで帰る途中・最初の確定・直島の依頼（T2〜T4 を飛ばして T5 → T6）。
const farRequested = withRequests(
  withLetters(withFerry(base, sailingFerry(RELAY, LEADER, [7])), [ferryLetter(7, RELAY, LEADER)]),
  [stage1Request("r1", LEADER, "succeeded"), stage1Request("r2", FAR)],
);

// T1 を出して（止めて）「次へ」を押し、再開まで進める。
function passT1(): void {
  load(base);
  expect(stepId()).toBe("T1");
  sync();
  click("次へ");
  sync();
}

// T5 の「次へ」で T6 を出す。
function toT6(): void {
  passT1();
  load(farRequested);
  expect(stepId()).toBe("T5");
  sync();
  click("次へ");
  expect(stepId()).toBe("T6");
}

beforeEach(() => {
  vi.useFakeTimers({
    toFake: [
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "requestAnimationFrame",
      "cancelAnimationFrame",
    ],
  });
  send = vi.fn(async (type: GameCommandType, payload: unknown): Promise<Ack> => {
    void type;
    void payload;
    return OK;
  });
  serverPaused = false;
  last = stage1Snapshot();
  gameStore.getState().reset();
  openingCeremonyState.reset();
});

afterEach(() => {
  cleanup();
  act(() => {
    setShowRaftTerms(false);
  });
  gameStore.getState().reset();
  openingCeremonyState.reset();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

describe("TutorialOverlay", () => {
  it("TO-1 stage1 以外では何も出さず、送らない", () => {
    renderOverlay("stage2");
    load(base);
    load(withRequest);
    expect(bubble()).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it("TO-2 T1 を出して一時停止し、「次へ」で再開する", () => {
    renderOverlay();
    load(base);
    const aside = bubble();
    expect(aside).not.toBeNull();
    expect(aside?.textContent).toContain(
      "あなたは離島Raft共和国の郵便船の船長です。島どうしの手紙を運んで、しまじまフェスを成功させましょう",
    );
    expect(screen.getByRole("button", { name: "次へ" })).toBeTruthy();
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenLastCalledWith("cmd.game.pause", { paused: true });

    sync();
    expect(send).toHaveBeenCalledTimes(1);

    click("次へ");
    expect(bubble()).toBeNull();
    expect(send).toHaveBeenCalledTimes(2);
    expect(send).toHaveBeenLastCalledWith("cmd.game.pause", { paused: false });
  });

  it("TO-3 T2 → T3 は止めたまま、出航で消えて再開を 1 回送る", () => {
    renderOverlay();
    passT1();
    expect(pauseCalls()).toEqual([{ paused: true }, { paused: false }]);

    load(withRequest);
    expect(stepId()).toBe("T2");
    expect(bubble()?.textContent).toContain("小豆島の港に手紙が出ました");
    expect(screen.getByRole("button", { name: "わかった" })).toBeTruthy();
    expect(pauseCalls()).toHaveLength(3);
    sync();

    load(withCargo);
    expect(stepId()).toBe("T3");
    expect(pauseCalls()).toHaveLength(3);

    load(sailing);
    expect(bubble()).toBeNull();
    expect(pauseCalls()).toEqual([
      { paused: true },
      { paused: false },
      { paused: true },
      { paused: false },
    ]);
  });

  it("TO-4 T6 の「わかった」で畳んで再開し、「開く」では止め直さない", () => {
    renderOverlay();
    toT6();
    sync();
    const before = pauseCalls().length;
    expect(serverPaused).toBe(true);

    click("わかった");
    expect(bubble()?.classList.contains("tutorial--collapsed")).toBe(true);
    expect(screen.getByRole("button", { name: "開く" })).toBeTruthy();
    expect(pauseCalls().slice(before)).toEqual([{ paused: false }]);
    sync();

    click("開く");
    expect(bubble()?.classList.contains("tutorial--collapsed")).toBe(false);
    expect(screen.getByRole("button", { name: "わかった" })).toBeTruthy();
    expect(pauseCalls().slice(before)).toEqual([{ paused: false }]);
  });

  it("TO-5 プレイヤーが ▶ で再開したら、その段では止め直さない", () => {
    renderOverlay();
    load(base);
    sync();
    expect(pauseCalls()).toEqual([{ paused: true }]);
    load(base, false); // HUD の ▶
    expect(stepId()).toBe("T1");
    load(base, false);
    expect(pauseCalls()).toEqual([{ paused: true }]);
    click("次へ");
    expect(pauseCalls()).toEqual([{ paused: true }]);
  });

  it("プレイヤーが自分で止めた一時停止は、吹き出しを閉じても解かない", () => {
    renderOverlay();
    load(base, true); // ⏸ してから
    expect(stepId()).toBe("T1");
    click("次へ");
    expect(send).not.toHaveBeenCalled();
  });

  it("TO-6 useGameCommand() が null でも T1 は出て、例外が出ない", () => {
    renderOverlay("stage1", false);
    load(base);
    expect(stepId()).toBe("T1");
    click("次へ");
    expect(bubble()).toBeNull();
  });

  it("TO-7 「チュートリアルを終える」の後は何も出さない", () => {
    renderOverlay();
    load(base);
    sync();
    click("チュートリアルを終える");
    expect(bubble()).toBeNull();
    expect(pauseCalls()).toEqual([{ paused: true }, { paused: false }]);
    sync();
    load(withRequest);
    load(farRequested);
    expect(bubble()).toBeNull();
    expect(pauseCalls()).toHaveLength(2);
  });

  it("TO-8 T1 は開会式の演出が終わってから出る", () => {
    const elected: ServerEvent = { type: "node.leader_elected", seq: 1, t: 0, id: LEADER, term: 1 };
    renderOverlay();
    // 時刻 0 の elected（開会式の結果）のある世界。OpeningCeremony を描かないので演出は終わらない。
    act(() => {
      gameStore.getState().applySnapshot(base, 0);
      gameStore
        .getState()
        .applyEvents({ t: 1000, speed: 1, paused: false, endsAt: 300000 }, [elected], 0);
    });
    expect(bubble()).toBeNull();
    expect(send).not.toHaveBeenCalled();

    act(() => {
      openingCeremonyState.finish(`${base.session.id}:1`);
    });
    expect(stepId()).toBe("T1");
  });

  it("TO-9 ended の世界では何も出さず、再開も送らない", () => {
    renderOverlay();
    load(stage1Snapshot({ phase: "ended" }));
    expect(bubble()).toBeNull();
    expect(send).not.toHaveBeenCalled();

    load(base);
    sync();
    expect(pauseCalls()).toEqual([{ paused: true }]);
    load(stage1Snapshot({ phase: "ended" }));
    expect(bubble()).toBeNull();
    cleanup();
    expect(pauseCalls()).toEqual([{ paused: true }]);
  });

  it("TO-10 用語の併記は各吹き出しの最初の 1 か所だけ", () => {
    renderOverlay();
    toT6();
    act(() => {
      setShowRaftTerms(true);
    });
    const notes = bubble()?.querySelectorAll(".raft-term-note") ?? [];
    expect(notes).toHaveLength(1);
    expect(bubble()?.textContent).toContain("代表（Leader）");
    act(() => {
      setShowRaftTerms(false);
    });
    expect(bubble()?.querySelector(".raft-term-note")).toBeNull();
  });

  it("TO-11 広がった吹き出しの間だけ、強調の輪を重ねる", () => {
    const port = document.createElement("ul");
    port.className = "ferry-panel__port";
    port.getBoundingClientRect = () =>
      ({ x: 5, y: 6, top: 6, left: 5, width: 10, height: 10, right: 15, bottom: 16 }) as DOMRect;
    document.body.append(port);
    const hidden = document.createElement("ul");
    hidden.className = "ferry-panel__cargo"; // 幅も高さも 0 なので輪を置かない
    document.body.append(hidden);

    const view = renderOverlay();
    load(base);
    act(() => {
      vi.advanceTimersByTime(50);
    });
    expect(stepId()).toBe("T1");
    expect(document.querySelectorAll(".tutorial-highlight")).toHaveLength(0);

    sync();
    click("次へ");
    sync();
    load(withRequest);
    expect(stepId()).toBe("T2");
    act(() => {
      vi.advanceTimersByTime(50);
    });
    const rings = document.querySelectorAll<HTMLElement>(".tutorial-highlight");
    expect(rings).toHaveLength(1);
    expect(rings[0]?.getAttribute("aria-hidden")).toBe("true");
    expect(rings[0]?.style.width).toBe("10px");
    expect(rings[0]?.style.top).toBe("6px");

    click("わかった");
    act(() => {
      vi.advanceTimersByTime(50);
    });
    expect(document.querySelectorAll(".tutorial-highlight")).toHaveLength(0);

    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("TO-12 押したボタンが消えたら、次の吹き出しのボタンか <h1> にフォーカスを移す", () => {
    renderOverlay();
    load(base);
    sync();
    const next = screen.getByRole("button", { name: "次へ" });
    next.focus();
    click("次へ");
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "はじめての郵便船" }));

    sync();
    load(farRequested);
    expect(stepId()).toBe("T5");
    screen.getByRole("button", { name: "次へ" }).focus();
    click("次へ");
    expect(stepId()).toBe("T6");
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "わかった" }));
  });

  it("TO-13 自分が止めているときにアンマウントしたら、再開を 1 回送る", async () => {
    const view = renderOverlay();
    load(base);
    sync();
    expect(pauseCalls()).toEqual([{ paused: true }]);
    view.unmount();
    expect(pauseCalls()).toEqual([{ paused: true }, { paused: false }]);
    await act(async () => {
      await Promise.resolve();
    });
  });

  it("ack が失敗したら、同じ段では送り直さない", async () => {
    send.mockImplementation(async () => ({ cmdId: "c1", ok: false, t: 0, code: "rate_limited" }));
    renderOverlay();
    load(base);
    expect(pauseCalls()).toEqual([{ paused: true }]);
    await act(async () => {
      await Promise.resolve();
    });
    load(base);
    load(base);
    expect(pauseCalls()).toEqual([{ paused: true }]);
    expect(stepId()).toBe("T1");
  });

  it("session.id が変わったら最初から出す", () => {
    renderOverlay();
    passT1();
    expect(bubble()).toBeNull();
    load(stage1Snapshot({ session: { ...base.session, id: "s-next" } }));
    expect(stepId()).toBe("T1");
  });

  it("島の名前が無いステージでは何も出さない", () => {
    renderOverlay();
    load(
      stage1Snapshot({
        stage: {
          ...base.stage,
          islands: base.stage.islands.map((i) => (i.id === RELAY ? { ...i, name: "手島" } : i)),
        },
      }),
    );
    expect(bubble()).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });
});
