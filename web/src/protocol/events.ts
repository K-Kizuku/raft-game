// イベント（プロトコル §6）の判別可能なユニオン型（T-301 / ADR-0014）。
// 各イベントの型は Go の server/internal/protocol から生成した protocol.gen.ts にある。
// ユニオン型はここ 1 か所だけで手で組み立てる。イベントを足したら、ここにも足す（protocol.test.ts が検出する）。
import type {
  FerryArrivedEvent,
  FerryBrokenEvent,
  FerryDepartedEvent,
  FerryRepairedEvent,
  ForecastEvent,
  IslandUpdatedEvent,
  LeaderElectedEvent,
  LedgerAppliedEvent,
  LetterCreatedEvent,
  LetterDeliveredEvent,
  LetterDroppedEvent,
  LetterMovedEvent,
  LetterStaleEvent,
  NodeStateEvent,
  NoticeEvent,
  PhaseChangedEvent,
  RequestCreatedEvent,
  RequestUpdatedEvent,
  RouteBlockedEvent,
  RouteUnblockedEvent,
  ScoreUpdatedEvent,
  SteppedDownEvent,
} from "./protocol.gen.ts";

// ServerEvent は events.events[] の要素。type で 22 種類に分けられる。
export type ServerEvent =
  | PhaseChangedEvent
  | LetterCreatedEvent
  | LetterMovedEvent
  | LetterDeliveredEvent
  | LetterDroppedEvent
  | LetterStaleEvent
  | FerryDepartedEvent
  | FerryArrivedEvent
  | FerryBrokenEvent
  | FerryRepairedEvent
  | RouteBlockedEvent
  | RouteUnblockedEvent
  | ForecastEvent
  | NodeStateEvent
  | LeaderElectedEvent
  | SteppedDownEvent
  | RequestCreatedEvent
  | RequestUpdatedEvent
  | LedgerAppliedEvent
  | IslandUpdatedEvent
  | ScoreUpdatedEvent
  | NoticeEvent;
