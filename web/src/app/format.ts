// 表示用の書式（T-401）。整数の演算だけで書く。

// ミリ秒を秒に切り捨てて `m:ss` にする（例: 180000 →「3:00」、59999 →「0:59」）。
export function formatDuration(ms: number): string {
  // 余りを引いてから割るので、商はいつも整数になる（浮動小数点の丸めに頼らない）。
  const totalSeconds = (ms - (ms % 1000)) / 1000;
  const seconds = totalSeconds % 60;
  const minutes = (totalSeconds - seconds) / 60;
  return `${minutes}:${seconds < 10 ? "0" : ""}${seconds}`;
}
