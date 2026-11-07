// 依頼の状態の表示（T-406・ゲームデザイン §4.3・FR-REQ-07）。アイコンと言葉を必ず両方出す。
import type { Request, RequestStatus } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";

export type RequestStatusView = Readonly<{ icon: string; text: string }>;

export const REQUEST_STATUS_VIEW: Readonly<Record<RequestStatus, RequestStatusView>> =
  Object.freeze({
    received: { icon: "📝", text: "受付" },
    waiting: { icon: "📮", text: "送付待ち" },
    in_transit: { icon: "⛴", text: "輸送中" },
    proposed: { icon: "🗳", text: "審議中" },
    committed: { icon: "📜", text: "共和国で確定" },
    succeeded: { icon: "✅", text: "確定" },
    rejected: { icon: "❌", text: "予約失敗" },
  });

// 終わった依頼を残す件数（画面のための値）。
const FINISHED_VISIBLE_COUNT = 3;

function isRequestStatus(status: string): status is RequestStatus {
  return Object.hasOwn(REQUEST_STATUS_VIEW, status);
}

// 知らない状態は「？」と値をそのまま出す（プロトコル §8）。
export function requestStatusView(status: string): RequestStatusView {
  return isRequestStatus(status) ? REQUEST_STATUS_VIEW[status] : { icon: "？", text: status };
}

export function isFinished(request: Request): boolean {
  return request.status === "succeeded" || request.status === "rejected";
}

// 島の依頼（requestIds の順）。進行中のものをすべて、続けて終わったものの最後の 3 件。
// 呼ぶたびに新しい配列を返す（React では useMemo の中で呼ぶ）。
export function islandRequests(world: World, islandId: number): Request[] {
  const active: Request[] = [];
  const finished: Request[] = [];
  for (const id of world.requestIds) {
    const request = Object.hasOwn(world.requests, id) ? world.requests[id] : undefined;
    if (request === undefined || request.origin !== islandId) continue;
    (isFinished(request) ? finished : active).push(request);
  }
  return [...active, ...finished.slice(-FINISHED_VISIBLE_COUNT)];
}
