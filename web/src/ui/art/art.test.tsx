// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { ArtIcon, type ArtIconKind } from "./ArtIcon.tsx";
import { TitleArt } from "./TitleArt.tsx";

// vitest は CSS の import を空にするので、CSS は node:fs で読む（tokens.test.ts と同じ）。
// src の tsconfig は Node の型を読まないので、使う関数の型だけを書く。
type NodeFs = { readFileSync(path: URL, encoding: "utf8"): string };
const nodeFs = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ nodeFs)) as NodeFs;

afterEach(() => {
  cleanup();
});

const HEX_COLOR = /#[0-9a-fA-F]{3,6}\b/;
const KINDS: readonly ArtIconKind[] = [
  "island",
  "port",
  "ship",
  "letter",
  "star",
  "compass",
  "back",
];

describe("TitleArt", () => {
  it("AR-1 aria-hidden で、文字を持たない", () => {
    const { container } = render(<TitleArt className="x" />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("focusable")).toBe("false");
    expect(svg?.classList.contains("title-art")).toBe(true);
    expect(svg?.classList.contains("x")).toBe(true);
    expect(svg?.textContent).toBe("");
  });

  it("AR-2 島が 4 つ・船・封筒・方位記号・航路の点線を描く", () => {
    const { container } = render(<TitleArt />);
    expect(container.querySelectorAll(".art-shoal")).toHaveLength(4);
    expect(container.querySelectorAll(".title-art-ship")).toHaveLength(1);
    expect(container.querySelectorAll(".title-art-float")).toHaveLength(3);
    expect(container.querySelectorAll(".title-art-route").length).toBeGreaterThanOrEqual(3);
    expect(container.querySelectorAll(".art-gold").length).toBeGreaterThan(0);
  });

  it("AR-3 2 つ描いても、グラデーション・フィルターの id がぶつからない", () => {
    const { container } = render(
      <>
        <TitleArt />
        <TitleArt />
      </>,
    );
    const ids = Array.from(container.querySelectorAll("[id]")).map((e) => e.id);
    expect(ids.length).toBeGreaterThan(0);
    expect(new Set(ids).size).toBe(ids.length);
    for (const svg of Array.from(container.querySelectorAll("svg"))) {
      const own = new Set(Array.from(svg.querySelectorAll("[id]")).map((e) => e.id));
      for (const ref of svg.innerHTML.matchAll(/url\(#([^)]+)\)/g)) {
        expect(own.has(ref[1] ?? "")).toBe(true);
      }
    }
  });
});

describe("ArtIcon", () => {
  it("AR-4 どの種類も aria-hidden で、文字を持たない", () => {
    for (const kind of KINDS) {
      const { container, unmount } = render(<ArtIcon kind={kind} className="y" />);
      const svg = container.querySelector("svg");
      expect(svg?.getAttribute("aria-hidden")).toBe("true");
      expect(svg?.getAttribute("data-art-icon")).toBe(kind);
      expect(svg?.classList.contains("y")).toBe(true);
      expect(svg?.textContent).toBe("");
      expect(svg?.childElementCount).toBeGreaterThan(0);
      unmount();
    }
  });
});

describe("色", () => {
  it("AR-5 絵の部品と画面の CSS は 16 進の色を直接書かない（トークンを使う）", () => {
    const { container } = render(
      <>
        <TitleArt />
        {KINDS.map((kind) => (
          <ArtIcon key={kind} kind={kind} />
        ))}
      </>,
    );
    expect(container.innerHTML).not.toMatch(HEX_COLOR);
    for (const file of [
      "./art.css",
      "../../app/app.css",
      "../../app/title/TitleScreen.css",
      "../../app/howto/HowToScreen.css",
      "../../app/credits/CreditsScreen.css",
      "../../app/settings/SettingsScreen.css",
    ]) {
      const css = readFileSync(new URL(file, import.meta.url), "utf8");
      expect(css, file).not.toMatch(HEX_COLOR);
    }
  });
});
