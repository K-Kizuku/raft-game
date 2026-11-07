// 色の定数がデザイントークン（web/src/ui/theme/tokens.ts）から来ていることを確かめる（T-421）。
import { describe, expect, it } from "vitest";

import { palette, tokens } from "../../../ui/theme/tokens.ts";
import { LETTER_EFFECT_STYLE } from "../../animations/style.ts";
import animationStyleSource from "../../animations/style.ts?raw";
import { MAP_DERIVED_COLORS, MAP_STYLE } from "../style.ts";
import mapStyleSource from "../style.ts?raw";
import { mixColor } from "./color.ts";
import artStyleSource from "./style.ts?raw";

const tokenValues = new Set<number | string>([
  ...Object.values(palette),
  ...Object.entries(tokens)
    .filter(([name]) => name.startsWith("--color-"))
    .map(([, value]) => value),
]);
const derivedValues = new Set<number>(Object.values(MAP_DERIVED_COLORS));

describe("mixColor", () => {
  it("2 つの色を t で混ぜる", () => {
    expect(mixColor(0x000000, 0xffffff, 0)).toBe(0x000000);
    expect(mixColor(0x000000, 0xffffff, 1)).toBe(0xffffff);
    expect(mixColor(0x000000, 0xffffff, 0.5)).toBe(0x808080);
    expect(mixColor(0x102030, 0x302010, 0.5)).toBe(0x202020);
    expect(mixColor(0x123456, 0xabcdef, 2)).toBe(0xabcdef); // 範囲の外は端にそろえる
  });
});

describe("マップの色", () => {
  it("MAP_STYLE.colors はすべてトークンの値か、トークンから作った値", () => {
    for (const [name, value] of Object.entries(MAP_STYLE.colors)) {
      expect(
        tokenValues.has(value) || derivedValues.has(value as number),
        `MAP_STYLE.colors.${name}`,
      ).toBe(true);
    }
  });

  it("LETTER_EFFECT_STYLE.colors はすべてトークンの値", () => {
    for (const [name, value] of Object.entries(LETTER_EFFECT_STYLE.colors)) {
      expect(tokenValues.has(value), `LETTER_EFFECT_STYLE.colors.${name}`).toBe(true);
    }
  });

  it("トークンに無い色は、トークンの値を混ぜて作る", () => {
    expect(MAP_DERIVED_COLORS.seaMidBlob).toBe(mixColor(palette.seaMid, palette.seaDeep, 0.3));
    expect(MAP_DERIVED_COLORS.forecastCloud).toBe(mixColor(palette.storm, palette.paper, 0.45));
  });

  it("海の地の色はトークン --color-sea-mid", () => {
    expect(MAP_STYLE.colors.sea).toBe(palette.seaMid);
  });

  it("文字の書体はトークンの値（--font-number は canvas で使えるよう var() を展開する）", () => {
    expect(MAP_STYLE.draw.fontFamily).toBe(tokens["--font-body"]);
    expect(MAP_STYLE.draw.numberFontFamily).not.toContain("var(");
    expect(MAP_STYLE.draw.numberFontFamily).toContain(tokens["--font-body"]);
  });

  it("色の定数のファイルに 16 進の色を直接書いていない", () => {
    const hexColor = /#[0-9a-f]{3,8}\b|0x[0-9a-f]{6}\b/i;
    expect(mapStyleSource).not.toMatch(hexColor);
    expect(animationStyleSource).not.toMatch(hexColor);
    expect(artStyleSource).not.toMatch(/#[0-9a-f]{3,8}\b/i);
  });
});
