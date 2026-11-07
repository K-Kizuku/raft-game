// 金額は内部でマイクロ USD（µUSD、整数）で持つ（1 USD = 1,000,000 µUSD）。
// 浮動小数点の足し算の誤差を帳簿に貯めないため（DesignDoc §12.3.1）。

export const MICRO_PER_USD = 1_000_000;

export function usdToMicro(usd: number): number {
  return Math.round(usd * MICRO_PER_USD);
}

export function microToUsd(micro: number): number {
  return micro / MICRO_PER_USD;
}
