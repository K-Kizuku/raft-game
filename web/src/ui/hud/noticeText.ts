// 通知（notice）の key から島の郵便局長のセリフを組み立てる（T-406・ゲームデザイン §7.3「通知のキー」）。
// 言い回しは郵便局長の口調（stage.islands[].postmaster.tone。T-406c）で変える。
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
  "ferry.broken",
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

// {船}: params.ferryId の船の名前。分からなければ「船」。
function ferryName(world: World, ferryId: unknown): string {
  if (typeof ferryId !== "string" || !Object.hasOwn(world.ferries, ferryId)) return "船";
  const name = world.ferries[ferryId]?.name;
  return name === undefined || name === "" ? "船" : name;
}

// 故障で出られない長さ（「20 秒」）。until から notice.t を引いて秒に切り上げる（1 以上）。until が有限の数でなければ「しばらく」。
function brokenLength(notice: NoticeView, until: unknown): string {
  if (typeof until !== "number" || !Number.isFinite(until)) return "しばらく";
  const seconds = Math.max(1, Math.ceil((until - notice.t) / 1000));
  return `${String(seconds)} 秒`;
}

// 郵便局長の口調（データ仕様 §5.4・ゲームデザイン §7.3）。サーバーの stage の postmasterTones と同じ並び。
export type PostmasterTone = "casual" | "polite" | "elder";

const POSTMASTER_TONES: readonly PostmasterTone[] = ["casual", "polite", "elder"];

function isPostmasterTone(tone: unknown): tone is PostmasterTone {
  return POSTMASTER_TONES.some((t) => t === tone);
}

function stageIsland(world: World, id: number) {
  return Object.hasOwn(world.stage.islands, id) ? world.stage.islands[id] : undefined;
}

// 島の郵便局長の口調。島・postmaster が無い、または知らない tone なら "casual"。
export function postmasterTone(world: World, islandId: number): PostmasterTone {
  const tone = stageIsland(world, islandId)?.postmaster?.tone;
  return isPostmasterTone(tone) ? tone : "casual";
}

// 話し手: postmaster.name があれば「{島}の{name}」、無い・空白だけなら「{島}の郵便局長」。
export function speakerName(world: World, islandId: number): string {
  const name = stageIsland(world, islandId)?.postmaster?.name.trim() ?? "";
  return `${islandName(world, islandId)}の${name === "" ? "郵便局長" : name}`;
}

// セリフの穴埋めの言葉。使うものだけを作る。
type Words = Readonly<{
  resource: () => string; // {資源}
  island: () => string; // {島}
  leader: string; // {代表}（1 つのセリフの中で最初の 1 か所だけ使う）
  lead: () => string; // {lead}（「n 秒後に」か「まもなく」）
  sea: () => string; // {海域}
  ferry: () => string; // {船}
  broken: () => string; // {n} 秒（故障で出られない長さ。分からなければ「しばらく」）
}>;

type Line = (w: Words) => string;

// key ごとの口調の文（ゲームデザイン §7.3 の表）。casual は T-406 のセリフのまま。
const LINES: ReadonlyMap<string, Readonly<Record<PostmasterTone, Line>>> = new Map<
  string,
  Readonly<Record<PostmasterTone, Line>>
>([
  [
    "request.created",
    {
      casual: (w) => `${w.resource()}の予約をお願いしたいんだ！`,
      polite: (w) => `${w.resource()}の予約をお願いします！`,
      elder: (w) => `${w.resource()}の予約を頼みたいんじゃ！`,
    },
  ],
  [
    "request.succeeded",
    {
      casual: (w) => `やった！ ${w.resource()}が取れたよ！`,
      polite: (w) => `やりました！ ${w.resource()}が取れました！`,
      elder: (w) => `やったのう！ ${w.resource()}が取れたぞい！`,
    },
  ],
  [
    "request.rejected",
    {
      casual: (w) => `えっ、${w.resource()}はもう他の島が予約してたの？`,
      polite: (w) => `えっ、${w.resource()}はもう他の島が予約していたのですか？`,
      elder: (w) => `なんと、${w.resource()}はもう他の島が予約しておったのか？`,
    },
  ],
  [
    "request.expired",
    {
      casual: (w) => `${w.resource()}、間に合わなかったなあ…`,
      polite: (w) => `${w.resource()}、間に合いませんでした…`,
      elder: (w) => `${w.resource()}、間に合わなんだのう…`,
    },
  ],
  [
    "island.silent",
    {
      casual: () => "最近、だれからも手紙が来ないなあ…",
      polite: () => "最近、だれからも手紙が来ません…",
      elder: () => "近ごろ、だれからも手紙が来んのう…",
    },
  ],
  [
    "node.campaign",
    {
      casual: (w) => `${w.leader}と連絡がつかない…わたしが代表に立候補します！`,
      polite: (w) => `${w.leader}と連絡がつきません…わたくしが代表に立候補いたします！`,
      elder: (w) => `${w.leader}と連絡がつかん…わしが代表に立候補するぞい！`,
    },
  ],
  [
    "leader.elected",
    {
      casual: (w) => `これからは${w.island()}が${w.leader}です。よろしく！`,
      polite: (w) => `これからは${w.island()}が${w.leader}です。よろしくお願いします！`,
      elder: (w) => `これからは${w.island()}が${w.leader}じゃ。よろしく頼むぞい！`,
    },
  ],
  [
    "leader.stepped_down",
    {
      casual: (w) => `みんなの返事が来ない…いったん${w.leader}をおります`,
      polite: (w) => `皆さんの返事が来ません…いったん${w.leader}をおります`,
      elder: (w) => `みなの返事が来ん…いったん${w.leader}をおりるとしよう`,
    },
  ],
  [
    "storm.forecast",
    {
      casual: (w) => `${w.lead()}${w.sea()}が荒れるぞ！`,
      polite: (w) => `${w.lead()}${w.sea()}が荒れます！`,
      elder: (w) => `${w.lead()}${w.sea()}が荒れるぞい！`,
    },
  ],
  [
    "ferry.broken",
    {
      casual: (w) => `${w.ferry()}が故障してしまった…${w.broken()}は港から出られないよ`,
      polite: (w) => `${w.ferry()}が故障してしまいました…${w.broken()}は港から出られません`,
      elder: (w) => `${w.ferry()}が故障してしもうた…${w.broken()}は港から出られんのう`,
    },
  ],
]);

export function noticeText(notice: NoticeView, world: World, show: boolean): string | null {
  const lines = LINES.get(notice.key);
  if (lines === undefined) return null;
  const params = notice.params;
  const words: Words = {
    resource: () => resourceLabel(world, params["reqId"]),
    island: () => islandName(world, notice.islandId),
    // {代表}: 1 つのセリフの中で最初の 1 か所だけ併記する。
    leader: withRaftTerm("代表", "Leader", show),
    lead: () => stormLead(notice, params["at"]),
    sea: () => seaArea(world, params["routeIds"]),
    ferry: () => ferryName(world, params["ferryId"]),
    broken: () => brokenLength(notice, params["until"]),
  };
  return lines[postmasterTone(world, notice.islandId)](words);
}
