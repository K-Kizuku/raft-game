// トークンの色から別の色を作る（T-421）。トークンに無い色が要るときは、ここで 2 つのトークンを混ぜて作る。

/** 0xRRGGBB の a と b を t（0〜1。0 で a、1 で b）で混ぜる。 */
export function mixColor(a: number, b: number, t: number): number {
  const k = Math.min(1, Math.max(0, t));
  const channel = (shift: number): number => {
    const ca = (a >> shift) & 0xff;
    const cb = (b >> shift) & 0xff;
    return Math.round(ca + (cb - ca) * k) << shift;
  };
  return (channel(16) | channel(8) | channel(0)) >>> 0;
}
