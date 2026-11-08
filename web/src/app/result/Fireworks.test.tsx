// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { FIREWORK_BURSTS, Fireworks } from "./Fireworks.tsx";

// vitest は CSS の import を空にするので、CSS とソースは node:fs で読む（tokens.test.ts と同じ）。
// src の tsconfig は Node の型を読まないので、使う関数の型だけを書く。
type NodeFs = {
  readFileSync(path: URL, encoding: "utf8"): string;
  readdirSync(path: URL, options: { recursive: true }): string[];
};
const nodeFs = "node:fs";
const { readFileSync, readdirSync } = (await import(/* @vite-ignore */ nodeFs)) as NodeFs;

// 花火の絵文字（このファイルにも書かないよう、コードポイントで組み立てる）。
const FIREWORK_EMOJI = [String.fromCodePoint(0x1f386), String.fromCodePoint(0x1f387)];
const HEX_COLOR = /#[0-9a-fA-F]{3,6}\b/;

// new URL("<文字列>", import.meta.url) と直接書くと、Vite がアセットの URL に置き換えるので、変数を通す。
function fileUrl(path: string): URL {
  return new URL(path, import.meta.url);
}

afterEach(() => {
  cleanup();
});

function burstPositions(container: HTMLElement): [string, string][] {
  return Array.from(container.querySelectorAll<HTMLElement>(".firework")).map((el) => [
    el.style.left,
    el.style.top,
  ]);
}

describe("Fireworks", () => {
  it("FW-1 文字の帯と、決まった位置の花火", () => {
    const { container } = render(<Fireworks />);

    const region = screen.getByRole("region", { name: "花火大会" });
    expect(within(region).getByText("花火大会")).toBeDefined();

    const sky = container.querySelector(".fireworks-sky");
    expect(sky?.getAttribute("aria-hidden")).toBe("true");
    const bursts = sky?.querySelectorAll<HTMLElement>(".firework") ?? [];
    expect(bursts).toHaveLength(FIREWORK_BURSTS.length);
    expect(FIREWORK_BURSTS).toHaveLength(8);
    FIREWORK_BURSTS.forEach((burst, i) => {
      const el = bursts[i];
      expect(el?.dataset["burst"]).toBe(String(i));
      expect(el?.style.left).toBe(`${burst.x}%`);
      expect(el?.style.top).toBe(`${burst.y}%`);
      expect(el?.style.animationDelay).toBe(`${burst.delayMs}ms`);
    });

    const first = burstPositions(container);
    cleanup();
    const again = render(<Fireworks />);
    expect(burstPositions(again.container)).toEqual(first);

    for (const tag of ["img", "audio", "canvas"]) {
      expect(again.container.querySelector(tag)).toBeNull();
    }
  });

  it("FW-2 位置と遅れの範囲", () => {
    for (const burst of FIREWORK_BURSTS) {
      expect(Number.isInteger(burst.x)).toBe(true);
      expect(Number.isInteger(burst.y)).toBe(true);
      expect(Number.isInteger(burst.delayMs)).toBe(true);
      expect(burst.x).toBeGreaterThanOrEqual(0);
      expect(burst.x).toBeLessThanOrEqual(100);
      expect(burst.y).toBeGreaterThanOrEqual(0);
      expect(burst.y).toBeLessThanOrEqual(100);
      expect(burst.delayMs).toBeGreaterThanOrEqual(0);
      expect(burst.delayMs).toBeLessThanOrEqual(1400);
    }
  });

  it("FW-3 絵文字を使わず、打ち上げと開く花を CSS の部品で描く", () => {
    const { container } = render(<Fireworks />);
    for (const emoji of FIREWORK_EMOJI) {
      expect(container.textContent).not.toContain(emoji);
    }
    const sky = container.querySelector(".fireworks-sky");
    expect(sky?.textContent).toBe("");
    const bursts = Array.from(container.querySelectorAll<HTMLElement>(".firework"));
    FIREWORK_BURSTS.forEach((burst, i) => {
      const el = bursts[i];
      expect(el?.style.color).toBe(`var(${burst.tone})`);
      expect(el?.querySelectorAll(".firework-trail")).toHaveLength(1);
      expect(el?.querySelectorAll(".firework-bloom")).toHaveLength(1);
    });
  });

  it("FW-4 見出しの両側は SVG の花火のアイコンで、読み上げは「花火大会」", () => {
    const { container } = render(<Fireworks />);
    const banner = container.querySelector(".fireworks-banner");
    const icons = banner?.querySelectorAll('svg[data-icon="firework"]') ?? [];
    expect(icons).toHaveLength(2);
    for (const icon of Array.from(icons)) {
      expect(icon.getAttribute("aria-hidden")).toBe("true");
    }
    expect(banner?.textContent?.trim()).toBe("花火大会");
  });

  it("FW-5 CSS: 動きを減らす設定で止め、16 進の色はローカルの変数にだけ書く", () => {
    const css = readFileSync(fileUrl("./Fireworks.css"), "utf8");
    const reduced = css.slice(css.indexOf("@media (prefers-reduced-motion: reduce)"));
    expect(reduced).toContain(".firework-bloom");
    expect(reduced).toContain("animation: none");
    expect(reduced).toContain(".firework-trail");
    expect(css).toMatch(/@keyframes fireworks-launch/);
    expect(css).toMatch(/@keyframes fireworks-bloom/);
    const declarations = css.replace(/\/\*[\s\S]*?\*\//g, "");
    for (const line of declarations.split("\n")) {
      if (HEX_COLOR.test(line)) {
        expect(line.trim(), line).toMatch(/^--firework-[a-z]+: #[0-9a-f]{6};$/);
      }
    }
    // ローカルの変数は、TS の型（FireworkLocalColor）で使うものとそろえる。
    for (const burst of FIREWORK_BURSTS) {
      if (burst.tone.startsWith("--firework-")) {
        expect(css).toContain(`${burst.tone}: #`);
      }
    }
  });
});

describe("花火の絵文字", () => {
  it("FW-6 web/src に花火の絵文字が残っていない", () => {
    const root = fileUrl("../../");
    const files = readdirSync(root, { recursive: true }).filter((f) =>
      /\.(tsx?|css|html|json|md)$/.test(f),
    );
    expect(files.length).toBeGreaterThan(10);
    for (const file of files) {
      const text = readFileSync(new URL(file, root), "utf8");
      for (const emoji of FIREWORK_EMOJI) {
        expect(text.includes(emoji), file).toBe(false);
      }
    }
  });
});
