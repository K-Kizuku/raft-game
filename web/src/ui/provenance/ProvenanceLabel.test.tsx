// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { ProvenanceLabel, type Provenance } from "./ProvenanceLabel.tsx";

afterEach(() => {
  cleanup();
});

describe("ProvenanceLabel", () => {
  // PV-1（データ仕様 §1 の表）
  it.each<[Provenance, string]>([
    ["fact", "出典：国土交通省『離島に関するデータ』"],
    ["derived", "国土交通省『離島に関するデータ』をもとに作成"],
    ["creative", "ゲーム上の設定"],
  ])("%s →「%s」", (provenance, text) => {
    const { container } = render(<ProvenanceLabel provenance={provenance} />);
    const label = container.querySelector("small");
    expect(label?.textContent).toBe(text);
    expect(label?.getAttribute("data-provenance")).toBe(provenance);
  });
});
