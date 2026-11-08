// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import miniRecord from "../../net/fixtures/mini-record.json";
import miniStage from "../../net/fixtures/mini-stage.json";
import {
  createManualTimers,
  type ConnectionHandlers,
  type ConnectionState,
  type ConnectionStatus,
  type FailureReason,
  type GameConnection,
} from "../../net/index.ts";
import { parseRecord, snapshotFromRecord } from "../../net/mock/record.ts";
import type { Ack, Phase, Snapshot, StageView } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { parseReplayResponse } from "../../replay/response.ts";
import { successResponse } from "../../replay/testing/responses.ts";
import { createMockRound } from "../dev/mockRound.ts";
import { createReplayConnector } from "../replay/replayConnector.ts";
import { stage1Snapshot } from "../tutorial/testing/stage1World.ts";
import type { RoundContext } from "../types.ts";
import {
  createRoundConnector,
  type RoundConnector,
  type RoundLink,
  type RoundSource,
} from "./connector.ts";
import { GameScreen } from "./GameScreen.tsx";

const round: RoundContext = {
  stage: {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
  },
  session: null,
  finished: false,
};

const withSession: RoundContext = {
  ...round,
  session: { sessionId: "s_1.sig", token: "t-1", wsPath: "/ws/sessions/s_1.sig" },
};

const record = parseRecord(miniRecord);

type SentCommand = { type: string; payload: unknown; resolve(ack: Ack): void };

// 手で動かす接続と、それを返す RoundConnector（session があれば server、無ければ offline）。
// source が replay なら、session によらず replay。
function manualConnector(source: Exclude<RoundSource, "mock"> = "server") {
  const sent: SentCommand[] = [];
  let handlers: ConnectionHandlers | null = null;
  let status: ConnectionStatus = { state: "idle", attempt: 0, reason: null };
  const connection: GameConnection = {
    start() {},
    close() {},
    sendCommand(type, payload) {
      return new Promise<Ack>((resolve) => {
        sent.push({ type, payload, resolve });
      });
    },
    setEngineerSubscription() {},
    getStatus: () => status,
  };
  const link: RoundLink = { source, connection, dispose() {} };
  const open = vi.fn((_session: unknown, h: ConnectionHandlers) => {
    handlers = h;
    return link;
  });
  const connector: RoundConnector = {
    modeOf: (session) => (source === "replay" ? "replay" : session === null ? "offline" : "server"),
    open,
  };
  function current(): ConnectionHandlers {
    if (handlers === null) {
      throw new Error("not opened");
    }
    return handlers;
  }
  return {
    connector,
    open,
    sent,
    setStatus(state: ConnectionState, reason: FailureReason | null = null) {
      status = { state, attempt: 1, reason };
      act(() => {
        current().onStatus?.(status);
      });
    },
    snapshot(phase: Phase) {
      act(() => {
        current().onSnapshot(
          { ...snapshotFromRecord(record, { stage: miniStage as StageView }), phase },
          0,
        );
      });
    },
    load(snapshot: Snapshot) {
      act(() => {
        current().onSnapshot(snapshot, 0);
      });
    },
  };
}

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

