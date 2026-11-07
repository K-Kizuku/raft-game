// 通知（notice）の key から島の郵便局長のセリフを組み立てる（T-406・ゲームデザイン §7.3「通知のキー」）。
// 知らない key は null（表示しない。プロトコル §8）。形の違う params は既定の言葉で埋め、例外を出さない。
import type { NoticeView, World } from "../../state/index.ts";
import { withRaftTerm } from "../terms/glossary.ts";

export const NOTICE_KEYS: readonly string[] = Object.freeze([
  "request.created",
  "request.succeeded",
  "request.rejected",
  "request.expired",
  "island.silent",
  "node.campaign",
  "leader.elected",
  "leader.stepped_down",
  "storm.forecast",
]);

// 島の名前（ステージに無ければ「島 id」）。
export function islandName(world: World, id: number): string {
  const island = Object.hasOwn(world.stage.islands, id) ? world.stage.islands[id] : undefined;
  return island?.name ?? `島 ${String(id)}`;
}

// {資源}: params.reqId の依頼の資源の名前。分からなければ「依頼」。
function resourceLabel(world: World, reqId: unknown): string {
  if (typeof reqId !== "string" || !Object.hasOwn(world.requests, reqId)) return "依頼";
  const request = world.requests[reqId];
  if (request === undefined) return "依頼";
  return world.stage.resources.find((r) => r.id === request.resource)?.label ?? "依頼";
}

// {海域}: 航路ごとに「a の島と b の島の間」を「、」でつなぐ。分からなければ「どこかの海」。
function seaArea(world: World, routeIds: unknown): string {
  if (!Array.isArray(routeIds)) return "どこかの海";
  const ids: unknown[] = routeIds;
  if (!ids.every((id): id is string => typeof id === "string")) return "どこかの海";
  const areas: string[] = [];
  for (const id of ids) {
    const route = Object.hasOwn(world.stage.routes, id) ? world.stage.routes[id] : undefined;
    if (route === undefined) continue;
    areas.push(`${islandName(world, route.a)}と${islandName(world, route.b)}の間`);
  }
  return areas.length > 0 ? areas.join("、") : "どこかの海";
}

// 嵐の予報の頭（「5 秒後に」）。at が有限の数でなければ「まもなく」。notice.t を使うので時間がたっても変わらない。
function stormLead(notice: NoticeView, at: unknown): string {
  if (typeof at !== "number" || !Number.isFinite(at)) return "まもなく";
  const seconds = Math.max(1, Math.ceil((at - notice.t) / 1000));
  return `${String(seconds)} 秒後に`;
}

export function noticeText(notice: NoticeView, world: World, show: boolean): string | null {
  // {代表}: 1 つのセリフの中で最初の 1 か所だけ併記する。
  const leader = withRaftTerm("代表", "Leader", show);
  const params = notice.params;
  switch (notice.key) {
    case "request.created":
      return `${resourceLabel(world, params["reqId"])}の予約をお願いしたいんだ！`;
    case "request.succeeded":
      return `やった！ ${resourceLabel(world, params["reqId"])}が取れたよ！`;
    case "request.rejected":
      return `えっ、${resourceLabel(world, params["reqId"])}はもう他の島が予約してたの？`;
    case "request.expired":
      return `${resourceLabel(world, params["reqId"])}、間に合わなかったなあ…`;
    case "island.silent":
      return "最近、だれからも手紙が来ないなあ…";
    case "node.campaign":
      return `${leader}と連絡がつかない…わたしが代表に立候補します！`;
    case "leader.elected":
      return `これからは${islandName(world, notice.islandId)}が${leader}です。よろしく！`;
    case "leader.stepped_down":
      return `みんなの返事が来ない…いったん${leader}をおります`;
    case "storm.forecast":
      return `${stormLead(notice, params["at"])}${seaArea(world, params["routeIds"])}が荒れるぞ！`;
    default:
      return null;
  }
}
