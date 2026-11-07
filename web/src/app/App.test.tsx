// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App, type AppDeps } from "./App.tsx";
import type { StageSummary } from "./types.ts";

const STAGES: StageSummary[] = [
  {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
  },
  {
    id: "stage2",
    title: "島から返事が来ない！",
    region: "伊豆諸島",
    islands: 5,
    ferries: 2,
    durationMs: 210000,
  },
];

function stagesLoader() {
  return vi.fn<AppDeps["loadStages"]>(async () => STAGES);
}

function renderApp(loadStages: AppDeps["loadStages"] = stagesLoader()) {
  return render(<App deps={{ loadStages }} />);
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

function popTo(path: string) {
  act(() => {
    window.history.replaceState(null, "", path);
    window.dispatchEvent(new PopStateEvent("popstate"));
  });
}

// タイトル → ステージ選択 → 紹介 → ゲーム → リザルトまで進める。
async function playToResult(stageTitle: string) {
  clickLink("遊ぶ");
  fireEvent.click(await screen.findByRole("button", { name: `${stageTitle}を選ぶ` }));
  clickButton("出航する");
  clickButton("リザルトへ（仮）");
  expect(window.location.pathname).toBe("/result");
}

beforeEach(() => {
  window.history.replaceState(null, "", "/");
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
    const loadStages = stagesLoader();
    renderApp(loadStages);

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
    renderApp();
    const start = window.history.length;

    clickLink("遊ぶ");
    expect(window.location.pathname).toBe("/stages");
    const choose = await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" });
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByText("制限時間 3:00")).toBeDefined();
    expect(screen.getByText("制限時間 3:30")).toBeDefined();

    fireEvent.click(choose);
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(heading().textContent).toBe("はじめての郵便船");
    expect(screen.getAllByText("ゲーム上の設定").length).toBeGreaterThan(0);

    clickButton("出航する");
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
    await waitFor(() => {
      expect(window.location.pathname).toBe("/stages");
    });
    expect(heading().textContent).toBe("ステージ選択");
  });

  it("AP-3 リザルトのボタン", async () => {
    renderApp();
    await playToResult("はじめての郵便船");

    clickButton("同じ条件でもう一度");
    expect(window.location.pathname).toBe("/play");
    clickButton("リザルトへ（仮）");
    expect(window.location.pathname).toBe("/result");

    clickButton("次のステージ");
    expect(window.location.pathname).toBe("/stages/stage2");
    expect(heading().textContent).toBe("島から返事が来ない！");

    clickButton("出航する");
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

    await waitFor(() => {
      expect(window.location.pathname).toBe("/stages");
    });
    expect(window.history.length).toBe(start);
    expect(await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();
  });

  it.each(["/foo", "/stages/Bad_ID", "/Stages", "/stages/a/b"])(
    "AP-4 %s を直接開くと / に転送する",
    async (path) => {
      window.history.replaceState(null, "", path);
      const start = window.history.length;
      renderApp();

      await waitFor(() => {
        expect(window.location.pathname).toBe("/");
      });
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
    const loadStages = vi.fn<AppDeps["loadStages"]>(() => new Promise<StageSummary[]>(() => {}));
    renderApp(loadStages);
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
    renderApp(loadStages);
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
    renderApp(loadStages);

    expect(await screen.findByText("ステージの一覧を読み込めませんでした。")).toBeDefined();
    clickButton("もう一度読み込む");
    expect(
      await screen.findByRole("heading", { level: 1, name: "はじめての郵便船" }),
    ).toBeDefined();
  });

  it("AP-5 0 件", async () => {
    renderApp(vi.fn<AppDeps["loadStages"]>(async () => []));
    clickLink("遊ぶ");

    expect(await screen.findByText("遊べるステージがありません。")).toBeDefined();
  });

  it("AP-6 一覧はキャッシュする", async () => {
    const loadStages = stagesLoader();
    renderApp(loadStages);

    clickLink("遊ぶ");
    fireEvent.click(await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" }));
    clickLink("ステージ選択へ戻る");
    fireEvent.click(screen.getByRole("button", { name: "島から返事が来ない！を選ぶ" }));
    expect(heading().textContent).toBe("島から返事が来ない！");
    clickLink("ステージ選択へ戻る");
    clickLink("タイトルへ");
    clickLink("遊ぶ");
    expect(screen.getByRole("button", { name: "はじめての郵便船を選ぶ" })).toBeDefined();

    expect(loadStages).toHaveBeenCalledTimes(1);
  });

  it("AP-6 読み込みの途中で画面を離れたら中断し、結果を反映しない", async () => {
    const signals: AbortSignal[] = [];
    const resolvers: Array<(stages: StageSummary[]) => void> = [];
    const loadStages = vi.fn<AppDeps["loadStages"]>(
      (signal) =>
        new Promise<StageSummary[]>((resolve) => {
          signals.push(signal);
          resolvers.push(resolve);
        }),
    );
    renderApp(loadStages);

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
    renderApp();
    await playToResult("はじめての郵便船");

    popTo("/play");
    await waitFor(() => {
      expect(window.location.pathname).toBe("/result");
    });
    expect(heading().textContent).toBe("リザルト");
  });

  it("AP-7 ゲームの途中で戻って進むと、ゲーム画面に戻る", async () => {
    renderApp();
    clickLink("遊ぶ");
    fireEvent.click(await screen.findByRole("button", { name: "はじめての郵便船を選ぶ" }));
    clickButton("出航する");

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

  it("AP-9 popstate で画面が変わる", () => {
    renderApp();
    popTo("/credits");

    expect(heading().textContent).toBe("クレジット");
  });
});
