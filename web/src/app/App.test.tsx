// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";

import miniRecord from "../net/fixtures/mini-record.json";
import miniStage from "../net/fixtures/mini-stage.json";
import type { ConnectionHandlers, ConnectionStatus, GameConnection } from "../net/index.ts";
import { parseRecord, snapshotFromRecord } from "../net/mock/record.ts";
import { placeholderRoundResult } from "../net/mock/server.ts";
import type { StageView } from "../protocol/index.ts";
import { gameStore } from "../state/index.ts";
import { App, type AppDeps } from "./App.tsx";
import { createRoundConnector, type RoundConnector } from "./game/connector.ts";
import { stageProgress } from "./progress/progress.ts";
import type { SessionRequest } from "./session/api.ts";
import {
  SESSION_ERROR_MESSAGES,
  SessionCreateError,
  type SessionErrorKind,
} from "./session/errors.ts";
import type { TurnstileClient } from "./session/turnstile.ts";
import type { SessionHandle, StageCatalogEntry } from "./types.ts";

const STAGES: StageCatalogEntry[] = [
  {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    demo: false,
    intro: {
      text: "温暖で雨の少ない瀬戸内の海。",
      provenance: "derived",
      source: {
        dataset: "mlit-ritou-2026",
        region: "小豆島地域",
        field: "NATURAL_CLIMATE_FEATURES",
        quote: "温暖、小雨の典型的な瀬戸内式気候",
      },
    },
    learning: ["log_replication", "commit"],
    unlock: null,
  },
  {
    id: "stage2",
    title: "島から返事が来ない！",
    region: "伊豆諸島",
    islands: 5,
    ferries: 2,
    durationMs: 210000,
    demo: false,
    intro: { text: "島から返事が来ない海。", provenance: "creative", source: null },
    learning: ["heartbeat"],
    unlock: null,
  },
];

// stage2 を stage1 の★1 で解放する一覧（T-703）。
const LOCKED_STAGES: StageCatalogEntry[] = STAGES.map((stage) =>
  stage.id === "stage2" ? { ...stage, unlock: "stage1" } : stage,
);

const OFFLINE = "サーバーなしで進む（開発用）";
const PREPARING = "ステージを準備しています…";
const NOT_CONNECTED = "サーバーに接続していません（画面の骨組みの表示です）。";

type PostSessionMock = Mock<AppDeps["postSession"]>;
type FakeTurnstile = {
  getToken: Mock<TurnstileClient["getToken"]>;
  reset: Mock<TurnstileClient["reset"]>;
};

function sessionOf(n: number): SessionHandle {
  const sessionId = `s_${String(n).padStart(22, "0")}`;
  return { sessionId, token: "T".repeat(22), wsPath: `/ws/sessions/${sessionId}` };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function stagesLoader(stages: StageCatalogEntry[] = STAGES) {
  return vi.fn<AppDeps["loadStages"]>(async () => stages);
}

// 記録して、1・2・… 番目のセッションを返す偽物。
function sessionPoster(): PostSessionMock {
  let count = 0;
  return vi.fn<AppDeps["postSession"]>(async () => sessionOf(++count));
}

function failingPoster(kind: SessionErrorKind = "unavailable"): PostSessionMock {
  return vi.fn<AppDeps["postSession"]>(async () => {
    throw new SessionCreateError(kind);
  });
}

// "tok-<回数>" を返す偽物。
function fakeTurnstile(): FakeTurnstile {
  let count = 0;
  return {
    getToken: vi.fn<TurnstileClient["getToken"]>(async () => `tok-${++count}`),
    reset: vi.fn<TurnstileClient["reset"]>(),
  };
}

// 101, 102, … を返す偽物。
function seeds() {
  let next = 100;
  return vi.fn<AppDeps["newSeed"]>(() => ++next);
}

// セッションがあれば server、無ければ offline（テストの MODE と同じ）で、本物の WebSocket を開かない接続。
function inertConnector(): RoundConnector {
  const status: ConnectionStatus = { state: "idle", attempt: 0, reason: null };
  return createRoundConnector({
    mockEnabled: false,
    openServer: () => ({
      source: "server",
      connection: {
        start() {},
        close() {},
        sendCommand: () => new Promise(() => {}),
        setEngineerSubscription() {},
        getStatus: () => status,
      },
      dispose() {},
    }),
  });
}

type TestDeps = {
  loadStages: Mock<AppDeps["loadStages"]>;
  connector: RoundConnector;
  postSession: PostSessionMock;
  turnstile: FakeTurnstile | null;
  newSeed: Mock<AppDeps["newSeed"]>;
  allowOffline: boolean;
};

function renderApp(overrides: Partial<TestDeps> = {}): TestDeps {
  const deps: TestDeps = {
    loadStages: stagesLoader(),
    connector: inertConnector(),
    postSession: sessionPoster(),
    turnstile: fakeTurnstile(),
    newSeed: seeds(),
    allowOffline: true,
    ...overrides,
  };
  render(<App deps={deps} />);
  return deps;
}

function heading(): HTMLElement {
  return screen.getByRole("heading", { level: 1 });
}

function clickLink(name: string) {
  fireEvent.click(screen.getByRole("link", { name }));
}

function clickButton(name: string) {
  fireEvent.click(screen.getByRole("button", { name }));
}

async function choose(stageTitle: string) {
  fireEvent.click(await screen.findByRole("button", { name: `${stageTitle}を選ぶ` }));
}

async function goOffline() {
  fireEvent.click(await screen.findByRole("button", { name: OFFLINE }));
}

async function waitForPath(path: string) {
  await waitFor(() => {
    expect(window.location.pathname).toBe(path);
  });
}

function popTo(path: string) {
  act(() => {
    window.history.replaceState(null, "", path);
    window.dispatchEvent(new PopStateEvent("popstate"));
  });
}

function turnstileHosts(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".turnstile-host")];
}

