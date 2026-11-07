// セッションのシード（T-413）。画面がシードを決めて送る（Worker に任せると、
// 「同じ条件でもう一度」に使うシードが画面に分からないため）。

export function randomSeed(
  getRandomValues: (array: Uint32Array<ArrayBuffer>) => Uint32Array = (array) =>
    crypto.getRandomValues(array),
): number {
  const values = getRandomValues(new Uint32Array(1));
  return values[0] ?? 0;
}
