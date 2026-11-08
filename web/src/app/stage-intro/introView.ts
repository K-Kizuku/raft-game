// ステージの紹介の表示のための純粋な関数（T-416・FR-UI-02）。
// ここにあるのは画面の文言で、ゲームの数値ではない。紹介文そのもの（intro.text）は一覧から受け取る。
import type { StageTextSource } from "../types.ts";

// 離島振興計画書の項目（データ仕様 §2.3 の D2）。
export const PLAN_FIELD_LABELS: Readonly<Record<string, string>> = {
  GEO_LOCATION_AREA: "地理的位置・面積",
  NATURAL_CLIMATE_FEATURES: "自然・気象特性",
  POPULATION_HOUSEHOLDS: "人口・世帯",
  MARITIME_TRANSPORT: "海上交通",
  PORTS: "港湾",
  LOCAL_INDUSTRY_TOURISM: "商工業・観光",
  DISASTER_LAND_CONSERVATION: "防災・国土保全",
};

// 学べることのキーと、ゲームの言葉・Raft の用語（進行表 §9 の「種明かし」の列）。
export const LEARNING_LABELS: Readonly<Record<string, Readonly<{ game: string; raft: string }>>> = {
  log_replication: { game: "記録の複製", raft: "Log Replication" },
  commit: { game: "共和国で確定", raft: "Commit" },
  quorum: { game: "過半数", raft: "Quorum" },
  prevote: { game: "立候補の相談", raft: "PreVote" },
  heartbeat: { game: "定期便", raft: "Heartbeat" },
  election_timeout: { game: "定期便が途切れる", raft: "Election Timeout" },
  leader_election: { game: "代表選び", raft: "Leader Election" },
  check_quorum: { game: "孤立した代表が退く", raft: "CheckQuorum" },
  state_machine_replication: { game: "台帳の複製", raft: "State Machine Replication" },
  network_partition: { game: "嵐", raft: "Network Partition" },
};

// 学べることを紹介で伏せるステージ（進行表 §2「Stage 1 は伏せる」。種明かしの驚きを守る）。
export const LEARNING_HIDDEN_STAGE_IDS: ReadonlySet<string> = new Set(["stage1"]);

// 提供データ（国土交通省『離島に関するデータ』）のデータセットの名前。
const PLAN_DATASET = "mlit-ritou-2026";

// Object.prototype のキー（toString など）を項目・学べることとして読まない。
function lookup<T>(table: Readonly<Record<string, T>>, key: string): T | undefined {
  return Object.hasOwn(table, key) ? table[key] : undefined;
}

// 出典の文。引用（quote）は含めない（部品が別の行に出す）。
export function sourceText(source: StageTextSource): string {
  const field = lookup(PLAN_FIELD_LABELS, source.field) ?? source.field;
  const text = `離島振興計画書（${source.region}）の「${field}」から`;
  return source.dataset === PLAN_DATASET ? text : `${text}（データ: ${source.dataset}）`;
}

export type LearningView =
  | { status: "hidden" }
  | { status: "shown"; items: readonly { key: string; game: string; raft: string }[] }
  | { status: "none" };

// 学べることの表示。learning の順に、知っているキーだけを 1 回ずつ並べる。
export function learningView(stageId: string, learning: readonly string[]): LearningView {
  if (LEARNING_HIDDEN_STAGE_IDS.has(stageId)) {
    return { status: "hidden" };
  }
  const items: { key: string; game: string; raft: string }[] = [];
  for (const key of learning) {
    const label = lookup(LEARNING_LABELS, key);
    if (label !== undefined && !items.some((item) => item.key === key)) {
      items.push({ key, game: label.game, raft: label.raft });
    }
  }
  return items.length > 0 ? { status: "shown", items } : { status: "none" };
}
