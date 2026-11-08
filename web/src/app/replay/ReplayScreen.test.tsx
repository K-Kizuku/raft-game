// @vitest-environment jsdom
// WASM は読まず、loadEngine に偽物を渡す。偽物の応答は net/fixtures の記録とステージから組み立てる。
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createManualTimers, type ManualTimers } from "../../net/index.ts";
import { MAX_INPUT_LOG_BYTES } from "../../replay/limits.ts";
import { queueReplayInput, takeQueuedReplayInput } from "../../replay/pending.ts";
import { failureResponse, successResponse } from "../../replay/testing/responses.ts";
import type { ReplayEngine } from "../../replay/wasm.ts";
import { gameStore } from "../../state/index.ts";
import { ReplayScreen } from "./ReplayScreen.tsx";

const INPUT_LOG = '{"format":1,"kind":"input-log"}';
const REPLAY_NOTICE = "リプレイを再生しています（船の操作はできません）。";

function fakeEngine(response: unknown = successResponse()) {
  const run = vi.fn((text: string) => {
    void text;
    return JSON.stringify(response);
  });
  const engine: ReplayEngine = { run };
  const loadEngine = vi.fn(() => Promise.resolve(engine));
  return { run, loadEngine };
}

function renderScreen(loadEngine: () => Promise<ReplayEngine>, timers: ManualTimers) {
  return render(<ReplayScreen loadEngine={loadEngine} timers={timers} />);
}

function fileInput(): HTMLInputElement {
  return screen.getByLabelText("入力ログのファイル");
}

async function chooseFile(file: File): Promise<void> {
  await act(async () => {
    fireEvent.change(fileInput(), { target: { files: [file] } });
    await Promise.resolve();
  });
}

function jsonFile(text: string): File {
  return new File([text], "input-log.json", { type: "application/json" });
}

async function advance(timers: ManualTimers, ms: number): Promise<void> {
  await act(async () => {
    timers.advance(ms);
    await Promise.resolve();
  });
}

