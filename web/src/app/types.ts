// 画面の間で受け渡すローカルの型（T-401）。
// T-301 の生成された型ができたら、StageSummary・SessionHandle を置き換える。

// プロトコル §2 の GET /api/stages の 1 件。T-301 の生成された型ができたら置き換える。
export type StageSummary = {
  id: string; // STAGE_ID_PATTERN に合う
  title: string;
  region: string;
  islands: number; // 1 以上の整数
  ferries: number; // 1 以上の整数
  durationMs: number; // 1 以上の整数
};

// プロトコル §2 のセッションの作成（POST）の 201 の本文。作るのは T-413。
export type SessionHandle = { sessionId: string; token: string; wsPath: string };

// ステージ選択で選んだもの。T-401 では session は常に null。
export type StageSelection = { stageId: string; session: SessionHandle | null };

// 遊んでいる（遊び終えた）ラウンド。
export type RoundContext = {
  stage: StageSummary;
  session: SessionHandle | null;
  finished: boolean;
};

// ステージの一覧の読み込みの状態。App が持ち、ステージ選択に渡す。
export type StageCatalogState =
  { status: "loading" } | { status: "error" } | { status: "ready"; stages: StageSummary[] };
