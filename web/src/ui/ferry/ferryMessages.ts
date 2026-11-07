// フェリー操作の結果と押せない理由の文（T-405・プロトコル §4.1）。成否はサーバーの ack が決める。
import type { Ack } from "../../protocol/index.ts";
import { formatRemaining } from "../hud/format.ts";
import type { FerryBlocker, ViaHint } from "./ferryView.ts";

// 港の隣でない島宛ての手紙の案内（ゲームデザイン §3.2・§3.4）。
export function viaHintText(hint: Pick<ViaHint, "toName" | "viaName">): string {
  return `${hint.toName}あての手紙は${hint.viaName}を経由して運びます。まず${hint.viaName}へ`;
}

// 積み荷の経由の札（「豊島経由」）。
export function viaLabel(viaName: string): string {
  return `${viaName}経由`;
}

export type FerryOp = "load" | "unload" | "autoload" | "depart";

export const FERRY_OP_NAMES: Readonly<Record<FerryOp, string>> = Object.freeze({
  load: "積み込み",
  unload: "荷降ろし",
  autoload: "おまかせ積み込み",
  depart: "出航",
});

const OK_MESSAGES: Readonly<Record<Exclude<FerryOp, "depart">, string>> = {
  load: "手紙を積みました。",
  unload: "手紙を港に戻しました。",
  autoload: "おまかせで積みました。",
};

// 知らない code・code なしは「{操作}できませんでした。」（プロトコル §8）。
export function ferryResultMessage(
  op: FerryOp,
  result: Ack | "rejected",
  toName: string | null,
): string {
  const opName = FERRY_OP_NAMES[op];
  const to = toName ?? "行き先";
  if (result === "rejected") {
    return `サーバーにつながっていないため、${opName}できませんでした。`;
  }
  if (result.ok) {
    return op === "depart" ? `${to}へ出航しました。` : OK_MESSAGES[op];
  }
  switch (result.code) {
    case "ferry_not_docked":
      return `船が港にいないため、${opName}できません。`;
    case "ferry_not_ready":
      return "停泊の時間がまだ終わっていません。少し待ってください。";
    case "ferry_broken":
      return `船が故障しているため、${opName}できません。`;
    case "capacity_exceeded":
      return "積み荷がいっぱいです。";
    case "letter_not_here":
      if (op === "load") return "その手紙はもう港にありません。";
      if (op === "unload") return "その手紙はもう船にありません。";
      if (op === "autoload") return `港に${to}宛ての手紙がありません。`;
      break;
    case "no_route":
      return `${to}へは直接の航路がありません。`;
    case "route_blocked":
      return `嵐で${to}への航路が通れません。`;
    case "phase_invalid":
      return `いまは${opName}できません。`;
    case "rate_limited":
      return "操作が多すぎます。少し待ってください。";
    default:
      break;
  }
  return `${opName}できませんでした。`;
}

export function blockerText(blocker: FerryBlocker, readyInMs: number): string {
  switch (blocker) {
    case "sailing":
      return "航行中は操作できません。";
    case "no_destination":
      return "行き先を選んでください。";
    case "not_ready":
      return `出航まで あと ${formatRemaining(readyInMs)}`;
    case "route_blocked":
      return "嵐で航路が通れません。";
  }
}
