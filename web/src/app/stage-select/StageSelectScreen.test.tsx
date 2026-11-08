// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { stageProgress } from "../progress/progress.ts";
import type { StageCatalogEntry, StageCatalogState } from "../types.ts";
import { StageSelectScreen } from "./StageSelectScreen.tsx";

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
    id: "demo-izu",
    title: "伊豆の島じま",
    region: "伊豆諸島",
    islands: 5,
    ferries: 2,
    durationMs: 210000,
    demo: true,
    intro: { text: "<b>伊豆</b>の島じまを結ぶ郵便船。", provenance: "creative", source: null },
    learning: ["heartbeat", "check_quorum"],
    unlock: null,
  },
];

beforeEach(() => {
  localStorage.clear();
  act(() => {
    stageProgress.reload();
  });
});

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

function buttons(): HTMLButtonElement[] {
  return STAGES.map((stage) =>
    screen.getByRole<HTMLButtonElement>("button", { name: `${stage.title}を選ぶ` }),
  );
}

describe("StageSelectScreen", () => {
  it("SS-1 「<タイトル>を選ぶ」で onSelect にそのステージを渡す", () => {
    const onSelect = vi.fn();
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages: STAGES }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy={false}
      />,
    );

    for (const button of buttons()) {
      expect(button.disabled).toBe(false);
    }
    fireEvent.click(screen.getByRole("button", { name: "伊豆の島じまを選ぶ" }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(STAGES[1]);
  });

  it("SS-1 busy のとき、すべての「<タイトル>を選ぶ」が disabled", () => {
    const onSelect = vi.fn();
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages: STAGES }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy
      />,
    );

    for (const button of buttons()) {
      expect(button.disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(onSelect).not.toHaveBeenCalled();
  });
});

describe("StageSelectScreen の紹介文（T-416）", () => {
  function renderReady(onSelect = vi.fn(), busy = false) {
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages: STAGES }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy={busy}
      />,
    );
    return screen.getAllByRole("listitem");
  }

  it("SS-1 各ステージに紹介文とラベル、derived だけに出典", () => {
    const [first, second] = renderReady();

    expect(within(first!).getByText("温暖で雨の少ない瀬戸内の海。")).toBeDefined();
    expect(first!.querySelector(".provenance-label")?.getAttribute("data-provenance")).toBe(
      "derived",
    );
    expect(
      within(first!).getByText("離島振興計画書（小豆島地域）の「自然・気象特性」から"),
    ).toBeDefined();
    expect(first!.querySelector("q")?.textContent).toBe("温暖、小雨の典型的な瀬戸内式気候");

    expect(within(second!).getByText("<b>伊豆</b>の島じまを結ぶ郵便船。")).toBeDefined();
    expect(second!.querySelector("b")).toBeNull();
    expect(second!.querySelector(".provenance-label")?.getAttribute("data-provenance")).toBe(
      "creative",
    );
    expect(second!.querySelector(".stage-intro-text__source")).toBeNull();
    expect(second!.querySelector("q")).toBeNull();
  });

  it("SS-2 学べることは一覧に出さない", () => {
    renderReady();

    for (const text of ["定期便", "Heartbeat", "記録の複製", "このステージで学べること"]) {
      expect(screen.queryByText(text, { exact: false })).toBeNull();
    }
  });

  it.each<[string, boolean]>([
    ["busy でない", false],
    ["busy", true],
  ])("SS-3 %s ときの onSelect は変わらない", (_label, busy) => {
    const onSelect = vi.fn();
    renderReady(onSelect, busy);

    const button = screen.getByRole<HTMLButtonElement>("button", {
      name: "はじめての郵便船を選ぶ",
    });
    expect(button.disabled).toBe(busy);
    fireEvent.click(button);
    if (busy) {
      expect(onSelect).not.toHaveBeenCalled();
    } else {
      expect(onSelect).toHaveBeenCalledWith(STAGES[0]);
    }
  });
});

