// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { RoundResult } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { loadMiniRound } from "../../state/fixtures/loadRecord.ts";
import { setShowRaftTerms } from "../../ui/terms/preference.ts";
import type { StageSummary } from "../types.ts";
import { ResultScreen } from "./ResultScreen.tsx";
import { FULL_RESULT, SCORE_ONLY_RESULT, resultWith } from "./testing/results.ts";

const STAGE: StageSummary = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
};

const NEXT_STAGE: StageSummary = {
  id: "stage2",
  title: "島から返事が来ない！",
  region: "伊豆諸島",
  islands: 5,
  ferries: 2,
  durationMs: 210000,
};

function renderResult(nextStage: StageSummary | null = NEXT_STAGE) {
  const props = {
    onRetry: vi.fn(),
    onNextStage: vi.fn(),
    onStageSelect: vi.fn(),
  };
  const utils = render(<ResultScreen stage={STAGE} nextStage={nextStage} {...props} />);
  return { ...utils, ...props };
}

function loadWorld(target: number) {
  const { snapshot } = loadMiniRound();
  snapshot.score = { ...snapshot.score, target };
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function setResult(result: RoundResult) {
  act(() => {
    gameStore.getState().setRoundResult(result);
  });
}

function section(name: string): HTMLElement {
  return screen.getByRole("region", { name });
}

function conditions(): HTMLElement[] {
  return within(screen.getByRole("list", { name: "★の条件" })).getAllByRole("listitem");
}

function definition(container: HTMLElement, term: string): HTMLElement {
  const dt = within(container)
    .getAllByText(term)
    .find((el) => el.tagName === "DT");
  const dd = dt?.nextElementSibling;
  if (!(dd instanceof HTMLElement) || dd.tagName !== "DD") {
    throw new Error(`「${term}」の dd がない`);
  }
  return dd;
}

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

describe("ResultScreen", () => {
  it("RS-1 roundResult が null なら「結果を受け取っていません。」とボタンだけ", () => {
    renderResult();

    expect(screen.getByRole("heading", { level: 1, name: "リザルト" })).toBeDefined();
    expect(screen.getByText("はじめての郵便船")).toBeDefined();
    expect(screen.getByText("結果を受け取っていません。")).toBeDefined();
    expect(screen.getByRole("button", { name: "同じ条件でもう一度" })).toBeDefined();
    expect(screen.getByRole("button", { name: "次のステージ" })).toBeDefined();
    expect(screen.getByRole("button", { name: "ステージ選択" })).toBeDefined();
    expect(screen.queryByRole("region", { name: "花火大会" })).toBeNull();
  });

  it("RS-2 FULL_RESULT と mini の世界", () => {
    loadWorld(4);
    setResult(FULL_RESULT);
    const { container } = renderResult();

    const stars = screen.getByRole("img", { name: "★ 2 / 3" });
    expect(stars.textContent).toBe("★★☆");
    expect(screen.getByText("★2 花火の予約がすべて確定！ 花火大会が開かれます。")).toBeDefined();
    const rows = conditions();
    expect(rows).toHaveLength(3);
    expect(rows[0]?.querySelector(".result-condition")?.textContent).toBe(
      "確定した依頼が目標数（4 件）以上",
    );
    expect(rows[0]?.getAttribute("data-achieved")).toBe("true");
    expect(rows[1]?.getAttribute("data-achieved")).toBe("true");
    expect(rows[2]?.getAttribute("data-achieved")).not.toBe("true");
    expect(rows[0]?.textContent).toContain("✓");
    expect(rows[2]?.textContent).toContain("・");

    const score = section("スコア");
    expect(within(score).getByText("1,520 点")).toBeDefined();
    const table = within(score).getByRole("table", { name: "スコアの内訳" });
    for (const value of ["+1,300", "−100", "−180", "+500"]) {
      expect(within(table).getByText(value)).toBeDefined();
    }

    const requests = section("依頼");
    expect(definition(requests, "確定").textContent).toBe("7 件");
    expect(definition(requests, "予約失敗").textContent).toBe("2 件");
    expect(definition(requests, "期限切れ").textContent).toBe("2 件");

    const technical = section("技術的な結果");
    expect(within(technical).getByText("93 通")).toBeDefined();
    const letters = within(technical).getByRole("list", { name: "配った手紙の種類" });
    const letterRows = within(letters).getAllByRole("listitem");
    expect(letterRows[0]?.textContent).toBe("お元気ですか？（MsgHeartbeat）40 通");
    expect(letterRows[0]?.querySelector('span[lang="en"]')?.textContent).toBe("MsgHeartbeat");
    expect(letterRows[4]?.textContent).toBe("住民の依頼書9 通");
    expect(definition(technical, "Leader の交代").textContent).toBe("1 回");
    expect(definition(technical, "平均 Commit Latency").textContent).toBe("18.4 秒");
    expect(definition(technical, "最長の音信不通").textContent).toBe("41.0 秒");

    const logs = within(technical).getByRole("list", { name: "各島の最終ログ" });
    expect(within(logs).getByText("一の島")).toBeDefined();
    expect(within(logs).getByText("Last Index 24・Commit 24")).toBeDefined();
    expect(within(logs).getByText("Term 1: 1〜9、Term 3: 10〜24")).toBeDefined();
    const band = logs.querySelector(".log-band");
    expect(band?.getAttribute("aria-hidden")).toBe("true");
    const segments = Array.from(band?.querySelectorAll<HTMLElement>("[data-term]") ?? []);
    expect(segments.map((s) => s.dataset["term"])).toEqual(["1", "3"]);
    expect(segments.map((s) => s.dataset["committed"])).toEqual(["true", "true"]);

    const story = section("今日起きた分散システムの出来事");
    expect(within(story).getByText("1:12").tagName).toBe("TIME");
    expect(within(story).getByText(FULL_RESULT.story[0]?.text ?? "")).toBeDefined();
    const concepts = within(story).getByRole("list", { name: "関係する Raft の仕組み" });
    expect(within(concepts).getByText("CheckQuorum").getAttribute("lang")).toBe("en");

    expect(screen.getByRole("region", { name: "花火大会" })).toBeDefined();
    expect(container.querySelector("img")).toBeNull();
  });

  it.each([
    [0, "目標まであと一歩。もう一度挑戦してみよう！", false, "★ 0 / 3"],
    [1, "★1 クリア！ 島じまの依頼が共和国で確定しました。", false, "★ 1 / 3"],
    [2, "★2 花火の予約がすべて確定！ 花火大会が開かれます。", true, "★ 2 / 3"],
    [3, "★3 パーフェクト！ 称号「共和国の名船長」を手に入れました。", true, "★ 3 / 3"],
    [7, "★3 パーフェクト！ 称号「共和国の名船長」を手に入れました。", true, "★ 3 / 3"],
  ])("RS-3 stars %d", (stars, message, fireworks, label) => {
    setResult(resultWith({ stars }));
    renderResult();

    expect(screen.getByText(message)).toBeDefined();
    expect(screen.getByRole("img", { name: label })).toBeDefined();
    expect(screen.queryByRole("region", { name: "花火大会" }) !== null).toBe(fireworks);
  });

  it("RS-4 T-208 の値が null（SCORE_ONLY_RESULT）", () => {
    setResult(SCORE_ONLY_RESULT);
    renderResult();

    const score = section("スコア");
    expect(within(score).getByText("396 点")).toBeDefined();
    const table = within(score).getByRole("table", { name: "スコアの内訳" });
    for (const value of ["+100", "0", "−4", "+300"]) {
      expect(within(table).getByText(value)).toBeDefined();
    }
    expect(screen.getByText("技術的な結果はまだ集計されていません。")).toBeDefined();
    expect(screen.getByText("今日は書き残す出来事がありませんでした。")).toBeDefined();
    expect(screen.queryByRole("region", { name: "花火大会" })).toBeNull();
  });

  it("RS-5 technical はあるが空", () => {
    setResult(
      resultWith({
        stats: {
          ...FULL_RESULT.stats,
          lettersDelivered: {},
          finalLogs: [],
          avgCommitLatencyMs: 0,
          longestSilenceMs: 0,
          leaderChanges: 0,
        },
      }),
    );
    renderResult();

    const technical = section("技術的な結果");
    expect(definition(technical, "配った手紙").textContent).toBe("—");
    expect(definition(technical, "平均 Commit Latency").textContent).toBe("—");
    expect(definition(technical, "最長の音信不通").textContent).toBe("—");
    expect(definition(technical, "Leader の交代").textContent).toBe("0 回");
    expect(definition(technical, "各島の最終ログ").textContent).toBe("記録がありません。");
  });

  it("RS-5 全島の lastIndex が 0 なら帯を描かない", () => {
    setResult(
      resultWith({
        stats: { ...FULL_RESULT.stats, finalLogs: [{ id: 2, lastIndex: 0, commit: 0, terms: [] }] },
      }),
    );
    const { container } = renderResult();

    expect(screen.getByText("ログは空です。")).toBeDefined();
    expect(container.querySelector(".log-band")).toBeNull();
  });

  it("RS-6 world が null", () => {
    setResult(FULL_RESULT);
    renderResult();

    const logs = screen.getByRole("list", { name: "各島の最終ログ" });
    expect(within(logs).getByText("島 1")).toBeDefined();
    expect(conditions()[0]?.querySelector(".result-condition")?.textContent).toBe(
      "確定した依頼が目標数以上",
    );
  });

  it("RS-6 未確定の記録とステージに無い島", () => {
    loadWorld(0);
    setResult(
      resultWith({
        stats: {
          ...FULL_RESULT.stats,
          finalLogs: [{ id: 9, lastIndex: 24, commit: 20, terms: [[3, 10, 24]] }],
        },
      }),
    );
    renderResult();

    const logs = screen.getByRole("list", { name: "各島の最終ログ" });
    expect(within(logs).getByText("島 9")).toBeDefined();
    expect(within(logs).getByText("Last Index 24・Commit 20（21〜24 は未確定）")).toBeDefined();
    expect(conditions()[0]?.querySelector(".result-condition")?.textContent).toBe(
      "確定した依頼が目標数以上",
    );
    const committed = Array.from(logs.querySelectorAll<HTMLElement>("[data-committed]")).map(
      (s) => s.dataset["committed"],
    );
    expect(committed).toEqual(["true", "false"]);
  });

  it("RS-7 ボタン", () => {
    setResult(FULL_RESULT);
    const { onRetry, onNextStage, onStageSelect } = renderResult();

    fireEvent.click(screen.getByRole("button", { name: "同じ条件でもう一度" }));
    fireEvent.click(screen.getByRole("button", { name: "次のステージ" }));
    fireEvent.click(screen.getByRole("button", { name: "ステージ選択" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onNextStage).toHaveBeenCalledTimes(1);
    expect(onStageSelect).toHaveBeenCalledTimes(1);
  });

  it("RS-7 nextStage が null なら「次のステージ」が無い", () => {
    setResult(FULL_RESULT);
    renderResult(null);

    expect(screen.queryByRole("button", { name: "次のステージ" })).toBeNull();
    expect(screen.getByRole("button", { name: "ステージ選択" })).toBeDefined();
  });

  it("RS-9 ★は 1 字ずつ aria-hidden の span に分かれ、textContent と aria-label は変わらない", () => {
    setResult(FULL_RESULT);
    renderResult();

    const stars = screen.getByRole("img", { name: "★ 2 / 3" });
    expect(stars.textContent).toBe("★★☆");
    const spans = Array.from(stars.querySelectorAll("span"));
    expect(spans).toHaveLength(3);
    expect(spans.every((span) => span.getAttribute("aria-hidden") === "true")).toBe(true);
    expect(spans.map((span) => span.textContent)).toEqual(["★", "★", "☆"]);
  });

  it.each([
    ["出来事あり", FULL_RESULT],
    ["出来事が 0 件", resultWith({ story: [] })],
  ])("RS-10 今日の出来事は便箋の節（%s）", (_, result) => {
    setResult(result);
    const { container } = renderResult();

    const story = section("今日起きた分散システムの出来事");
    expect(story.classList.contains("result-letter")).toBe(true);
    expect(story.classList.contains("result-section")).toBe(true);
    for (const tag of ["img", "canvas"]) {
      expect(container.querySelector(tag)).toBeNull();
    }
  });

  it("RS-11 「同じ条件でもう一度」が主ボタン、ほかは副ボタン（順番は変わらない）", () => {
    setResult(FULL_RESULT);
    const { container } = renderResult();

    const buttons = Array.from(container.querySelectorAll(".result-actions button"));
    expect(buttons.map((b) => b.textContent)).toEqual([
      "同じ条件でもう一度",
      "次のステージ",
      "ステージ選択",
    ]);
    expect(buttons.map((b) => b.className)).toEqual(["btn btn-primary", "btn", "btn"]);
  });

  it("RS-T1 結果があるときだけ「E エンジニアモード」のボタンが出来事の節の後にある", () => {
    loadWorld(3);
    setResult(FULL_RESULT);
    const { unmount } = renderResult();
    const story = section("今日起きた分散システムの出来事");
    const toggle = screen.getByRole("button", { name: "E エンジニアモード" });
    expect(story.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(story.contains(toggle)).toBe(false);
    fireEvent.click(toggle);
    expect(section("タイムライン")).toBeDefined();
    unmount();

    act(() => {
      gameStore.getState().reset();
    });
    loadWorld(3);
    renderResult();
    expect(screen.queryByRole("button", { name: "E エンジニアモード" })).toBeNull();
  });

  it("RS-S1 結果と世界があるとき、「同じ条件で遊ぶ」の節がボタンの列の直前にある", () => {
    loadWorld(3);
    setResult(FULL_RESULT);
    const { container, unmount } = renderResult();

    const seed = section("同じ条件で遊ぶ");
    const actions = container.querySelector(".result-actions");
    expect(seed.nextElementSibling).toBe(actions);
    expect(within(seed).getByText(/このラウンドのシード:/).textContent).toBe(
      "このラウンドのシード: 1",
    );
    expect(seed.querySelector("code")?.textContent).toBe(
      `${window.location.origin}/stages/stage1?seed=1`,
    );
    unmount();

    // 結果が無いときは無い（世界があっても）。
    act(() => {
      gameStore.getState().reset();
    });
    loadWorld(3);
    renderResult();
    expect(screen.queryByRole("region", { name: "同じ条件で遊ぶ" })).toBeNull();
  });

  describe("RS-8 用語", () => {
    afterEach(() => {
      act(() => {
        setShowRaftTerms(false);
      });
    });

    it.each([true, false])("併記の設定 %s でも同じ文字", (show) => {
      act(() => {
        setShowRaftTerms(show);
      });
      setResult(FULL_RESULT);
      renderResult();

      const technical = section("技術的な結果");
      expect(definition(technical, "Leader の交代").textContent).toBe("1 回");
      expect(definition(technical, "平均 Commit Latency").textContent).toBe("18.4 秒");
      const letters = within(technical).getByRole("list", { name: "配った手紙の種類" });
      expect(within(letters).getAllByRole("listitem")[0]?.textContent).toBe(
        "お元気ですか？（MsgHeartbeat）40 通",
      );
    });
  });
});
