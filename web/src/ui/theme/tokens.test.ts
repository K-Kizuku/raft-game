import { describe, expect, it } from "vitest";

import {
  colorNumber,
  LARGE_SCREEN_FONT_SCALE,
  LARGE_SCREEN_MIN_WIDTH_PX,
  largeScreenTokens,
  palette,
  tokens,
  type ColorTokenName,
} from "./tokens.ts";

// vitest は CSS の import（`?raw` を含む）を空にするので、tokens.css は node:fs で読む。
// src の tsconfig は Node の型を読まないので、使う関数の型だけを書く。
type NodeFs = { readFileSync(path: URL, encoding: "utf8"): string };
const nodeFs = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ nodeFs)) as NodeFs;
const tokensCss = readFileSync(new URL("./tokens.css", import.meta.url), "utf8");

// `{` から対応する `}` までの中身を返す。
function blockAfter(css: string, start: number): string {
  const open = css.indexOf("{", start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") {
      depth++;
    } else if (css[i] === "}") {
      depth--;
      if (depth === 0) {
        return css.slice(open + 1, i);
      }
    }
  }
  throw new Error("閉じ括弧がない");
}

// ブロックの中のカスタムプロパティを読む（入れ子のブロックは読まない）。
function customProperties(block: string): Map<string, string> {
  const withoutComments = block.replace(/\/\*[\s\S]*?\*\//g, "");
  const result = new Map<string, string>();
  for (const declaration of withoutComments.split(";")) {
    const match = /^\s*(--[a-z0-9-]+)\s*:\s*([\s\S]+?)\s*$/.exec(declaration);
    if (match?.[1] !== undefined && match[2] !== undefined) {
      result.set(match[1], match[2].replace(/\s+/g, " "));
    }
  }
  return result;
}

const css = tokensCss.replace(/\/\*[\s\S]*?\*\//g, "");
const mediaStart = css.indexOf(`@media (min-width: ${LARGE_SCREEN_MIN_WIDTH_PX}px)`);
const rootBlock = customProperties(blockAfter(css, css.indexOf(":root")));
const mediaBlock = mediaStart < 0 ? "" : blockAfter(css, mediaStart);
const largeRootBlock = customProperties(
  mediaBlock.includes(":root") ? blockAfter(mediaBlock, mediaBlock.indexOf(":root")) : "",
);

describe("tokens.css と tokens.ts", () => {
  it(":root の名前と値が一致する", () => {
    expect(Object.fromEntries(rootBlock)).toEqual({ ...tokens });
  });

  it("大きな画面の @media がある", () => {
    expect(mediaStart).toBeGreaterThan(0);
  });

  it("大きな画面の文字の大きさが一致し、基準の 1.25 倍になっている", () => {
    expect(Object.fromEntries(largeRootBlock)).toEqual({ ...largeScreenTokens });
    for (const [name, value] of Object.entries(largeScreenTokens)) {
      const base = Number.parseFloat(tokens[name as keyof typeof largeScreenTokens]);
      expect(Number.parseFloat(value)).toBe(base * LARGE_SCREEN_FONT_SCALE);
    }
  });

  it("色はどれも #RRGGBB で、colorNumber と palette が同じ数値を返す", () => {
    const colorNames = Object.keys(tokens).filter((name): name is ColorTokenName =>
      name.startsWith("--color-"),
    );
    expect(colorNames).toHaveLength(Object.keys(palette).length);
    for (const name of colorNames) {
      expect(tokens[name]).toMatch(/^#[0-9a-f]{6}$/);
      const camel = name
        .slice("--color-".length)
        .replace(/-([a-z])/g, (_, c: string) => c.toUpperCase()) as keyof typeof palette;
      expect(palette[camel]).toBe(colorNumber(name));
    }
    expect(colorNumber("--color-sea-mid")).toBe(0x8ec5d6);
  });
});

// WCAG 2 の相対輝度とコントラスト比。
function relativeLuminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = channels.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
}

function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

describe("コントラスト比（NFR-10）", () => {
  it("計算が既知の値と合う", () => {
    expect(contrastRatio("#000000", "#FFFFFF")).toBeCloseTo(21, 5);
    expect(contrastRatio("#FFFFFF", "#FFFFFF")).toBeCloseTo(1, 5);
  });

  it.each([
    ["本文と地色", tokens["--color-ink"], tokens["--color-paper"]],
    ["本文とパネル", tokens["--color-ink"], tokens["--color-paper-deep"]],
    ["主ボタン（白と郵便の赤）", "#FFFFFF", tokens["--color-postal"]],
  ])("%s が 4.5:1 以上", (_, foreground, background) => {
    expect(contrastRatio(foreground, background)).toBeGreaterThanOrEqual(4.5);
  });
});
