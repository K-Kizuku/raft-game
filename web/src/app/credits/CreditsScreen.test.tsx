// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { CreditsScreen } from "./CreditsScreen.tsx";

afterEach(() => {
  cleanup();
});

describe("CreditsScreen", () => {
  // CR-1
  it("データ仕様 §6 の出典と etcd-io/raft のライセンスを出す", () => {
    const { container } = render(<CreditsScreen />);
    const text = container.textContent ?? "";

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("クレジット");
    for (const phrase of [
      "国土交通省 Project LINKS",
      "政府標準利用規約（第2.0版）",
      "実際の航路・運航とは異なります",
      "etcd-io/raft",
      "Apache License 2.0",
      "server/third_party/etcd-raft/PATCHES.md",
      "画像・効果音の一部は画像生成 AI 等で作成しています。",
    ]) {
      expect(text).toContain(phrase);
    }

    const links = screen.getAllByRole("link");
    const mlit = links.find((a) => a.getAttribute("href") === "https://www.mlit.go.jp/links/");
    expect(mlit).toBeDefined();
    expect(mlit?.getAttribute("rel")).toContain("noopener");
    expect(mlit?.getAttribute("target")).toBe("_blank");

    for (const file of ["LICENSE", "NOTICE"]) {
      const link = screen.getByRole("link", { name: file });
      expect(link.getAttribute("href")).toBe(
        `https://github.com/K-Kizuku/raft-game/blob/main/${file}`,
      );
      expect(link.getAttribute("rel")).toContain("noopener");
    }
    expect(screen.getByRole("link", { name: "タイトルへ" }).getAttribute("href")).toBe("/");
  });
});
