// 画面の間で受け渡すローカルの型（T-401・T-413・T-416）。SessionHandle・StageSummary は生成した型（T-301）から作る。
// StageText は T-301 の生成された型に揃えるまでローカルの型。
import type {
  CreateSessionResponse,
  StageSummary as ProtocolStageSummary,
} from "../protocol/index.ts";
import type { Provenance } from "../ui/provenance/ProvenanceLabel.tsx";

// プロトコル §2 の POST /api/sessions の 201 の本文（T-413）。
export type SessionHandle = CreateSessionResponse;

// プロトコル §2 の GET /api/stages の 1 件のうち、画面が読む欄。unlock（P1）は今は読まない。
// 紹介文など（intro・learning）は StageCatalogEntry で足す。
export type StageSummary = Omit<ProtocolStageSummary, "unlock" | "intro" | "learning" | "demo"> & {
  // T-311。true なら POST /api/sessions で demo: true を送る。一覧の読み込み（toStageSummary）は
  // 必ず真偽値を入れる。省略できるのは、範囲の外のテスト（result・game）が作る値のため。
  demo?: boolean;
};

// プロトコル §2 の GET /api/stages の intro（データ仕様 §5.2）。T-301 の生成された型に揃えるまでのローカルの型。
export type StageTextSource = {
  dataset: string;
  region: string;
  field: string;
  quote: string | null;
};
export type StageText = { text: string; provenance: Provenance; source: StageTextSource | null };
// 一覧の 1 件（紹介文と学べることつき）。RoundContext の stage には StageSummary として渡せる。
export type StageCatalogEntry = StageSummary & { intro: StageText; learning: readonly string[] };

// ステージ選択で選んだもの。seed はそのセッションを作ったときに送ったシード。
export type StageSelection = {
  stageId: string;
  session: SessionHandle | null;
  seed: number | null;
};

// 遊んでいる（遊び終えた）ラウンド。seed はサーバーなしで進んだときも、決めたシードを持つ
// （「同じ条件でもう一度」に使う）。flowReducer は必ず入れる。省略できるのは、範囲の外の
// テスト（game）が作る値のため。
export type RoundContext = {
  stage: StageSummary;
  session: SessionHandle | null;
  seed?: number | null;
  finished: boolean;
};

// ステージの一覧の読み込みの状態。App が持ち、ステージ選択に渡す。
export type StageCatalogState =
  { status: "loading" } | { status: "error" } | { status: "ready"; stages: StageCatalogEntry[] };
