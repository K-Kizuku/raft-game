// デザイントークン（T-420・docs/11-visual-design.md）。tokens.css と同じ名前・同じ値を TypeScript で出す。
// PixiJS のマップは色を数値（0xRRGGBB）で使うので、colorNumber・palette で引く。
// 値を変えるときは tokens.css も同じに変える（tokens.test.ts が一致を確かめる）。

export const tokens = {
  "--color-paper": "#f6efdf",
  "--color-paper-deep": "#efe4cc",
  "--color-ink": "#3b3a36",
  "--color-ink-muted": "#6b6457",
  "--color-sea-light": "#bfdde6",
  "--color-sea-mid": "#8ec5d6",
  "--color-sea-deep": "#3c6e8f",
  "--color-land": "#d9c79a",
  "--color-land-green": "#8fb07a",
  "--color-postal": "#c0503a",
  "--color-gold": "#e0a84f",
  "--color-storm": "#5a6472",
  "--color-storm-deep": "#2f3640",
  "--color-success": "#5b9a68",
  "--color-warning": "#e0a84f",
  "--color-danger": "#c0503a",
  "--color-danger-text": "#8b4738",
  "--font-body":
    '"Hiragino Maru Gothic ProN", "BIZ UDPGothic", "Zen Maru Gothic", "Yu Gothic", system-ui, sans-serif',
  "--font-number": 'ui-rounded, "SF Pro Rounded", var(--font-body)',
  "--font-size-body": "16px",
  "--font-size-h3": "20px",
  "--font-size-h2": "24px",
  "--font-size-h1": "32px",
  "--space-1": "4px",
  "--space-2": "8px",
  "--space-3": "12px",
  "--space-4": "16px",
  "--space-5": "24px",
  "--space-6": "32px",
  "--radius-s": "6px",
  "--radius-m": "12px",
  "--radius-l": "20px",
  "--focus-ring-width": "3px",
  "--focus-ring-offset": "2px",
  "--focus-ring-color": "var(--color-sea-deep)",
  "--focus-ring-color-on-sea": "var(--color-ink)",
  "--focus-ring-color-on-night": "var(--color-gold)",
  "--shadow-paper": "0 1px 0 rgba(59, 58, 54, 0.08), 0 4px 12px rgba(59, 58, 54, 0.12)",
} as const;

export type TokenName = keyof typeof tokens;
export type ColorTokenName = Extract<TokenName, `--color-${string}`>;

// 大きな画面（プロジェクター 16:9）の境目と、文字の倍率。
export const LARGE_SCREEN_MIN_WIDTH_PX = 1600;
export const LARGE_SCREEN_FONT_SCALE = 1.25;

// 大きな画面で上書きする文字の大きさ（tokens.css の @media (min-width: 1600px)）。
export const largeScreenTokens = {
  "--font-size-body": "20px",
  "--font-size-h3": "25px",
  "--font-size-h2": "30px",
  "--font-size-h1": "40px",
} as const satisfies Partial<Record<TokenName, string>>;

/** 色のトークンを PixiJS などが使う数値（0xRRGGBB）にする。 */
export function colorNumber(name: ColorTokenName): number {
  return Number.parseInt(tokens[name].slice(1), 16);
}

/** 色のトークンの数値。名前は CSS の `--color-` を除いた camelCase。 */
export const palette = {
  paper: colorNumber("--color-paper"),
  paperDeep: colorNumber("--color-paper-deep"),
  ink: colorNumber("--color-ink"),
  inkMuted: colorNumber("--color-ink-muted"),
  seaLight: colorNumber("--color-sea-light"),
  seaMid: colorNumber("--color-sea-mid"),
  seaDeep: colorNumber("--color-sea-deep"),
  land: colorNumber("--color-land"),
  landGreen: colorNumber("--color-land-green"),
  postal: colorNumber("--color-postal"),
  gold: colorNumber("--color-gold"),
  storm: colorNumber("--color-storm"),
  stormDeep: colorNumber("--color-storm-deep"),
  success: colorNumber("--color-success"),
  warning: colorNumber("--color-warning"),
  danger: colorNumber("--color-danger"),
  dangerText: colorNumber("--color-danger-text"),
} as const;
