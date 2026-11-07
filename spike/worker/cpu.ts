// CPU を決まった量だけ使う計算（M-4 の limits.cpuMs の確かめ）。浮動小数点を使わない。

export interface BurnResult {
  iters: number;
  checksum: number;
}

/** iters 回の整数の計算（xorshift32）をして checksum を返す。同じ iters なら同じ checksum */
export function burn(iters: number): BurnResult {
  if (!Number.isSafeInteger(iters) || iters < 1) throw new RangeError(`iters must be a positive integer: ${iters}`);
  let x = 0x9e3779b9 | 0;
  let acc = 0;
  for (let i = 0; i < iters; i++) {
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    acc = (acc + (x ^ i)) | 0;
  }
  return { iters, checksum: acc >>> 0 };
}
