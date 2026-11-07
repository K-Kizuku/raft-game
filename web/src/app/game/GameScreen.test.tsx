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
import type { Ack, Phase, StageView } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { createMockRound } from "../dev/mockRound.ts";
import type { RoundContext } from "../types.ts";
import { createRoundConnector, type RoundConnector, type RoundLink } from "./connector.ts";
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
function manualConnector() {
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
  const link: RoundLink = { source: "server", connection, dispose() {} };
  const open = vi.fn((_session: unknown, h: ConnectionHandlers) => {
    handlers = h;
    return link;
  });
  const connector: RoundConnector = {
    modeOf: (session) => (session === null ? "offline" : "server"),
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
    expect(statusWith("サーバーに接続しています…")).toBeDefined();

    m.setStatus("ready");
    expect(screen.queryByText("サーバーに接続しています…")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();

    m.setStatus("reconnecting");
    expect(statusWith("接続が切れました。つなぎ直しています…")).toBeDefined();

    m.setStatus("failed", "replaced");
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain(
      "ほかのタブでこのゲームを開いたため、この画面の接続を終えました。",
    );
    const link = within(alert).getByRole("link", { name: "ステージ選択へ" });
    expect(link.getAttribute("href")).toBe("/stages");
    expect(screen.queryByText("接続が切れました。つなぎ直しています…")).toBeNull();
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
});