beforeEach(() => {
  gameStore.getState().reset();
  takeQueuedReplayInput();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

describe("ReplayScreen", () => {
  it("選ぶ画面: 見出し・説明・ファイルの入力を出し、WASM の読み込みを始める", async () => {
    const timers = createManualTimers();
    const { loadEngine } = fakeEngine();
    renderScreen(loadEngine, timers);
    await act(async () => {});

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("リプレイ");
    expect(
      screen.getByText(
        "リザルト画面でダウンロードした入力ログ（JSON）を選ぶと、同じゲームをもう一度見られます。ファイルはこの端末の外に送りません。",
      ),
    ).toBeDefined();
    expect(fileInput().getAttribute("accept")).toBe("application/json,.json");
    expect(loadEngine).toHaveBeenCalledTimes(1);
  });

  it("RS-3 ファイルを選ぶと、ゲーム画面が出て、記録の再生のスナップショットがストアに入る", async () => {
    const timers = createManualTimers();
    const { run, loadEngine } = fakeEngine();
    renderScreen(loadEngine, timers);
    await act(async () => {});

    await chooseFile(jsonFile(INPUT_LOG));
    expect(screen.getByRole("status").textContent).toBe("再計算しています…");
    expect(run).not.toHaveBeenCalled();

    await advance(timers, 0);
    expect(run).toHaveBeenCalledWith(INPUT_LOG);
    expect(screen.getByRole("region", { name: "マップ" })).toBeDefined();
    expect(screen.getByText(REPLAY_NOTICE)).toBeDefined();
    expect(gameStore.getState().world).toBeNull();

    // ゲーム画面が接続を開き、記録の再生がスナップショットを送る。
    await advance(timers, 0);
    const world = gameStore.getState().world;
    expect(world?.stage.title).toBe("テストの海");
    expect(world?.session.id).toBe("replay_mini_1");
  });

  it("WASM の読み込みが終わっていなければ「読み込んでいます」を出し、終わったら再計算する", async () => {
    const timers = createManualTimers();
    const { run } = fakeEngine();
    let resolveEngine: (engine: ReplayEngine) => void = () => {};
    const loadEngine = vi.fn(
      () =>
        new Promise<ReplayEngine>((resolve) => {
          resolveEngine = resolve;
        }),
    );
    renderScreen(loadEngine, timers);

    await chooseFile(jsonFile(INPUT_LOG));
    expect(screen.getByRole("status").textContent).toBe(
      "リプレイの部品を読み込んでいます（初回だけ、約 2〜3 MB）…",
    );

    await act(async () => {
      resolveEngine({ run });
    });
    expect(screen.getByRole("status").textContent).toBe("再計算しています…");
    await advance(timers, 0);
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.getByText(REPLAY_NOTICE)).toBeDefined();
  });

  it("RS-4 stage_mismatch では決めた文言と版の欄を出し、ゲーム画面を出さない", async () => {
    const timers = createManualTimers();
    const { loadEngine } = fakeEngine(
      failureResponse("stage_mismatch", { stageId: "stage2", logBuild: "git:abcdef0" }),
    );
    renderScreen(loadEngine, timers);
    await act(async () => {});
    await chooseFile(jsonFile(INPUT_LOG));
    await advance(timers, 0);

    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain(
      "この入力ログは、今と違うステージ定義で記録されました。今の版では再生できません。",
    );
    expect(alert.textContent).toContain(
      "ステージ: stage2・記録した版: git:abcdef0・今の版: git:1234567",
    );
    expect(screen.queryByRole("region", { name: "マップ" })).toBeNull();
    expect(screen.queryByRole("button", { name: "もう一度読み込む" })).toBeNull();
    expect(gameStore.getState().world).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "別の入力ログを選ぶ" }));
    expect(fileInput()).toBeDefined();
  });

  it("bad_input では決めた文言を出す", async () => {
    const timers = createManualTimers();
    const { loadEngine } = fakeEngine(failureResponse("bad_input"));
    renderScreen(loadEngine, timers);
    await act(async () => {});
    await chooseFile(jsonFile("not json"));
    await advance(timers, 0);
    expect(screen.getByRole("alert").textContent).toBe(
      "入力ログとして読めませんでした。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
    );
  });

  it("RS-5 1 MiB を超えるファイルでは too_large の文言を出し、engine.run を呼ばない", async () => {
    const timers = createManualTimers();
    const { run, loadEngine } = fakeEngine();
    renderScreen(loadEngine, timers);
    await act(async () => {});
    await chooseFile(jsonFile("x".repeat(MAX_INPUT_LOG_BYTES + 1)));
    await advance(timers, 0);

    expect(screen.getByRole("alert").textContent).toBe(
      "ファイルが大きすぎます（1 MiB まで）。リザルト画面でダウンロードした入力ログ（JSON）を選んでください。",
    );
    expect(run).not.toHaveBeenCalled();
  });

  it("RS-6 loadEngine が失敗すると load_failed の文言と「もう一度読み込む」を出し、押すとやり直す", async () => {
    const timers = createManualTimers();
    const { run } = fakeEngine();
    const loadEngine = vi
      .fn<() => Promise<ReplayEngine>>()
      .mockRejectedValueOnce(new Error("404"))
      .mockResolvedValue({ run });
    renderScreen(loadEngine, timers);
    await act(async () => {});

    expect(screen.getByRole("alert").textContent).toBe(
      "リプレイの部品を読み込めませんでした。回線を確かめて、もう一度お試しください。",
    );
    expect(screen.getByRole("button", { name: "別の入力ログを選ぶ" })).toBeDefined();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "もう一度読み込む" }));
    });
    expect(loadEngine).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("alert")).toBeNull();

    await chooseFile(jsonFile(INPUT_LOG));
    await advance(timers, 0);
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.getByText(REPLAY_NOTICE)).toBeDefined();
  });

  it("RS-7 queueReplayInput で入れた入力ログがあると、ファイルを選ばずに再生が始まる", async () => {
    const timers = createManualTimers();
    const { run, loadEngine } = fakeEngine();
    queueReplayInput(INPUT_LOG);
    renderScreen(loadEngine, timers);
    await act(async () => {});

    expect(screen.queryByLabelText("入力ログのファイル")).toBeNull();
    await advance(timers, 0);
    expect(run).toHaveBeenCalledWith(INPUT_LOG);
    expect(screen.getByText(REPLAY_NOTICE)).toBeDefined();
    expect(takeQueuedReplayInput()).toBeNull();
  });

  it("RS-8 round.result でリザルトが出て、「別の入力ログを選ぶ」で選ぶ画面に戻る", async () => {
    const timers = createManualTimers();
    const { run, loadEngine } = fakeEngine();
    renderScreen(loadEngine, timers);
    await act(async () => {});
    await chooseFile(jsonFile(INPUT_LOG));
    await advance(timers, 0);
    await advance(timers, 60100);

    expect(screen.getByRole("heading", { level: 1, name: "リザルト" })).toBeDefined();
    expect(gameStore.getState().roundResult?.stars).toBe(2);
    expect(screen.queryByRole("button", { name: "次のステージ" })).toBeNull();

    // 同じ条件でもう一度: engine.run をやり直さず、最初から再生する。
    fireEvent.click(screen.getByRole("button", { name: "同じ条件でもう一度" }));
    await advance(timers, 0);
    expect(screen.getByText(REPLAY_NOTICE)).toBeDefined();
    expect(gameStore.getState().roundResult).toBeNull();
    expect(gameStore.getState().world?.session.id).toBe("replay_mini_1");
    await advance(timers, 60100);
    expect(screen.getByRole("heading", { level: 1, name: "リザルト" })).toBeDefined();
    expect(run).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "別の入力ログを選ぶ" }));
    expect(fileInput()).toBeDefined();
    expect(gameStore.getState().roundResult).toBeNull();
  });

  it("再生の途中でアンマウントすると、タイマーが残らない", async () => {
    const timers = createManualTimers();
    const { loadEngine } = fakeEngine();
    const view = renderScreen(loadEngine, timers);
    await act(async () => {});
    await chooseFile(jsonFile(INPUT_LOG));
    await advance(timers, 0);
    await advance(timers, 3000);
    expect(timers.pendingCount()).toBeGreaterThan(0);

    view.unmount();
    timers.advance(0); // 接続を閉じたときの通知（setTimeout 0）
    expect(timers.pendingCount()).toBe(0);
  });
});
