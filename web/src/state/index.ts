// 世界の状態の入口（T-403）。ほかのモジュールは ../state/index.ts から import する（テスト用の記録は含めない）。
export { WORLD_LIMITS } from "./world.ts";
export type {
  ClockView,
  FerryView,
  ForecastView,
  LeaderChange,
  LetterExit,
  NoticeView,
  StageStatic,
  World,
} from "./world.ts";
export { applyEvent, applyEvents, withClock, worldFromSnapshot } from "./applyEvent.ts";
export {
  estimateSimTime,
  isAutoFerry,
  leaderId,
  lettersAtPort,
  lettersOnFerry,
  portPiles,
  withAutoFerry,
} from "./selectors.ts";
export { createGameStore, gameStore, useGameStore } from "./store.ts";
export {
  EMPTY_TIMELINE,
  TIMELINE_LIMIT,
  appendTimelineEvents,
  timelineFromSnapshot,
} from "./timeline.ts";
export type { Timeline, TimelineItem, TimelineLeader } from "./timeline.ts";
export type { GameState, GameStore, UiSelection } from "./store.ts";