describe("StageSelectScreen の見た目（T-424）", () => {
  function renderCatalog(catalog: StageCatalogState) {
    return render(
      <StageSelectScreen catalog={catalog} onRetry={() => {}} onSelect={() => {}} busy={false} />,
    );
  }

  it("葉書のカードに地域名の切手と札があり、選ぶボタンは主ボタン。見出しとボタンの名前は変わらない", () => {
    renderCatalog({ status: "ready", stages: STAGES });

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("ステージ選択");
    const items = screen.getAllByRole("listitem");
    expect(items.map((item) => item.className)).toEqual(["stage-select-item", "stage-select-item"]);
    const first = items[0]!;
    expect(first.querySelector(".stage-select-region")?.textContent).toBe("瀬戸内");
    expect(
      Array.from(first.querySelectorAll(".stage-select-fact"), (fact) => fact.textContent),
    ).toEqual(["島 3", "船 1 隻", "制限時間 3:00"]);
    expect(first.querySelector(".stage-select-facts")?.textContent).toBe(
      "島 3・船 1 隻・制限時間 3:00",
    );
    for (const button of buttons()) {
      expect(button.classList.contains("btn")).toBe(true);
      expect(button.classList.contains("btn-primary")).toBe(true);
    }
    expect(screen.getByRole("link", { name: "タイトルへ" })).toBeDefined();
  });

  it("読み込み中・失敗・空の一覧の文は紙のカード（失敗は ⚠ の印の付く枠）", () => {
    const { unmount } = renderCatalog({ status: "loading" });
    expect(screen.getByRole("status").className).toBe("stage-select-message");
    unmount();

    const error = renderCatalog({ status: "error" });
    expect(screen.getByRole("alert").className).toBe("stage-select-message stage-select-error");
    expect(screen.getByRole("button", { name: "もう一度読み込む" }).classList.contains("btn")).toBe(
      true,
    );
    error.unmount();

    renderCatalog({ status: "ready", stages: [] });
    expect(screen.getByText("遊べるステージがありません。").className).toBe("stage-select-message");
  });
});

describe("StageSelectScreen のクリアの記録（T-703・T-703b）", () => {
  // unlock があっても鍵には使わない（T-703b）。
  const stage2: StageCatalogEntry = {
    id: "stage2",
    title: "島から返事が来ない！",
    region: "伊豆諸島",
    islands: 5,
    ferries: 2,
    durationMs: 210000,
    demo: false,
    intro: { text: "島から返事が来ない海。", provenance: "creative", source: null },
    learning: ["heartbeat"],
    unlock: "stage1",
  };
  const stages = [STAGES[0]!, stage2];

  function renderStages(onSelect = vi.fn()) {
    render(
      <StageSelectScreen
        catalog={{ status: "ready", stages }}
        onRetry={() => {}}
        onSelect={onSelect}
        busy={false}
      />,
    );
    return onSelect;
  }

  function button(title: string): HTMLButtonElement {
    return screen.getByRole<HTMLButtonElement>("button", { name: `${title}を選ぶ` });
  }

  it("SS-U1 記録がなくても、unlock のあるステージを含めてすべて押せ、鍵の表示がない", () => {
    const onSelect = renderStages();

    const [first, second] = screen.getAllByRole("listitem");
    expect(within(second!).getByRole("heading", { level: 2 }).textContent).toBe(
      "島から返事が来ない！",
    );
    for (const item of [first!, second!]) {
      expect(item.getAttribute("data-locked")).toBeNull();
      expect(item.textContent).not.toContain("🔒");
      expect(item.textContent).not.toContain("クリアすると遊べます");
    }

    const next = button("島から返事が来ない！");
    expect(next.disabled).toBe(false);
    expect(next.getAttribute("aria-describedby")).toBeNull();
    fireEvent.click(next);
    expect(onSelect).toHaveBeenCalledWith(stage2);

    expect(button("はじめての郵便船").disabled).toBe(false);
    expect(screen.queryByText("クリア済み", { exact: false })).toBeNull();
  });

  it("SS-U2 記録があれば、そのカードにだけクリア済みの印（最高の★）を出す", () => {
    act(() => {
      stageProgress.recordResult("stage2", 2);
    });
    renderStages();

    const cleared = screen.getByText("クリア済み ★2");
    expect(cleared.className).toBe("stage-select-cleared");
    expect(cleared.getAttribute("aria-label")).toBe("クリア済み、★ 2 / 3");
    const [first, second] = screen.getAllByRole("listitem");
    expect(within(second!).getByText("クリア済み ★2")).toBeDefined();
    expect(first!.querySelector(".stage-select-cleared")).toBeNull();
    expect(button("はじめての郵便船").disabled).toBe(false);
  });

  it("SS-U2 表示中にクリアが記録されると印が出る", () => {
    renderStages();
    expect(screen.queryByText("クリア済み", { exact: false })).toBeNull();

    act(() => {
      stageProgress.recordResult("stage1", 1);
    });
    expect(screen.getByText("クリア済み ★1")).toBeDefined();
  });

  it("SS-U3 保存した記録が壊れていても、すべて押せる（印を出さないだけ）", () => {
    localStorage.setItem("raft-game.progress", "{broken");
    act(() => {
      stageProgress.reload();
    });
    renderStages();

    expect(button("はじめての郵便船").disabled).toBe(false);
    expect(button("島から返事が来ない！").disabled).toBe(false);
    expect(screen.queryByText("クリア済み", { exact: false })).toBeNull();
  });
});
