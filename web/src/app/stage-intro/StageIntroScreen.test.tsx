// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setShowRaftTerms, termsPreference } from "../../ui/terms/preference.ts";
import type { StageCatalogEntry } from "../types.ts";
import { StageIntroScreen } from "./StageIntroScreen.tsx";

const STAGE: StageCatalogEntry = {
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
  learning: ["log_replication", "commit", "quorum", "prevote"],
};

const STAGE2: StageCatalogEntry = {
  id: "stage2",
  title: "島から返事が来ない！",
  region: "五島列島",
  islands: 3,
  ferries: 2,
  durationMs: 210000,
  demo: false,
  intro: {
    text: "世界遺産の潜伏キリシタン関連遺産が残る五島列島。",
    provenance: "derived",
    source: {
      dataset: "mlit-ritou-2026",
      region: "離島振興対策実施地域",
      field: "LOCAL_INDUSTRY_TOURISM",
      quote: null,
    },
  },
  learning: ["heartbeat", "unknown_key", "election_timeout", "check_quorum"],
};

afterEach(() => {
  cleanup();
});

function departButton(): HTMLButtonElement {
  return screen.getByRole<HTMLButtonElement>("button", { name: "出航する" });
}

describe("StageIntroScreen", () => {
  it("SI-1 busy でなければ「出航する」で onDepart を呼ぶ", () => {
    const onDepart = vi.fn();
    render(<StageIntroScreen stage={STAGE} onDepart={onDepart} busy={false} />);

    expect(departButton().disabled).toBe(false);
    fireEvent.click(departButton());
    expect(onDepart).toHaveBeenCalledTimes(1);
  });

  it("SI-1 busy のとき「出航する」が disabled", () => {
    const onDepart = vi.fn();
    render(<StageIntroScreen stage={STAGE} onDepart={onDepart} busy />);

    expect(departButton().disabled).toBe(true);
    fireEvent.click(departButton());
    expect(onDepart).not.toHaveBeenCalled();
  });
});

describe("StageIntroScreen の紹介文と学べること（T-416）", () => {
  it("SI-1 島の紹介に紹介文とラベル、学べることを learning の順に出す", () => {
    render(<StageIntroScreen stage={STAGE2} onDepart={() => {}} busy={false} />);

    const intro = screen.getByRole("region", { name: "島の紹介" });
    expect(within(intro).getByText(STAGE2.intro.text)).toBeDefined();
    expect(intro.querySelector(".provenance-label")?.getAttribute("data-provenance")).toBe(
      "derived",
    );
    expect(
      within(intro).getByText("離島振興計画書（離島振興対策実施地域）の「商工業・観光」から"),
    ).toBeDefined();
    // 以前の仮の文言（「準備中」）が残っていない。
    expect(screen.queryByText(/準備中/)).toBeNull();

    const learning = screen.getByRole("region", { name: "学べること" });
    expect(
      within(learning).getByRole("heading", { name: "このステージで学べること" }),
    ).toBeDefined();
    const items = within(learning)
      .getAllByRole("listitem")
      .map((li) => li.querySelector(".stage-intro-learning-name")?.textContent);
    expect(items).toEqual([
      "定期便（Heartbeat）",
      "定期便が途切れる（Election Timeout）",
      "孤立した代表が退く（CheckQuorum）",
    ]);
    expect(learning.querySelector('span[lang="en"]')?.textContent).toBe("Heartbeat");
  });

  it("SI-2 stage1 は学べることを伏せる", () => {
    render(<StageIntroScreen stage={STAGE} onDepart={() => {}} busy={false} />);

    const learning = screen.getByRole("region", { name: "学べること" });
    expect(within(learning).getByText("遊び終わったあとで種明かしします。")).toBeDefined();
    expect(within(learning).queryByRole("list")).toBeNull();
    expect(screen.queryByText(/記録の複製/)).toBeNull();
    // 図も伏せる（T-433）
    expect(learning.querySelector("[data-learning-art]")).toBeNull();
    expect(learning.querySelector("[data-learning-caption]")).toBeNull();
  });

  it.each<[string, string[]]>([
    ["空", []],
    ["知らないキーだけ", ["unknown_key"]],
  ])("SI-3 learning が %s → 学べることの欄が無い", (_label, learning) => {
    render(<StageIntroScreen stage={{ ...STAGE2, learning }} onDepart={() => {}} busy={false} />);

    expect(screen.queryByRole("region", { name: "学べること" })).toBeNull();
    expect(screen.queryByText("このステージで学べること")).toBeNull();
    expect(screen.getByRole("region", { name: "島の紹介" })).toBeDefined();
  });

  it.each<[string, boolean]>([
    ["busy でない", false],
    ["busy", true],
  ])("SI-4 %s ときの「出航する」は変わらない", (_label, busy) => {
    const onDepart = vi.fn();
    render(<StageIntroScreen stage={STAGE2} onDepart={onDepart} busy={busy} />);

    expect(departButton().disabled).toBe(busy);
    fireEvent.click(departButton());
    expect(onDepart).toHaveBeenCalledTimes(busy ? 0 : 1);
  });
});

