// ステージ紹介の URL の `?seed=`（T-705・FR-GAME-06・DesignDoc §13.6）。純粋な関数だけを置く。
// 受け付ける範囲は Worker と同じ（プロトコル §2「0 以上 Number.MAX_SAFE_INTEGER 以下の整数」）。
import { formatRoute } from "../router/routes.ts";

export type SeedParam = { status: "none" } | { status: "ok"; seed: number } | { status: "invalid" };

// 10 進の整数（先頭の 0 は不可）。桁数は MAX_SAFE_INTEGER（16 桁）まで。
const SEED_PATTERN = /^(0|[1-9][0-9]{0,15})$/;

// 0 以上 Number.MAX_SAFE_INTEGER 以下の整数か。
function isSeed(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0;
}

// location.search を受ける（先頭の ? はあってもなくてもよい）。seed が複数あれば最初の値だけを見る。
export function parseSeedParam(search: string): SeedParam {
  const value = new URLSearchParams(search).get("seed");
  if (value === null) {
    return { status: "none" };
  }
  if (!SEED_PATTERN.test(value)) {
    return { status: "invalid" };
  }
  const seed = Number(value);
  if (!isSeed(seed)) {
    return { status: "invalid" };
  }
  return { status: "ok", seed };
}

// このシードで遊ぶ URL（ステージ紹介の URL に ?seed= を付けたもの）。作れない URL は作らない。
export function seedUrl(origin: string, stageId: string, seed: number): string {
  if (!isSeed(seed)) {
    throw new Error(`不正なシード: ${String(seed)}`);
  }
  return `${origin}${formatRoute({ name: "stageIntro", stageId })}?seed=${String(seed)}`;
}