describe("GameScreen", () => {
  it("GM-1 領域とスロットを置き、<h1> がステージのタイトル", () => {
    const { connector } = manualConnector();
    render(<GameScreen round={round} onRoundEnd={() => {}} connector={connector} />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("はじめての郵便船");
    for (const name of ["状況", "マップ", "フェリー操作", "お知らせ"]) {
      expect(screen.getByRole("region", { name })).toBeDefined();
    }
    expect(screen.getByRole("complementary", { name: "島の詳細" })).toBeDefined();
    const notifications = screen.getByRole("region", { name: "お知らせ" });
    expect(notifications.getAttribute("aria-live")).toBe("polite");
  });

  it("GM-6 エンジニアモードを開いている間は、島の詳細の代わりにエンジニアパネルを出す（T-438）", () => {
    const m = manualConnector();
    render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
    m.setStatus("ready");
    m.snapshot("sailing");
    const island = () => screen.queryByRole("complementary", { name: "島の詳細" });
    const engineer = () => screen.queryByRole("complementary", { name: "エンジニアパネル" });
    expect(island()).not.toBeNull();
    expect(engineer()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "E エンジニアモード" }));
    expect(island()).toBeNull();
    const panel = engineer();
    expect(panel).not.toBeNull();
    // パネルは E ボタンと一緒に「状況」の帯の中（T-439。GameScreen.css が島の詳細の列に置く）で、
    // マップの中には無い。
    const hud = screen.getByRole("region", { name: "状況" });
    expect(hud.className).toBe("game-hud");
    expect(panel?.parentElement).toBe(hud);
    expect(screen.getByRole("button", { name: "E エンジニアモード" }).parentElement).toBe(hud);
    expect(
      within(screen.getByRole("region", { name: "マップ" })).queryByRole("tablist"),
    ).toBeNull();

    // マップで島を選ぶ（ストアの選択）と、開いたままのパネルのノード一覧でその島を選ぶ。
    act(() => {
      gameStore.getState().selectIsland(2);
    });
    expect(
      within(screen.getByRole("table", { name: "ノード一覧" }))
        .getByRole("button", { name: /^2 / })
        .getAttribute("aria-pressed"),
    ).toBe("true");

    fireEvent.keyDown(window, { key: "e" });
    expect(engineer()).toBeNull();
    expect(island()).not.toBeNull();
  });

  it("GM-6 リプレイでもエンジニアモードを開ける", () => {
    const m = manualConnector("replay");
    render(
      <GameScreen
        round={{ ...round, session: null }}
        onRoundEnd={() => {}}
        connector={m.connector}
      />,
    );
    m.setStatus("ready");
    m.snapshot("sailing");
    fireEvent.click(screen.getByRole("button", { name: "E エンジニアモード" }));
    expect(screen.queryByRole("complementary", { name: "島の詳細" })).toBeNull();
    const panel = screen.getByRole("complementary", { name: "エンジニアパネル" });
    expect(within(panel).getAllByRole("tab")).toHaveLength(4);
  });

  it("GM-2 offline なら「リザルトへ（仮）」で onRoundEnd を呼ぶ", () => {
    const { connector, open } = manualConnector();
    const onRoundEnd = vi.fn();
    render(<GameScreen round={round} onRoundEnd={onRoundEnd} connector={connector} />);

    expect(screen.getByText(/サーバーに接続していません/)).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "リザルトへ（仮）" }));
    expect(onRoundEnd).toHaveBeenCalledTimes(1);
    expect(open).not.toHaveBeenCalled();
  });

  it("GM-2 session があるときは仮のボタンと文言を出さない", () => {
    const { connector } = manualConnector();
    render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={connector} />);

    expect(screen.queryByRole("button", { name: "リザルトへ（仮）" })).toBeNull();
    expect(screen.queryByText(/サーバーに接続していません/)).toBeNull();
  });

  it("GM-3 接続の状態を出す", () => {
    const m = manualConnector();
    render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
    const statusWith = (text: string) =>
      screen.getAllByRole("status").find((el) => el.textContent?.includes(text));

    m.setStatus("connecting");
    expect(statusWith("出航の準備をしています…")).toBeDefined();

    m.setStatus("ready");
    expect(screen.queryByText("出航の準備をしています…")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();

    m.setStatus("reconnecting");
    expect(statusWith("接続が切れました。つなぎ直しています…")).toBeDefined();
    expect(document.querySelector(".departure-art")).toBeNull();

    m.setStatus("failed", "replaced");
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain(
      "ほかのタブでこのゲームを開いたため、この画面の接続を終えました。",
    );
    const link = within(alert).getByRole("link", { name: "ステージ選択へ" });
    expect(link.getAttribute("href")).toBe("/stages");
    expect(screen.queryByText("接続が切れました。つなぎ直しています…")).toBeNull();
  });

  it("GM-3b 接続中は出港の絵と文言をマップの中央に出し、つながると消す（T-436）", () => {
    const m = manualConnector();
    render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
    const map = screen.getByRole("region", { name: "マップ" });
    const departure = () => map.querySelector(".game-departure");

    // 接続の準備中（idle）・接続中は、出港の絵（飾り）と文言を 1 つの status に出す
    for (const state of ["idle", "connecting"] as const) {
      m.setStatus(state);
      const card = departure();
      expect(card?.getAttribute("role")).toBe("status");
      expect(card?.textContent).toBe("出航の準備をしています…");
      const art = card?.querySelector("svg.departure-art");
      expect(art?.getAttribute("aria-hidden")).toBe("true");
      expect(art?.querySelectorAll(".departure-art-ship")).toHaveLength(1);
    }

    m.setStatus("ready");
    expect(departure()).toBeNull();
    expect(screen.queryByText("出航の準備をしています…")).toBeNull();

    // つながらないときは、絵を消して今までのエラーの文言を出す
    m.setStatus("connecting");
    expect(departure()).not.toBeNull();
    m.setStatus("failed", "unreachable");
    expect(departure()).toBeNull();
    expect(screen.getByRole("alert").textContent).toContain("サーバーに接続できませんでした。");
  });

  it("GM-4 ready・syncing のときだけパネルからコマンドを送れる", () => {
    const m = manualConnector();
    render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
    m.setStatus("connecting");
    m.snapshot("sailing");
    const faster = () => screen.getByRole("button", { name: "速くする" }) as HTMLButtonElement;
    expect(faster().disabled).toBe(true);

    m.setStatus("ready");
    expect(faster().disabled).toBe(false);
    fireEvent.click(faster());
    expect(m.sent.map((s) => ({ type: s.type, payload: s.payload }))).toEqual([
      { type: "cmd.game.speed", payload: { speed: 2 } },
    ]);

    m.setStatus("reconnecting");
    expect(faster().disabled).toBe(true);
  });

  it("GM-5 開発用のモックで記録を最後まで再生する", async () => {
    const timers = createManualTimers();
    const connector = createRoundConnector({
      mockEnabled: true,
      openMock: (h) => createMockRound(h, { timers }),
    });
    const onRoundEnd = vi.fn();
    render(<GameScreen round={round} onRoundEnd={onRoundEnd} connector={connector} />);

    await act(async () => {
      timers.advance(0);
    });
    expect(gameStore.getState().world?.stage.title).toBe("テストの海");
    expect(screen.getByText(/開発用の記録を再生しています/)).toBeDefined();
    expect(screen.queryByText(/サーバーに接続していません/)).toBeNull();

    await act(async () => {
      timers.advance(60100);
    });
    expect(onRoundEnd).toHaveBeenCalledTimes(1);
    expect(gameStore.getState().roundResult).not.toBeNull();
  });

  describe("RS-11 リプレイの接続", () => {
    const stage1Round: RoundContext = { ...round, session: null };
    const tutorial = () => screen.queryByRole("complementary", { name: "チュートリアル" });
    const demoSnapshot = () =>
      stage1Snapshot({ session: { ...stage1Snapshot().session, demo: true } });

    it("server ではチュートリアルが出て、デモのキーを受け付ける（比べるため）", () => {
      const m = manualConnector("server");
      render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
      m.setStatus("ready");
      m.load(demoSnapshot());
      expect(tutorial()).not.toBeNull();
      expect(screen.queryByText("リプレイを再生しています（船の操作はできません）。")).toBeNull();
      act(() => {
        fireEvent.keyDown(window, { code: "KeyD", key: "d" });
      });
      expect(m.sent.map((x) => x.type)).toContain("demo.trigger");
    });

    it("リプレイの表示が出て、チュートリアルとデモのキーを出さない", () => {
      const m = manualConnector("replay");
      render(<GameScreen round={stage1Round} onRoundEnd={() => {}} connector={m.connector} />);
      expect(screen.getByText("リプレイを再生しています（船の操作はできません）。")).toBeDefined();
      m.setStatus("ready");
      m.load(demoSnapshot());
      expect(screen.getByText("リプレイを再生しています（船の操作はできません）。")).toBeDefined();
      expect(tutorial()).toBeNull();
      expect(screen.queryByText(/サーバーに接続していません/)).toBeNull();
      expect(screen.queryByText(/開発用の記録を再生しています/)).toBeNull();
      act(() => {
        fireEvent.keyDown(window, { code: "KeyD", key: "d" });
      });
      expect(m.sent.map((x) => x.type)).not.toContain("demo.trigger");
      expect(m.sent.map((x) => x.type)).not.toContain("cmd.game.pause");
      // 船の操作のパネルは出す（操作は記録の再生が ack の失敗にする）。
      expect(screen.getByRole("region", { name: "フェリー操作" })).toBeDefined();
    });

    it("server では「次の出来事まで」のボタンを出さない", () => {
      const m = manualConnector("server");
      render(<GameScreen round={withSession} onRoundEnd={() => {}} connector={m.connector} />);
      m.setStatus("ready");
      m.load(demoSnapshot());
      expect(screen.queryByRole("button", { name: "次の出来事まで" })).toBeNull();
    });

    it("リプレイでは「次の出来事まで」で cmd.game.skip を送る（航海中だけ押せる）", () => {
      const m = manualConnector("replay");
      render(<GameScreen round={stage1Round} onRoundEnd={() => {}} connector={m.connector} />);
      m.setStatus("ready");
      m.snapshot("intro");
      const button = () =>
        screen.getByRole("button", { name: "次の出来事まで" }) as HTMLButtonElement;
      expect(button().disabled).toBe(true);
      m.snapshot("sailing");
      expect(button().disabled).toBe(false);
      fireEvent.click(button());
      expect(m.sent.map((x) => ({ type: x.type, payload: x.payload }))).toContainEqual({
        type: "cmd.game.skip",
        payload: { until: "next_notable" },
      });
    });

    it("記録の再生で一時停止・再開が HUD と時計に反映され、「次の出来事まで」で次の出来事まで進む", async () => {
      const parsed = parseReplayResponse(JSON.stringify(successResponse()));
      if (!parsed.ok) throw new Error("fixture");
      const timers = createManualTimers();
      const connector = createReplayConnector(parsed, { timers });
      render(<GameScreen round={stage1Round} onRoundEnd={() => {}} connector={connector} />);
      const step = async (ms: number) => {
        await act(async () => {
          timers.advance(ms);
        });
      };
      const clock = () => gameStore.getState().world?.clock;
      await step(0);
      await step(0);
      await step(1500);
      expect(gameStore.getState().world?.phase).toBe("sailing");
      expect(clock()?.paused).toBe(false);

      fireEvent.click(screen.getByRole("button", { name: "⏸ 一時停止" }));
      await step(0);
      expect(clock()?.paused).toBe(true);
      const pausedAt = clock()?.simTime;
      expect(screen.getByRole("button", { name: "▶ 再開" })).toBeDefined();
      await step(3000);
      expect(clock()?.simTime).toBe(pausedAt);
      expect(clock()?.paused).toBe(true);

      fireEvent.click(screen.getByRole("button", { name: "▶ 再開" }));
      await step(0);
      expect(clock()?.paused).toBe(false);
      expect(screen.getByRole("button", { name: "⏸ 一時停止" })).toBeDefined();

      fireEvent.click(screen.getByRole("button", { name: "次の出来事まで" }));
      await step(0);
      // mini-record の次の ferry.arrived は t=7000。
      expect(clock()?.simTime).toBe(7000);
      expect(clock()?.paused).toBe(true);
      expect(screen.getByRole("button", { name: "▶ 再開" })).toBeDefined();
    });
  });
});
