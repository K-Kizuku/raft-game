// 依頼の進み具合（T-431・ゲームデザイン §4.3）。受付 → 提案 → 記録 → 確定 → 返事の 5 段階と、止まっている理由の一言。
// 段階は依頼の status（request.created・request.updated）だけから決める（ADR-0009。新しいプロトコルは足さない）。
// status と段階の対応（DesignDoc §8.2 の判定）:
//   received・waiting・in_transit … 受付が済み、代表の提案を待つ（依頼書を代表へ運ぶところ）
//   proposed                       … 代表が提案した。島々の台帳への記録と、代表への返事を待つ
//   committed                      … いずれかの台帳で確定した。依頼元の島に結果（返事）が届くのを待つ
//   succeeded・rejected            … 依頼元の島に結果が届いた（5 段階すべて済み）
import type { Request } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";

export type ProgressStepId = "received" | "propose" | "append" | "commit" | "reply";

// raft: 「Raft の用語を併記する」がオンのときに添える用語（null は添えない）。
export type ProgressStep = Readonly<{ id: ProgressStepId; game: string; raft: string | null }>;

export const PROGRESS_STEPS: readonly ProgressStep[] = Object.freeze([
  { id: "received", game: "受付", raft: null },
  { id: "propose", game: "提案", raft: "Propose" },
  { id: "append", game: "記録", raft: "Append" },
  { id: "commit", game: "確定", raft: "Commit" },
  { id: "reply", game: "返事", raft: null },
]);

export type ProgressStepState = "done" | "current" | "todo" | "failed";

// done: 済んだ段階の数（0〜5）。current: 今の段階の位置（終わった・分からないときは null）。
// outcome: 進行中・確定・予約失敗・分からない（知らない status）。reason: 止まっている理由・結果の一言。
export type RequestProgress = Readonly<{
  done: number;
  current: number | null;
  outcome: "pending" | "succeeded" | "rejected" | "unknown";
  reason: string;
}>;

const STEP_COUNT = PROGRESS_STEPS.length;

export function requestProgress(status: string): RequestProgress {
  switch (status) {
    case "received":
      return { done: 1, current: 1, outcome: "pending", reason: "代表へ送る準備をしています" };
    case "waiting":
      return {
        done: 1,
        current: 1,
        outcome: "pending",
        reason: "依頼書が港で、代表へ運ぶ船を待っています",
      };
    case "in_transit":
      return { done: 1, current: 1, outcome: "pending", reason: "依頼書を代表へ運んでいます" };
    case "proposed":
      return {
        done: 2,
        current: 2,
        outcome: "pending",
        reason: "島々の記録と、代表への返事を待っています",
      };
    case "committed":
      return {
        done: 4,
        current: 4,
        outcome: "pending",
        reason: "依頼元の島に結果が届くのを待っています",
      };
    case "succeeded":
      return { done: STEP_COUNT, current: null, outcome: "succeeded", reason: "結果が届きました" };
    case "rejected":
      return {
        done: STEP_COUNT,
        current: null,
        outcome: "rejected",
        reason: "予約できませんでした",
      };
    default:
      // 知らない値は段階を決めない（プロトコル §8）。
      return {
        done: 0,
        current: null,
        outcome: "unknown",
        reason: `状態が分かりません（${status}）`,
      };
  }
}

// i 番目の段階の状態。予約失敗は最後の段階（返事）を failed にする。
export function stepState(progress: RequestProgress, index: number): ProgressStepState {
  if (progress.outcome === "rejected" && index === STEP_COUNT - 1) return "failed";
  if (index < progress.done) return "done";
  if (index === progress.current) return "current";
  return "todo";
}

function isFinishedStatus(status: string): boolean {
  return status === "succeeded" || status === "rejected";
}

// HUD の重要な依頼: 重要な資源ごと（ステージの resources の順）に、まだ終わっていない最後の依頼。
// 呼ぶたびに新しい配列を返す（React では useMemo の中で呼ぶ）。
export function criticalRequestsInProgress(world: World): Request[] {
  const latest = new Map<string, Request>();
  for (const id of world.requestIds) {
    const request = Object.hasOwn(world.requests, id) ? world.requests[id] : undefined;
    if (request === undefined || !request.critical || isFinishedStatus(request.status)) continue;
    latest.set(request.resource, request);
  }
  const result: Request[] = [];
  for (const resource of world.stage.resources) {
    if (!resource.critical) continue;
    const request = latest.get(resource.id);
    if (request !== undefined) result.push(request);
  }
  return result;
}
