// 推定ゲーム内時刻を読むフック（T-406・DesignDoc §13.3）。サーバーの時計（world.clock）と
// performance.now から estimateSimTime（T-403）で推定する。時計が無ければ null。
// 一時停止していなければ intervalMs ごとに読み直し、時計が変わったらすぐ読み直す。
import { useEffect, useState } from "react";

import { estimateSimTime, useGameStore } from "../../state/index.ts";
import type { ClockView } from "../../state/index.ts";

const DEFAULT_INTERVAL_MS = 250;

function performanceNow(): number {
  return performance.now();
}

type Sample = Readonly<{ clock: ClockView; now: number }>;

// now は描画をまたいで同じ関数を渡す（変わるたびに読み直す）。
export function useSimNow(
  intervalMs: number = DEFAULT_INTERVAL_MS,
  now: () => number = performanceNow,
): number | null {
  const clock = useGameStore((s) => s.world?.clock ?? null);
  const [sample, setSample] = useState<Sample | null>(null);

  useEffect(() => {
    if (clock === null) return;
    const read = (): void => {
      setSample({ clock, now: now() });
    };
    // 時計が変わったら（新しい events）すぐ読み直す。一時停止中は時刻が進まないので読み直さない。
    read();
    if (clock.paused) return;
    const id = setInterval(read, intervalMs);
    return () => {
      clearInterval(id);
    };
  }, [clock, intervalMs, now]);

  if (clock === null) return null;
  // まだ読み直していない時計は、受け取った時刻で推定する（= 届いた時刻の値）。
  const at = sample !== null && sample.clock === clock ? sample.now : clock.receivedAt;
  return estimateSimTime(clock, at);
}
