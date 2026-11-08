// 絵の形を決める小さな擬似乱数（T-421）。同じ種なら同じ列を返す（Math.random を使わない）。
// mulberry32: 32 ビットの状態で、絵のばらつきには十分。

/** 種から [0, 1) の数を返す関数を作る。 */
export function createRandom(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** 2 つの整数を混ぜて種にする（島の ID と固定の種など）。 */
export function mixSeed(a: number, b: number): number {
  let h = Math.imul(a ^ 0x9e3779b9, 0x85ebca6b) ^ b;
  h = Math.imul(h ^ (h >>> 13), 0xc2b2ae35);
  return (h ^ (h >>> 16)) >>> 0;
}