function expectOnlyHost(host: HTMLElement | undefined) {
  const hosts = turnstileHosts();
  expect(hosts).toHaveLength(1);
  expect(hosts[0]).toBe(host);
}

// タイトル → ステージ選択 → 紹介 → ゲーム → リザルトまで、サーバーなしで進める
// （postSession は失敗させておく）。
async function playToResult(stageTitle: string) {
  clickLink("遊ぶ");
  await choose(stageTitle);
  await goOffline();
  clickButton("出航する");
  await goOffline();
  clickButton("リザルトへ（仮）");
  expect(window.location.pathname).toBe("/result");
}

beforeEach(() => {
  window.history.replaceState(null, "", "/");
  localStorage.clear();
  act(() => {
    stageProgress.reload();
  });
});

afterEach(() => {
  cleanup();
});

describe("App", () => {
  it("タイトル「合意、乗船中。」を描く", () => {
    render(<App />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("合意、乗船中。");
  });

  it("AP-1 タイトルのリンクと、一覧を読み込まないこと", () => {
    const { loadStages } = renderApp();

    expect(heading().textContent).toBe("合意、乗船中。");
    for (const [name, href] of [
      ["遊ぶ", "/stages"],
      ["遊び方", "/howto"],
      ["クレジット", "/credits"],
      ["設定", "/settings"],
    ] as const) {
      expect(screen.getByRole("link", { name }).getAttribute("href")).toBe(href);
    }
    expect(loadStages).not.toHaveBeenCalled();
  });

  it("AP-1 タイトルから遊び方・クレジット・設定へ進める", () => {
    renderApp();
    for (const [name, path, title] of [
      ["遊び方", "/howto", "遊び方"],
      ["クレジット", "/credits", "クレジット"],
      ["設定", "/settings", "設定"],
    ] as const) {
      clickLink(name);
      expect(window.location.pathname).toBe(path);
      expect(heading().textContent).toBe(title);
      clickLink("タイトルへ");
      expect(heading().textContent).toBe("合意、乗船中。");
    }
  });

  it("AP-2 通しの流れと履歴", async () => {
    renderApp({ postSession: failingPoster() });
    const start = window.history.length;

    clickLink("遊ぶ");
    expect(window.location.pathname).toBe("/stages");
    await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" });
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByText("制限時間 3:00")).toBeDefined();
    expect(screen.getByText("制限時間 3:30")).toBeDefined();

    await choose("はじめての郵便船");
    await goOffline();
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(heading().textContent).toBe("はじめての郵便船");
    expect(screen.getAllByText("ゲーム上の設定").length).toBeGreaterThan(0);

    clickButton("出航する");
    await goOffline();
    expect(window.location.pathname).toBe("/play");
    expect(screen.getByRole("region", { name: "マップ" })).toBeDefined();

    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");
    expect(heading().textContent).toBe("リザルト");
    expect(window.history.length).toBe(start + 2);

    // リザルトから戻ると /stages に戻る。
    act(() => {
      window.history.back();
    });
    await waitForPath("/stages");
    expect(heading().textContent).toBe("ステージ選択");
  });

  it("AP-3 リザルトのボタン", async () => {
    renderApp({ postSession: failingPoster() });
    await playToResult("はじめての郵便船");

    clickButton("同じ条件でもう一度");
    await goOffline();
    expect(window.location.pathname).toBe("/play");
    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");

    clickButton("次のステージ");
    await goOffline();
    expect(window.location.pathname).toBe("/stages/stage2");
    expect(heading().textContent).toBe("島から返事が来ない！");

    clickButton("出航する");
    await goOffline();
    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");
    expect(screen.getByText("島から返事が来ない！")).toBeDefined();
    expect(screen.queryByRole("button", { name: "次のステージ" })).toBeNull();

    clickButton("ステージ選択");
    expect(window.location.pathname).toBe("/stages");
    expect(heading().textContent).toBe("ステージ選択");
  });

  it.each(["/play", "/result"])("AP-4 %s を直接開くと /stages に転送する", async (path) => {
    window.history.replaceState(null, "", path);
    const start = window.history.length;
    renderApp();

    await waitForPath("/stages");
    expect(window.history.length).toBe(start);
    expect(await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();
  });

  it.each(["/foo", "/stages/Bad_ID", "/Stages", "/stages/a/b"])(
    "AP-4 %s を直接開くと / に転送する",
    async (path) => {
      window.history.replaceState(null, "", path);
      const start = window.history.length;
      renderApp();

      await waitForPath("/");
      expect(window.history.length).toBe(start);
      expect(heading().textContent).toBe("合意、乗船中。");
    },
  );

  it("AP-4 /stages/stage2 を直接開くと紹介", async () => {
    window.history.replaceState(null, "", "/stages/stage2");
    renderApp();

    expect(
      await screen.findByRole("heading", { level: 1, name: "島から返事が来ない！" }),
    ).toBeDefined();
    expect(window.location.pathname).toBe("/stages/stage2");
  });

  it("AP-4 /stages/zzz は「ステージが見つかりません。」", async () => {
    window.history.replaceState(null, "", "/stages/zzz");
    renderApp();

    expect(await screen.findByText("ステージが見つかりません。")).toBeDefined();
    clickLink("ステージ選択へ");
    expect(window.location.pathname).toBe("/stages");
  });

  it("AP-5 読み込み中", async () => {
    const loadStages = vi.fn<AppDeps["loadStages"]>(
      () => new Promise<StageCatalogEntry[]>(() => {}),
    );
    renderApp({ loadStages });
    clickLink("遊ぶ");

    expect(screen.getByText("読み込み中…")).toBeDefined();
    await waitFor(() => {
      expect(loadStages).toHaveBeenCalledTimes(1);
    });
    expect(screen.getByText("読み込み中…")).toBeDefined();
  });

  it("AP-5 失敗 →「もう一度読み込む」で読み込み直す", async () => {
    const loadStages = vi
      .fn<AppDeps["loadStages"]>()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(STAGES);
    renderApp({ loadStages });
    clickLink("遊ぶ");

    expect(await screen.findByText("ステージの一覧を読み込めませんでした。")).toBeDefined();
    clickButton("もう一度読み込む");
    expect(await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();
    expect(loadStages).toHaveBeenCalledTimes(2);
  });

  it("AP-5 紹介を直接開いて失敗しても「もう一度読み込む」が出る", async () => {
    window.history.replaceState(null, "", "/stages/stage1");
    const loadStages = vi
      .fn<AppDeps["loadStages"]>()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(STAGES);
    renderApp({ loadStages });

    expect(await screen.findByText("ステージの一覧を読み込めませんでした。")).toBeDefined();
    clickButton("もう一度読み込む");
    expect(
      await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" }),
    ).toBeDefined();
  });

  it("AP-5 0 件", async () => {
    renderApp({ loadStages: stagesLoader([]) });
    clickLink("遊ぶ");

    expect(await screen.findByText("遊べるステージがありません。")).toBeDefined();
  });

  it("AP-6 一覧はキャッシュする", async () => {
    const { loadStages } = renderApp();

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    clickLink("ステージ選択へ戻る");
    await choose("島から返事が来ない！");
    await waitForPath("/stages/stage2");
    expect(heading().textContent).toBe("島から返事が来ない！");
    clickLink("ステージ選択へ戻る");
    clickLink("タイトルへ");
    clickLink("遊ぶ");
    expect(screen.getByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();

    expect(loadStages).toHaveBeenCalledTimes(1);
  });

  it("AP-6 読み込みの途中で画面を離れたら中断し、結果を反映しない", async () => {
    const signals: AbortSignal[] = [];
    const resolvers: Array<(stages: StageCatalogEntry[]) => void> = [];
    const loadStages = vi.fn<AppDeps["loadStages"]>(
      (signal) =>
        new Promise<StageCatalogEntry[]>((resolve) => {
          signals.push(signal);
          resolvers.push(resolve);
        }),
    );
    renderApp({ loadStages });

    clickLink("遊ぶ");
    await waitFor(() => {
      expect(loadStages).toHaveBeenCalledTimes(1);
    });
    clickLink("タイトルへ");
    expect(signals[0]?.aborted).toBe(true);

    clickLink("遊ぶ");
    await waitFor(() => {
      expect(loadStages).toHaveBeenCalledTimes(2);
    });
    // 中断した 1 回目の結果は反映しない。
    await act(async () => {
      resolvers[0]?.([]);
    });
    expect(screen.getByText("読み込み中…")).toBeDefined();
    await act(async () => {
      resolvers[1]?.(STAGES);
    });
    expect(screen.getByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();
  });

  it("AP-7 終わったゲームに戻らない", async () => {
    renderApp({ postSession: failingPoster() });
    await playToResult("はじめての郵便船");

    popTo("/play");
    await waitForPath("/result");
    expect(heading().textContent).toBe("リザルト");
  });

  it("AP-7 ゲームの途中で戻って進むと、ゲーム画面に戻る", async () => {
    renderApp();
    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");

    popTo("/stages");
    expect(heading().textContent).toBe("ステージ選択");
    popTo("/play");
    expect(window.location.pathname).toBe("/play");
    expect(screen.getByRole("region", { name: "マップ" })).toBeDefined();
  });

  it("AP-8 ルートが変わったら <h1> にフォーカスを移す", () => {
    renderApp();
    expect(document.activeElement).not.toBe(heading());

    clickLink("遊ぶ");
    const h1 = heading();
    expect(h1.textContent).toBe("ステージ選択");
    expect(document.activeElement).toBe(h1);
  });

  it("AP-10 reset() は「同じ条件でもう一度」で呼び、ラウンドの終わりでは呼ばない", async () => {
    const record = parseRecord(miniRecord);
    const opened: { handlers: ConnectionHandlers; worldAtOpen: unknown; resultAtOpen: unknown }[] =
      [];
    const status: ConnectionStatus = { state: "ready", attempt: 1, reason: null };
    const connection: GameConnection = {
      start() {},
      close() {},
      sendCommand: () => new Promise(() => {}),
      setEngineerSubscription() {},
      getStatus: () => status,
    };
    // セッションの無いラウンドでも開く（開発のモックの代わり）。
    const connector: RoundConnector = {
      modeOf: () => "mock",
      open(_session, handlers) {
        const state = gameStore.getState();
        opened.push({ handlers, worldAtOpen: state.world, resultAtOpen: state.roundResult });
        return { source: "mock", connection, dispose() {} };
      },
    };
    const finishRound = () => {
      const handlers = opened.at(-1)?.handlers;
      act(() => {
        handlers?.onSnapshot(snapshotFromRecord(record, { stage: miniStage as StageView }), 0);
        handlers?.onRoundResult?.(placeholderRoundResult(record));
      });
    };
    gameStore.getState().reset();
    renderApp({ connector });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    expect(opened).toHaveLength(1);
    finishRound();
    expect(window.location.pathname).toBe("/result");
    expect(gameStore.getState().roundResult).not.toBeNull();
    expect(gameStore.getState().world).not.toBeNull();

    clickButton("同じ条件でもう一度");
    await waitForPath("/play");
    expect(opened).toHaveLength(2);
    expect(opened[1]?.worldAtOpen).toBeNull();
    expect(opened[1]?.resultAtOpen).toBeNull();
    expect(gameStore.getState().roundResult).toBeNull();
    gameStore.getState().reset();
  });

  it("AP-9 popstate で画面が変わる", () => {
    renderApp();
    popTo("/credits");

    expect(heading().textContent).toBe("クレジット");
  });
});

describe("App のセッションの作成（T-413）", () => {
  it("AS-1 ステージを選ぶと 1 回だけ作り、紹介とゲームに渡す", async () => {
    const { postSession, turnstile } = renderApp();

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");

    expect(postSession).toHaveBeenCalledTimes(1);
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      stageId: "stage1",
      seed: 101,
      demo: false,
      turnstileToken: "tok-1",
    });
    expect(turnstile?.reset).toHaveBeenCalledTimes(1);

    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    expect(postSession).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("region", { name: "マップ" })).toBeDefined();
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it("AS-2 作成中はほかのボタンを押せず、POST は 1 回", async () => {
    const pending = deferred<SessionHandle>();
    const postSession = vi.fn<AppDeps["postSession"]>(() => pending.promise);
    renderApp({ postSession });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    expect(await screen.findByText(PREPARING)).toBeDefined();
    await waitFor(() => {
      expect(postSession).toHaveBeenCalledTimes(1);
    });

    const other = screen.getByRole<HTMLButtonElement>("button", {
      name: "島から返事が来ない！を選ぶ",
    });
    expect(other.disabled).toBe(true);
    fireEvent.click(other);
    expect(
      screen.getByRole<HTMLButtonElement>("button", { name: "はじめての郵便船を選ぶ" }).disabled,
    ).toBe(true);
    expect(postSession).toHaveBeenCalledTimes(1);
    expect(window.location.pathname).toBe("/stages");

    await act(async () => {
      pending.resolve(sessionOf(1));
    });
    await waitForPath("/stages/stage1");
    expect(postSession).toHaveBeenCalledTimes(1);
  });

  const kinds: SessionErrorKind[] = [
    "budget_exhausted",
    "sessions_disabled",
    "rate_limited",
    "turnstile_failed",
    "challenge_failed",
    "forbidden_origin",
    "bad_request",
    "unavailable",
  ];
  it.each(kinds)("AS-3 %s の文言を出し、/stages のまま", async (kind) => {
    const turnstile = fakeTurnstile();
    const postSession = failingPoster(kind);
    let attempts: Mock<(...args: never[]) => unknown> = postSession;
    if (kind === "challenge_failed") {
      turnstile.getToken.mockImplementation(async () => {
        throw new Error("blocked");
      });
      attempts = turnstile.getToken;
    }
    renderApp({ postSession, turnstile, allowOffline: false });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(SESSION_ERROR_MESSAGES[kind]);
    expect(window.location.pathname).toBe("/stages");
    expect(attempts).toHaveBeenCalledTimes(1);
    if (kind === "challenge_failed") {
      expect(postSession).not.toHaveBeenCalled();
    }
    expect(turnstile.reset).toHaveBeenCalledTimes(1);

    // もう一度押すと文言が消えて送り直す。
    const retry = deferred<never>();
    attempts.mockImplementationOnce(() => retry.promise);
    await choose("はじめての郵便船");
    expect(await screen.findByText(PREPARING)).toBeDefined();
    expect(screen.queryByRole("alert")).toBeNull();
    await waitFor(() => {
      expect(attempts).toHaveBeenCalledTimes(2);
    });
    await act(async () => {
      retry.reject(kind === "challenge_failed" ? new Error("x") : new SessionCreateError(kind));
    });
    expect((await screen.findByRole("alert")).textContent).toBe(SESSION_ERROR_MESSAGES[kind]);

    // 画面を移ると文言が消える。
    clickLink("タイトルへ");
    expect(window.location.pathname).toBe("/");
    expect(screen.queryByRole("alert")).toBeNull();
    clickLink("遊ぶ");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("AS-4 同じステージを選び直すと作らず、違うステージは作る", async () => {
    const { postSession } = renderApp();

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    clickLink("ステージ選択へ戻る");
    await choose("はじめての郵便船");
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(postSession).toHaveBeenCalledTimes(1);

    clickLink("ステージ選択へ戻る");
    await choose("島から返事が来ない！");
    await waitForPath("/stages/stage2");
    expect(postSession).toHaveBeenCalledTimes(2);
    expect(postSession.mock.calls[1]?.[0]).toEqual({
      stageId: "stage2",
      seed: 102,
      demo: false,
      turnstileToken: "tok-2",
    });
  });

  it("AS-5 URL から開いた紹介の「出航する」は新しいシードで作る", async () => {
    window.history.replaceState(null, "", "/stages/stage2");
    const { postSession } = renderApp();

    await screen.findByRole("heading", { level: 1, name: "島から返事が来ない！" });
    const start = window.history.length;
    clickButton("出航する");
    await waitForPath("/play");

    expect(postSession).toHaveBeenCalledTimes(1);
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      stageId: "stage2",
      seed: 101,
      demo: false,
      turnstileToken: "tok-1",
    });
    expect(window.history.length).toBe(start);
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it("AS-6 allowOffline なら「サーバーなしで進む（開発用）」で進める", async () => {
    const { postSession } = renderApp({ postSession: failingPoster(), allowOffline: true });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await goOffline();
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(screen.queryByRole("alert")).toBeNull();

    clickButton("出航する");
    await goOffline();
    expect(window.location.pathname).toBe("/play");
    expect(screen.getByText(NOT_CONNECTED)).toBeDefined();
    expect(postSession).toHaveBeenCalledTimes(2);
  });

  it("AS-6 allowOffline が偽ならボタンを出さない", async () => {
    renderApp({ postSession: failingPoster(), allowOffline: false });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    expect(await screen.findByRole("alert")).toBeDefined();
    expect(screen.queryByRole("button", { name: OFFLINE })).toBeNull();
  });

  // サーバーなしで stage1（シード 101）を遊んでリザルトへ進める。
  async function playOfflineFromIntro(postSession: PostSessionMock) {
    window.history.replaceState(null, "", "/stages/stage1");
    const deps = renderApp({ postSession });
    await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
    clickButton("出航する");
    await goOffline();
    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");
    return deps;
  }

  it("AS-7 「同じ条件でもう一度」は同じシードと新しいトークンで作り直す", async () => {
    const postSession = vi
      .fn<AppDeps["postSession"]>()
      .mockRejectedValueOnce(new SessionCreateError("unavailable"))
      .mockResolvedValueOnce(sessionOf(1));
    await playOfflineFromIntro(postSession);
    const length = window.history.length;

    clickButton("同じ条件でもう一度");
    await waitForPath("/play");

    expect(postSession).toHaveBeenCalledTimes(2);
    expect(postSession.mock.calls[0]?.[0]).toEqual({
      stageId: "stage1",
      seed: 101,
      demo: false,
      turnstileToken: "tok-1",
    });
    expect(postSession.mock.calls[1]?.[0]).toEqual({
      stageId: "stage1",
      seed: 101,
      demo: false,
      turnstileToken: "tok-2",
    });
    expect(window.history.length).toBe(length);
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it("AS-7 「同じ条件でもう一度」が失敗すると /result のまま文言を出す", async () => {
    const postSession = vi
      .fn<AppDeps["postSession"]>()
      .mockRejectedValueOnce(new SessionCreateError("unavailable"))
      .mockRejectedValueOnce(new SessionCreateError("budget_exhausted"));
    await playOfflineFromIntro(postSession);

    clickButton("同じ条件でもう一度");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("本日の受付は終了しました。");
    expect(window.location.pathname).toBe("/result");
    expect(postSession).toHaveBeenCalledTimes(2);
  });

  it("AS-8 「次のステージ」は新しいシードで作り、続く「出航する」では作らない", async () => {
    const postSession = failingPoster();
    renderApp({ postSession });
    await playToResult("はじめての郵便船");
    expect(postSession).toHaveBeenCalledTimes(2);
    postSession.mockImplementation(async () => sessionOf(9));

    clickButton("次のステージ");
    await waitForPath("/stages/stage2");
    expect(postSession).toHaveBeenCalledTimes(3);
    expect(postSession.mock.calls[2]?.[0]).toEqual({
      stageId: "stage2",
      seed: 103,
      demo: false,
      turnstileToken: "tok-3",
    });
    expect(heading().textContent).toBe("島から返事が来ない！");

    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    expect(postSession).toHaveBeenCalledTimes(3);
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it("AS-9 作成中に画面を移ると、成功しても移らず、選び直しに使う", async () => {
    const pending = deferred<SessionHandle>();
    const postSession = vi.fn<AppDeps["postSession"]>(() => pending.promise);
    renderApp({ postSession });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitFor(() => {
      expect(postSession).toHaveBeenCalledTimes(1);
    });
    clickLink("タイトルへ");
    await act(async () => {
      pending.resolve(sessionOf(1));
    });
    expect(window.location.pathname).toBe("/");
    expect(heading().textContent).toBe("合意、乗船中。");

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(postSession).toHaveBeenCalledTimes(1);
    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it("AS-10 一覧で demo のステージは demo: true で送る", async () => {
    const demoStage: StageCatalogEntry = {
      ...STAGES[1]!,
      id: "demo-izu",
      title: "伊豆の島じま",
      demo: true,
    };
    const { postSession } = renderApp({ loadStages: stagesLoader([STAGES[0]!, demoStage]) });

    clickLink("遊ぶ");
    await choose("伊豆の島じま");
    await waitForPath("/stages/demo-izu");
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      stageId: "demo-izu",
      seed: 101,
      demo: true,
      turnstileToken: "tok-1",
    });
  });

  it("AS-11 .turnstile-host はどの画面でもちょうど 1 つで、getToken に渡る", async () => {
    const { turnstile, postSession } = renderApp({ postSession: failingPoster() });
    expect(turnstileHosts()).toHaveLength(1);
    const host = turnstileHosts()[0];

    clickLink("遊び方");
    expectOnlyHost(host);
    clickLink("タイトルへ");
    clickLink("遊ぶ");
    expectOnlyHost(host);
    await choose("はじめての郵便船");
    await goOffline();
    expectOnlyHost(host);
    clickButton("出航する");
    await goOffline();
    expectOnlyHost(host);
    clickButton("リザルトへ（仮）");
    expectOnlyHost(host);

    expect(turnstile?.getToken).toHaveBeenCalledTimes(2);
    for (const call of turnstile?.getToken.mock.calls ?? []) {
      expect(call[0]).toBe(host);
    }
    expect(postSession).toHaveBeenCalledTimes(2);
  });

  it("AS-11 turnstile が null なら turnstileToken: null で送る", async () => {
    const { postSession } = renderApp({ turnstile: null });

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      stageId: "stage1",
      seed: 101,
      demo: false,
      turnstileToken: null,
    });
    expect(turnstileHosts()).toHaveLength(1);
  });
});

describe("App のステージの解放（T-703）", () => {
  const UNLOCKED_NOTICE = "🎉「島から返事が来ない！」が遊べるようになりました！";
  const record = parseRecord(miniRecord);

  // セッションの無いラウンドでも開き、finish(stars) で round.result を届ける接続。
  function resultConnector() {
    const opened: ConnectionHandlers[] = [];
    const status: ConnectionStatus = { state: "ready", attempt: 1, reason: null };
    const connection: GameConnection = {
      start() {},
      close() {},
      sendCommand: () => new Promise(() => {}),
      setEngineerSubscription() {},
      getStatus: () => status,
    };
    const connector: RoundConnector = {
      modeOf: () => "mock",
      open(_session, handlers) {
        opened.push(handlers);
        return { source: "mock", connection, dispose() {} };
      },
    };
    const finish = (stars: number) => {
      const handlers = opened.at(-1);
      act(() => {
        handlers?.onSnapshot(snapshotFromRecord(record, { stage: miniStage as StageView }), 0);
        handlers?.onRoundResult?.({ ...placeholderRoundResult(record), stars });
      });
    };
    return { connector, finish };
  }

  async function playStage1(finish: (stars: number) => void, stars: number) {
    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    finish(stars);
    expect(window.location.pathname).toBe("/result");
  }

  beforeEach(() => {
    gameStore.getState().reset();
  });

  afterEach(() => {
    gameStore.getState().reset();
  });

  it("AP-U1 未解放のステージ紹介を URL で開くと鍵の画面で、セッションを作らない", async () => {
    window.history.replaceState(null, "", "/stages/stage2");
    const { postSession, turnstile } = renderApp({ loadStages: stagesLoader(LOCKED_STAGES) });

    expect(
      await screen.findByRole("heading", { level: 1, name: "このステージはまだ遊べません。" }),
    ).toBeDefined();
    expect(
      screen.getByText(
        "「島から返事が来ない！」は、「はじめての郵便船」を★1 以上でクリアすると遊べます。",
      ),
    ).toBeDefined();
    expect(screen.queryByRole("button", { name: "出航する" })).toBeNull();
    expect(postSession).not.toHaveBeenCalled();
    expect(turnstile?.getToken).not.toHaveBeenCalled();

    clickLink("「はじめての郵便船」へ");
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(heading().textContent).toBe("はじめての郵便船");
    expect(postSession).not.toHaveBeenCalled();
  });

  it("AP-U1 前のステージをクリア済みなら紹介を出す", async () => {
    stageProgress.recordResult("stage1", 1);
    window.history.replaceState(null, "", "/stages/stage2");
    renderApp({ loadStages: stagesLoader(LOCKED_STAGES) });

    expect(
      await screen.findByRole("heading", { level: 1, name: "島から返事が来ない！" }),
    ).toBeDefined();
    expect(screen.getByRole("button", { name: "出航する" })).toBeDefined();
  });

  it("AP-U2 ★1 で終えると保存し、解放の知らせと「次のステージ」を出す", async () => {
    const { connector, finish } = resultConnector();
    renderApp({ connector, loadStages: stagesLoader(LOCKED_STAGES) });

    await playStage1(finish, 1);
    expect(localStorage.getItem("raft-game.progress")).toBe('{"v":1,"bestStars":{"stage1":1}}');
    expect(screen.getByText(UNLOCKED_NOTICE).getAttribute("role")).toBe("status");
    expect(screen.getByRole("button", { name: "次のステージ" })).toBeDefined();

    // リザルトを離れたら知らせを消す。
    clickButton("ステージ選択");
    expect(window.location.pathname).toBe("/stages");
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
    expect(
      screen.getByRole<HTMLButtonElement>("button", { name: "島から返事が来ない！を選ぶ" })
        .disabled,
    ).toBe(false);
    expect(screen.getByText("クリア済み ★1")).toBeDefined();
  });

  it("AP-U2 「次のステージ」で離れると知らせを消す", async () => {
    const { connector, finish } = resultConnector();
    renderApp({ connector, loadStages: stagesLoader(LOCKED_STAGES) });

    await playStage1(finish, 2);
    expect(screen.getByText(UNLOCKED_NOTICE)).toBeDefined();
    clickButton("次のステージ");
    await waitForPath("/stages/stage2");
    expect(heading().textContent).toBe("島から返事が来ない！");
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
  });

  it("AP-U2 ★0 で終えると保存せず、知らせも「次のステージ」も出さない", async () => {
    const { connector, finish } = resultConnector();
    renderApp({ connector, loadStages: stagesLoader(LOCKED_STAGES) });

    await playStage1(finish, 0);
    expect(localStorage.getItem("raft-game.progress")).toBeNull();
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
    expect(screen.queryByRole("button", { name: "次のステージ" })).toBeNull();
    expect(screen.getByRole("button", { name: "同じ条件でもう一度" })).toBeDefined();
  });

  it("AP-U2 結果の無いラウンド（リザルトへ（仮））は記録しない", async () => {
    renderApp({ postSession: failingPoster(), loadStages: stagesLoader(LOCKED_STAGES) });

    await playToResult("はじめての郵便船");
    expect(localStorage.getItem("raft-game.progress")).toBeNull();
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
    expect(screen.queryByRole("button", { name: "次のステージ" })).toBeNull();
  });

  it("AP-U2 再読み込みしても次のステージは解放されたまま", async () => {
    const { connector, finish } = resultConnector();
    renderApp({ connector, loadStages: stagesLoader(LOCKED_STAGES) });
    await playStage1(finish, 1);
    cleanup();

    // 再読み込みの代わりに、保存から読み直して描き直す。
    act(() => {
      stageProgress.reload();
    });
    window.history.replaceState(null, "", "/stages");
    renderApp({ loadStages: stagesLoader(LOCKED_STAGES) });
    expect(
      (
        await screen.findByRole<HTMLButtonElement>("button", {
          name: "島から返事が来ない！を選ぶ",
        })
      ).disabled,
    ).toBe(false);
  });

  it("AP-U3 同じステージを 2 回目にクリアしても知らせを出さない", async () => {
    const { connector, finish } = resultConnector();
    renderApp({ connector, loadStages: stagesLoader(LOCKED_STAGES) });

    await playStage1(finish, 1);
    expect(screen.getByText(UNLOCKED_NOTICE)).toBeDefined();

    clickButton("同じ条件でもう一度");
    await waitForPath("/play");
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
    finish(3);
    expect(window.location.pathname).toBe("/result");
    expect(screen.queryByText(UNLOCKED_NOTICE)).toBeNull();
    expect(screen.getByRole("button", { name: "次のステージ" })).toBeDefined();
    expect(localStorage.getItem("raft-game.progress")).toBe('{"v":1,"bestStars":{"stage1":3}}');
  });
});

describe("App のシードの指定（T-705）", () => {
  const record = parseRecord(miniRecord);
  const SEED_INVALID = "シードの指定（?seed=）が正しくないため、新しいシードで遊びます。";

  // セッションの有無にかかわらず開き、finish(seed) でそのシードのスナップショットと結果を届ける接続。
  function seededConnector() {
    const opened: ConnectionHandlers[] = [];
    const status: ConnectionStatus = { state: "ready", attempt: 1, reason: null };
    const connection: GameConnection = {
      start() {},
      close() {},
      sendCommand: () => new Promise(() => {}),
      setEngineerSubscription() {},
      getStatus: () => status,
    };
    const connector: RoundConnector = {
      modeOf: () => "mock",
      open(_session, handlers) {
        opened.push(handlers);
        return { source: "mock", connection, dispose() {} };
      },
    };
    const finish = (seed: number) => {
      const handlers = opened.at(-1);
      const snapshot = snapshotFromRecord(record, { stage: miniStage as StageView });
      snapshot.session = { ...snapshot.session, seed };
      act(() => {
        handlers?.onSnapshot(snapshot, 0);
        handlers?.onRoundResult?.(placeholderRoundResult(record));
      });
    };
    return { connector, finish };
  }

  function seedNotice(): HTMLElement | null {
    return document.querySelector<HTMLElement>(".seed-notice");
  }

  beforeEach(() => {
    gameStore.getState().reset();
  });

  afterEach(() => {
    gameStore.getState().reset();
  });

  it("AP-S1 ?seed=42 の紹介から出航するとシード 42 で作り、「同じ条件でもう一度」も 42", async () => {
    window.history.replaceState(null, "", "/stages/stage1?seed=42");
    const { connector, finish } = seededConnector();
    const { postSession, newSeed } = renderApp({ connector });

    await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
    expect(seedNotice()?.textContent).toBe(
      "シード 42 で遊びます。同じシードなら、依頼と嵐が同じ時刻に起きます。",
    );
    expect(seedNotice()?.getAttribute("role")).toBe("status");

    clickButton("出航する");
    await waitForPath("/play");
    expect(postSession).toHaveBeenCalledTimes(1);
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      stageId: "stage1",
      seed: 42,
      demo: false,
      turnstileToken: "tok-1",
    });
    expect(seedNotice()).toBeNull();

    finish(42);
    expect(window.location.pathname).toBe("/result");
    const section = screen.getByRole("region", { name: "同じ条件で遊ぶ" });
    expect(section.querySelector(".result-seed-value")?.textContent).toBe("42");
    expect(section.querySelector("code")?.textContent).toBe(
      `${window.location.origin}/stages/stage1?seed=42`,
    );

    clickButton("同じ条件でもう一度");
    await waitForPath("/play");
    expect(postSession).toHaveBeenCalledTimes(2);
    expect(postSession.mock.calls[1]?.[0]).toEqual({
      stageId: "stage1",
      seed: 42,
      demo: false,
      turnstileToken: "tok-2",
    });
    expect(newSeed).not.toHaveBeenCalled();
  });

  it("AP-S1 サーバーなしで進んでも、「同じ条件でもう一度」は ?seed= のシード", async () => {
    window.history.replaceState(null, "", "/stages/stage1?seed=42");
    const postSession = failingPoster();
    const { newSeed } = renderApp({ postSession });

    await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
    clickButton("出航する");
    // 作成に失敗しても、シードの知らせは出したまま。
    expect(await screen.findByRole("alert")).toBeDefined();
    expect(seedNotice()).not.toBeNull();
    await goOffline();
    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");

    clickButton("同じ条件でもう一度");
    await waitFor(() => {
      expect(postSession).toHaveBeenCalledTimes(2);
    });
    expect(postSession.mock.calls.map((call) => call[0].seed)).toEqual([42, 42]);
    expect(newSeed).not.toHaveBeenCalled();
  });

  it("AP-S1 同じステージの使っていないセッションでも、シードが違えば ?seed= で作り直す", async () => {
    const { postSession } = renderApp();

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    expect(postSession).toHaveBeenCalledTimes(1);

    // 履歴で一覧へ戻り、紹介の URL を ?seed= つきで開き直す（App の状態はそのまま）。
    popTo("/stages");
    popTo("/stages/stage1?seed=42");
    expect(seedNotice()).not.toBeNull();
    clickButton("出航する");
    await waitForPath("/play");
    expect(postSession).toHaveBeenCalledTimes(2);
    expect(postSession.mock.calls[1]?.[0].seed).toBe(42);
  });

  it("AP-S1 同じステージの使っていないセッションが同じシードなら、作らずに使う", async () => {
    const { postSession } = renderApp();

    clickLink("遊ぶ");
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    expect(postSession.mock.calls[0]?.[0].seed).toBe(101);

    popTo("/stages");
    popTo("/stages/stage1?seed=101");
    expect(seedNotice()).not.toBeNull();
    clickButton("出航する");
    expect(window.location.pathname).toBe("/play");
    expect(postSession).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(NOT_CONNECTED)).toBeNull();
  });

  it.each(["abc", "-1", "1.5", "007", "", "9007199254740992"])(
    "AP-S2 ?seed=%s は知らせを出し、新しいシードで作る",
    async (value) => {
      window.history.replaceState(null, "", `/stages/stage1?seed=${value}`);
      const { postSession, newSeed } = renderApp();

      await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
      expect(seedNotice()?.textContent).toBe(SEED_INVALID);

      clickButton("出航する");
      await waitForPath("/play");
      expect(newSeed).toHaveBeenCalledTimes(1);
      expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
        stageId: "stage1",
        seed: 101,
        demo: false,
        turnstileToken: "tok-1",
      });
    },
  );

  it("AP-S3 ?seed= が無ければ知らせを出さず、新しいシードで作る", async () => {
    window.history.replaceState(null, "", "/stages/stage1");
    const { postSession } = renderApp();

    await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
    expect(seedNotice()).toBeNull();
    clickButton("出航する");
    await waitForPath("/play");
    expect(postSession.mock.calls[0]?.[0].seed).toBe(101);
  });

  it("AP-S3 紹介から画面の中で移ると ?seed= は消え、ステージ選択の「選ぶ」は新しいシード", async () => {
    window.history.replaceState(null, "", "/stages/stage1?seed=42");
    const { postSession } = renderApp();

    await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" });
    clickLink("ステージ選択へ戻る");
    expect(window.location.search).toBe("");
    expect(seedNotice()).toBeNull();
    await choose("はじめての郵便船");
    await waitForPath("/stages/stage1");
    expect(seedNotice()).toBeNull();
    expect(postSession.mock.calls[0]?.[0].seed).toBe(101);
  });
});
