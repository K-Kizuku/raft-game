// コントラスト比の計算（T-420 の tokens.test.ts から移した。T-425 で色の組の一覧のテスト contrast.test.ts も使う）。
// 色は "#rrggbb"。半透明の重ね合わせと color-mix(in srgb, …) は、ブラウザと同じく sRGB の値のまま線形に混ぜる。

/** WCAG 2 の相対輝度。 */
export function relativeLuminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = channels.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
}

/** WCAG 2 のコントラスト比（1〜21）。 */
export function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

function channels(hex: string): number[] {
  return [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16));
}

/** color-mix(in srgb, a p%, b) と同じ色（p は 0〜1）。半透明の a を b の上に重ねた色とも同じ。 */
export function mixHex(a: string, b: string, p: number): string {
  const ca = channels(a);
  const cb = channels(b);
  return `#${ca
    .map((v, i) =>
      Math.round(v * p + (cb[i] ?? 0) * (1 - p))
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}