describe("StageIntroScreen の学べることの図（T-433）", () => {
  beforeEach(() => {
    localStorage.clear();
    act(() => {
      termsPreference.reload();
    });
  });

  afterEach(() => {
    localStorage.clear();
    act(() => {
      termsPreference.reload();
    });
  });

  it("SI-5 学べることの 1 つずつに、そのキーの図と説明が付く", () => {
    render(<StageIntroScreen stage={STAGE2} onDepart={() => {}} busy={false} />);

    const learning = screen.getByRole("region", { name: "学べること" });
    const items = within(learning).getAllByRole("listitem");
    expect(
      items.map((li) =>
        li.querySelector("svg[data-learning-art]")?.getAttribute("data-learning-art"),
      ),
    ).toEqual(["heartbeat", "election_timeout", "check_quorum"]);
    for (const li of items) {
      const art = li.querySelector("svg[data-learning-art]");
      expect(art?.getAttribute("aria-hidden")).toBe("true");
      expect(art?.classList.contains("stage-intro-learning-art")).toBe(true);
      const caption = li.querySelector(".stage-intro-learning-caption");
      expect(caption?.getAttribute("data-learning-caption")).toBe(
        art?.getAttribute("data-learning-art"),
      );
      expect(caption?.textContent).not.toBe("");
    }
    expect(items[0]?.querySelector(".stage-intro-learning-caption")?.textContent).toBe(
      "代表は定期便を出し続け、元気だと知らせる",
    );
    // 知らないキー（unknown_key）は名前も図も出さない
    expect(learning.querySelectorAll("[data-learning-art]").length).toBe(3);
  });

  it("SI-6 説明の用語は「Raft の用語を併記する」に従う（名前は設定に関係なく両方）", () => {
    render(<StageIntroScreen stage={STAGE2} onDepart={() => {}} busy={false} />);
    const learning = screen.getByRole("region", { name: "学べること" });
    const caption = (): string | null | undefined =>
      learning.querySelector('[data-learning-caption="heartbeat"]')?.textContent;
    const name = (): string | null | undefined =>
      learning.querySelector(".stage-intro-learning-name")?.textContent;

    expect(caption()).toBe("代表は定期便を出し続け、元気だと知らせる");
    expect(name()).toBe("定期便（Heartbeat）");

    act(() => {
      setShowRaftTerms(true);
    });
    expect(caption()).toBe("代表（Leader）は定期便（Heartbeat）を出し続け、元気だと知らせる");
    expect(name()).toBe("定期便（Heartbeat）");
  });
});

describe("StageIntroScreen の見た目（T-424）", () => {
  it("地域名の切手・便箋の紹介・学べることのカードがあり、「出航する」は主ボタン。名前と順番は変わらない", () => {
    render(<StageIntroScreen stage={STAGE2} onDepart={() => {}} busy={false} />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("島から返事が来ない！");
    expect(screen.getByText("五島列島").className).toBe("stage-intro-region");
    expect(screen.getByRole("region", { name: "島の紹介" }).className).toBe("stage-intro-letter");
    expect(screen.getByRole("region", { name: "学べること" }).className).toBe(
      "stage-intro-learning",
    );
    const depart = departButton();
    expect(depart.classList.contains("btn")).toBe(true);
    expect(depart.classList.contains("btn-primary")).toBe(true);
    const back = screen.getByRole("link", { name: "ステージ選択へ戻る" });
    expect(depart.compareDocumentPosition(back) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0);
  });
});
