// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { isLearningArtKey, LEARNING_ART_KEYS, LearningArt } from "./LearningArt.tsx";

// vitest は CSS の import を空にするので、CSS は node:fs で読む（art.test.tsx と同じ）。
type NodeFs = { readFileSync(path: URL, encoding: "utf8"): string };
const nodeFs = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ nodeFs)) as NodeFs;

afterEach(() => {
  cleanup();
});

function renderArt(key: string): SVGSVGElement | null {
  const { container } = render(<LearningArt learningKey={key} />);
  return container.querySelector("svg");
}

describe("LearningArt（T-433）", () => {
  it.each(LEARNING_ART_KEYS)("LA-1 %s の図は aria-hidden で、文字を持たない", (key) => {
    const svg = renderArt(key);
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("focusable")).toBe("false");
    expect(svg?.getAttribute("data-learning-art")).toBe(key);
    expect(svg?.getAttribute("viewBox")).toBe("0 0 120 72");
    expect(svg?.classList.contains("art")).toBe(true);
    expect(svg?.classList.contains("learning-art")).toBe(true);
    expect(svg?.textContent).toBe("");
    // 島が描かれている
    expect(svg?.querySelectorAll(".learning-isle, [data-ledger]").length).toBeGreaterThan(0);
  });

  it("LA-2 キーごとに違う図を描く", () => {
    const pictures = LEARNING_ART_KEYS.map((key) => renderArt(key)?.innerHTML ?? "");
    expect(new Set(pictures).size).toBe(LEARNING_ART_KEYS.length);
  });

  it("LA-3 知らないキー・Object.prototype のキーでは何も描かない", () => {
    for (const key of ["unknown_key", "toString", "constructor", ""]) {
      expect(isLearningArtKey(key)).toBe(false);
      expect(renderArt(key)).toBeNull();
    }
  });

  it("LA-4 className を足せる", () => {
    const { container } = render(<LearningArt learningKey="quorum" className="x" />);
    const svg = container.querySelector("svg");
    expect(svg?.classList.contains("learning-art")).toBe(true);
    expect(svg?.classList.contains("x")).toBe(true);
  });

  it("LA-5 記録の複製: 代表（★）が手紙を配り、どの島にも台帳がある", () => {
    const svg = renderArt("log_replication");
    expect(svg?.querySelectorAll('[data-isle="leader"]').length).toBe(1);
    expect(svg?.querySelectorAll("[data-letter]").length).toBe(2);
    expect(svg?.querySelectorAll("[data-ledger]").length).toBe(3);
  });

  it("LA-6 確定・過半数: 5 島のうち 3 島（代表＋返事 2 つ、または ✓ 3 つ）がそろう", () => {
    const commit = renderArt("commit");
    expect(commit?.querySelectorAll(".learning-isle").length).toBe(5);
    expect(commit?.querySelectorAll('[data-isle="leader"]').length).toBe(1);
    expect(commit?.querySelectorAll("[data-check]").length).toBe(2);
    expect(commit?.querySelector('[data-ledger="entry committed"]')).not.toBeNull();
    cleanup();

    const quorum = renderArt("quorum");
    expect(quorum?.querySelectorAll(".learning-isle").length).toBe(5);
    expect(quorum?.querySelectorAll('[data-isle="faded"]').length).toBe(2);
    expect(quorum?.querySelectorAll("[data-check]").length).toBe(3);
  });

  it("LA-7 嵐の図（孤立した代表が退く・嵐）は雲と雨を描き、退く代表は淡い", () => {
    for (const key of ["check_quorum", "network_partition"]) {
      const svg = renderArt(key);
      expect(svg?.querySelector("[data-storm] .learning-cloud")).not.toBeNull();
      expect(svg?.querySelectorAll("[data-storm] .learning-rain").length).toBeGreaterThan(0);
      const former = svg?.querySelector('[data-isle="former"]');
      expect(former?.classList.contains("learning-faded")).toBe(true);
      cleanup();
    }
    // 嵐で分かれたあと、多い側に新しい代表がいる
    expect(renderArt("network_partition")?.querySelectorAll('[data-isle="leader"]').length).toBe(1);
  });

  it("LA-8 定期便・途切れ・代表選び・台帳の複製・立候補の相談の場面", () => {
    expect(renderArt("heartbeat")?.querySelectorAll("[data-beat]").length).toBe(3);
    cleanup();
    const timeout = renderArt("election_timeout");
    expect(timeout?.querySelector("[data-clock]")).not.toBeNull();
    expect(timeout?.querySelector("[data-break]")).not.toBeNull();
    cleanup();
    const election = renderArt("leader_election");
    expect(election?.querySelectorAll('[data-isle="leader"]').length).toBe(1);
    expect(election?.querySelectorAll("[data-letter]").length).toBe(2);
    cleanup();
    const ledgers = renderArt("state_machine_replication")?.querySelectorAll("[data-ledger]");
    expect(ledgers?.length).toBe(3);
    expect(new Set([...(ledgers ?? [])].map((l) => l.getAttribute("data-ledger"))).size).toBe(1);
    cleanup();
    const prevote = renderArt("prevote");
    expect(prevote?.querySelector("[data-question]")).not.toBeNull();
    expect(prevote?.querySelectorAll("[data-check]").length).toBe(2);
  });
});

// Vite は文字列の直書きの new URL("./…", import.meta.url) を資産の URL に書き換えるので、
// ファイル名は変数で渡す（art.test.tsx と同じ）。
const LEARNING_CSS = "./learning.css";

function learningCss(): string {
  return readFileSync(new URL(LEARNING_CSS, import.meta.url), "utf8");
}

describe("learning.css（T-433）", () => {
  it("LA-9 16 進の色を直接書かない（トークンだけを使う）", () => {
    const css = learningCss();
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,6}\b/);
    expect(css).toMatch(/var\(--color-/);
  });

  it("LA-10 動きは prefers-reduced-motion: no-preference の中だけ", () => {
    const css = learningCss();
    const start = css.indexOf("@media (prefers-reduced-motion: no-preference)");
    expect(start).toBeGreaterThan(0);
    const before = css.slice(0, start);
    expect(before).not.toMatch(/animation\s*:/);
    // @media の中の { } を数えて、閉じたあとにも animation が無いことを確かめる。
    let depth = 0;
    let end = -1;
    for (let i = css.indexOf("{", start); i < css.length; i++) {
      if (css[i] === "{") depth++;
      if (css[i] === "}" && --depth === 0) {
        end = i;
        break;
      }
    }
    expect(end).toBeGreaterThan(start);
    expect(css.slice(start, end)).toMatch(/animation\s*:/);
    expect(css.slice(end)).not.toMatch(/animation\s*:/);
  });
});
