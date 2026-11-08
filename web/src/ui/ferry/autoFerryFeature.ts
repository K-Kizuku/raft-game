// 自動航行のスイッチ（T-725・プロトコル §2 の GET /api/stages の features.autoFerry）。
// Worker の変数 AUTO_FERRY が "1" でなければ features.autoFerry は false で、画面は切り替えを出さない（切り戻し用）。
// 読めない・形が違う・まだ読み込み中のときも false（出さない側に倒す）。
// ステージの一覧の読み込み（app/api/stages.ts）は features を読まないので、ここでページごとに 1 回だけ読む。
import { useEffect, useSyncExternalStore } from "react";

import type { Ack, FerryAutoPayload } from "../../protocol/index.ts";
import type { GameCommandSender } from "../commands/GameCommands.tsx";

const STAGES_PATH = "/api/stages";

// cmd.ferry.auto を送る（プロトコル §4。T-724）。
// 手書きの ClientMessage（protocol/messages.ts）にまだ cmd.ferry.auto が無く、GameCommandSender の型では
// 送れないので、ここだけ型を広げる（送る側の WS クライアントは種類を問わず送る）。messages.ts に足したら、この関数ごと消す。
type FerryAutoSender = (type: "cmd.ferry.auto", payload: FerryAutoPayload) => Promise<Ack>;
export function sendFerryAuto(sender: GameCommandSender, payload: FerryAutoPayload): Promise<Ack> {
  return (sender as unknown as FerryAutoSender)("cmd.ferry.auto", payload);
}

type FeatureState = "idle" | "loading" | "done";

let enabled = false;
let state: FeatureState = "idle";
let generation = 0; // set・reset のたびに増やす（古い読み込みの結果を捨てる）
const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// isAutoFerryFeatureEnabled はいまのスイッチの値（読み込む前・失敗したときは false）。
export function isAutoFerryFeatureEnabled(): boolean {
  return enabled;
}

function getSnapshot(): boolean {
  return enabled;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// parseAutoFerryFeature は /api/stages の本文から features.autoFerry を読む。true のときだけ true。
export function parseAutoFerryFeature(body: unknown): boolean {
  if (!isRecord(body) || !isRecord(body.features)) return false;
  return body.features.autoFerry === true;
}

// setAutoFerryFeature はスイッチの値を決める（読み込んだ結果・テスト）。決めた後は読み込まない。
export function setAutoFerryFeature(value: boolean): void {
  generation++;
  state = "done";
  if (enabled === value) return;
  enabled = value;
  notify();
}

// resetAutoFerryFeature は読み込む前に戻す（テスト用）。
export function resetAutoFerryFeature(): void {
  generation++;
  state = "idle";
  if (!enabled) return;
  enabled = false;
  notify();
}

// loadAutoFerryFeature は /api/stages を読んでスイッチを決める。読み込み中・決めた後は何もしない。
// 失敗したら false のまま、次に呼ばれたときにもう一度読む。
export async function loadAutoFerryFeature(doFetch?: typeof fetch): Promise<void> {
  if (state !== "idle") return;
  state = "loading";
  const mine = generation;
  try {
    const request = doFetch ?? globalThis.fetch;
    const response = await request(STAGES_PATH, {
      method: "GET",
      headers: { Accept: "application/json" },
    });
    if (response.status !== 200) throw new Error(`status ${String(response.status)}`);
    const body: unknown = await response.json();
    if (mine !== generation) return; // 読み込み中に setAutoFerryFeature・reset で決まった
    setAutoFerryFeature(parseAutoFerryFeature(body));
  } catch {
    if (mine === generation) state = "idle";
  }
}

// useAutoFerryFeature は自動航行の切り替えを出してよいか。初めて使うときに /api/stages を読む。
export function useAutoFerryFeature(): boolean {
  useEffect(() => {
    void loadAutoFerryFeature();
  }, []);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
