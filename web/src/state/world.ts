// 世界の状態の型（T-403 / DesignDoc §13.2）。サーバーの snapshot と events を写したもので、
// Raft の結果をここで決めない（ADR-0009）。型はすべて ../protocol/index.ts（T-301）のもの。
import type {
  DemoStormView,
  DropReason,
  Ferry,
  Island,
  IslandState,
  Letter,
  NodeSummary,
  Phase,
  Request,
  Resource,
  Route,
  RouteState,
  Score,
  Session,
  StepDownReason,
} from "../protocol/index.ts";

// 時計（プロトコル §7）。receivedAt は受け取った時刻（呼び出し側の performance.now()、ミリ秒）。
export type ClockView = Readonly<{
  simTime: number;
  speed: number;
  paused: boolean;
  endsAt: number;
  allowedSpeeds: readonly number[];
  receivedAt: number;
}>;

// ステージの静的な値（snapshot.stage から作る。イベントでは変わらない）。
export type StageStatic = Readonly<{
  title: string;
  islands: Readonly<Record<number, Island>>;
  islandIds: readonly number[]; // snapshot の順
  routes: Readonly<Record<string, Route>>;
  routeIds: readonly string[]; // snapshot の順
  resources: readonly Resource[];
  demoStorm?: DemoStormView; // デモの嵐（snapshot.stage.demoStorm）。ないステージでは省く
  relayCap?: number; // 島ごとの中継倉庫の上限（snapshot.stage.relayCap）。無い・0 なら積み替えなし
}>;

// 船。brokenUntil はスナップショットの brokenUntil（故障中だけ）と ferry.broken / ferry.repaired が入れる。故障していなければ null。
export type FerryView = Readonly<Omit<Ferry, "brokenUntil"> & { brokenUntil: number | null }>;

export type ForecastView = Readonly<{
  seq: number;
  t: number;
  event: string;
  at: number;
  routeIds: readonly string[];
}>;

export type NoticeView = Readonly<{
  seq: number;
  t: number;
  islandId: number;
  key: string;
  params: Readonly<Record<string, unknown>>;
}>;

// 港・船から消えた手紙（T-410 のアニメーション・T-406 の表示用）。letter は消える直前の値。
export type LetterExit = Readonly<
  | {
      seq: number;
      t: number;
      outcome: "delivered";
      letter: Letter;
      islandId: number;
      accepted: boolean;
    }
  | { seq: number; t: number; outcome: "dropped"; letter: Letter; reason: DropReason }
>;

export type LeaderChange = Readonly<
  | { seq: number; t: number; kind: "elected"; id: number; term: number }
  | {
      seq: number;
      t: number;
      kind: "stepped_down";
      id: number;
      term: number;
      reason: StepDownReason;
    }
>;

export type World = Readonly<{
  session: Session;
  phase: Phase;
  clock: ClockView;
  stage: StageStatic;
  islandStates: Readonly<Record<number, IslandState>>; // 満足度など（snapshot.islandsState と island.updated）
  routeStates: Readonly<Record<string, RouteState>>; // 嵐（snapshot.routesState と route.*）
  forecasts: readonly ForecastView[]; // 嵐の予報（古い順）
  ferries: Readonly<Record<string, FerryView>>;
  ferryIds: readonly string[]; // snapshot の順
  letters: Readonly<Record<number, Letter>>; // 港・船・中継倉庫にある手紙だけ（配達済み・破棄は入れない）
  requests: Readonly<Record<string, Request>>;
  requestIds: readonly string[]; // 出てきた順
  nodes: Readonly<Record<number, NodeSummary>>;
  score: Score;
  notices: readonly NoticeView[]; // 古い順
  letterExits: readonly LetterExit[]; // 古い順
  leaderChanges: readonly LeaderChange[]; // 古い順
  lastEventSeq: number; // 最後に当てはめたイベントの seq（snapshot の lastSeq から）
}>;

// 一覧に残す件数の上限（画面のための値で、ゲームの数値ではない）。超えたら古いものから捨てる。
export const WORLD_LIMITS: Readonly<{
  forecasts: 20;
  notices: 50;
  letterExits: 50;
  leaderChanges: 20;
}> = Object.freeze({ forecasts: 20, notices: 50, letterExits: 50, leaderChanges: 20 });
