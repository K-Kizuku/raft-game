// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { StageText } from "../types.ts";
import { StageIntroText } from "./StageIntroText.tsx";

const DERIVED: StageText = {
  text: "温暖で雨の少ない瀬戸内の海。",
  provenance: "derived",
  source: {
    dataset: "mlit-ritou-2026",
    region: "小豆島地域",
    field: "NATURAL_CLIMATE_FEATURES",
    quote: "温暖、小雨の典型的な瀬戸内式気候",
  },
};
const SOURCE_TEXT = "離島振興計画書（小豆島地域）の「自然・気象特性」から";

afterEach(() => {
  cleanup();
});

function renderText(intro: StageText) {
  const { container } = render(<StageIntroText intro={intro} />);
  return {
    body: container.querySelector(".stage-intro-text__body"),
    label: container.querySelector(".provenance-label"),
    source: container.querySelector(".stage-intro-text__source"),
    quote: container.querySelector(".stage-intro-text__quote"),
    container,
  };
}

describe("StageIntroText", () => {
  it("IT-1 derived で引用あり → 本文・ラベル・出典・引用", () => {
    const { body, label, source, quote } = renderText(DERIVED);

    expect(body?.textContent).toBe("温暖で雨の少ない瀬戸内の海。");
    expect(label?.getAttribute("data-provenance")).toBe("derived");
    expect(source?.textContent).toBe(SOURCE_TEXT);
    expect(quote?.querySelector("q")?.textContent).toBe("温暖、小雨の典型的な瀬戸内式気候");
  });

  it("IT-2 creative で source が null → 本文とラベルだけ", () => {
    const { body, label, source, quote } = renderText({
      text: "島から返事が来ない海。",
      provenance: "creative",
      source: null,
    });

    expect(body?.textContent).toBe("島から返事が来ない海。");
    expect(label?.getAttribute("data-provenance")).toBe("creative");
    expect(label?.textContent).toBe("ゲーム上の設定");
    expect(source).toBeNull();
    expect(quote).toBeNull();
  });

  it.each<[string, string | null]>([
    ["null", null],
    ["空", ""],
  ])("IT-3 quote が %s → 出典の文はあり、引用の行が無い", (_label, value) => {
    const { source, quote, container } = renderText({
      ...DERIVED,
      source: { ...DERIVED.source!, quote: value },
    });

    expect(source?.textContent).toBe(SOURCE_TEXT);
    expect(quote).toBeNull();
    expect(container.querySelector("q")).toBeNull();
  });

  it("IT-4 HTML のように見える本文は文字として出す", () => {
    const { body, container } = renderText({ ...DERIVED, text: "<b>x</b>" });

    expect(body?.textContent).toBe("<b>x</b>");
    expect(container.querySelector("b")).toBeNull();
  });
});
