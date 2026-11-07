// WebSocket の電文（プロトコル §3〜§5・§9）の判別可能なユニオン型（T-301 / ADR-0014）。
// payload の型は Go の server/internal/protocol から生成した protocol.gen.ts にある。
import type {
  Ack,
  DemoTriggerPayload,
  ErrorPayload,
  EventsPayload,
  FerryLettersPayload,
  FerryToPayload,
  GamePausePayload,
  GameSkipPayload,
  GameSpeedPayload,
  HelloPayload,
  InternalInitPayload,
  InternalRestorePayload,
  MessageType,
  MessageTypeAck,
  MessageTypeCmdFerryAutoload,
  MessageTypeCmdFerryDepart,
  MessageTypeCmdFerryLoad,
  MessageTypeCmdFerryUnload,
  MessageTypeCmdGamePause,
  MessageTypeCmdGameSkip,
  MessageTypeCmdGameSpeed,
  MessageTypeCmdGameStart,
  MessageTypeCmdRelayLoad,
  MessageTypeCmdSync,
  MessageTypeDemoTrigger,
  MessageTypeError,
  MessageTypeEvents,
  MessageTypeHello,
  MessageTypeInternalInit,
  MessageTypeInternalPing,
  MessageTypeInternalPong,
  MessageTypeInternalRestore,
  MessageTypeInternalResult,
  MessageTypeNodeDetail,
  MessageTypePing,
  MessageTypePong,
  MessageTypeRoundResult,
  MessageTypeSnapshot,
  MessageTypeSubEngineer,
  NodeDetail,
  PingPayload,
  PongPayload,
  RoundResult,
  Snapshot,
  SubEngineerPayload,
  Version,
} from "./protocol.gen.ts";

// EmptyPayload は {} の payload（cmd.game.start・cmd.sync・internal.ping・internal.pong）。
export type EmptyPayload = Record<string, never>;

// Message は電文 1 つ（§3）。seq はサーバー → クライアントでは必ず付く。
type Message<T extends MessageType, P> = {
  v: typeof Version;
  type: T;
  seq?: number;
  cmdId?: string;
  payload: P;
};

// ServerMessageOf はサーバー → クライアントの電文 1 つ（seq を必ず持つ）。
type ServerMessageOf<T extends MessageType, P> = Message<T, P> & { seq: number };

// ClientMessage はクライアント → サーバーの電文（§4）。
export type ClientMessage =
  | Message<typeof MessageTypeHello, HelloPayload>
  | Message<typeof MessageTypeCmdGameStart, EmptyPayload>
  | Message<typeof MessageTypeCmdGamePause, GamePausePayload>
  | Message<typeof MessageTypeCmdGameSpeed, GameSpeedPayload>
  | Message<typeof MessageTypeCmdGameSkip, GameSkipPayload>
  | Message<typeof MessageTypeCmdFerryLoad, FerryLettersPayload>
  | Message<typeof MessageTypeCmdFerryUnload, FerryLettersPayload>
  | Message<typeof MessageTypeCmdFerryAutoload, FerryToPayload>
  | Message<typeof MessageTypeCmdFerryDepart, FerryToPayload>
  | Message<typeof MessageTypeCmdRelayLoad, FerryLettersPayload>
  | Message<typeof MessageTypeCmdSync, EmptyPayload>
  | Message<typeof MessageTypeSubEngineer, SubEngineerPayload>
  | Message<typeof MessageTypeDemoTrigger, DemoTriggerPayload>
  | Message<typeof MessageTypePing, PingPayload>;

// ServerMessage はサーバー → クライアントの電文（§5）。
export type ServerMessage =
  | ServerMessageOf<typeof MessageTypeSnapshot, Snapshot>
  | ServerMessageOf<typeof MessageTypeEvents, EventsPayload>
  | ServerMessageOf<typeof MessageTypeAck, Ack>
  | ServerMessageOf<typeof MessageTypeNodeDetail, NodeDetail>
  | ServerMessageOf<typeof MessageTypeRoundResult, RoundResult>
  | ServerMessageOf<typeof MessageTypeError, ErrorPayload>
  | ServerMessageOf<typeof MessageTypePong, PongPayload>;

// InternalMessage は Durable Object ⇔ コンテナの電文（§9）。
export type InternalMessage =
  | Message<typeof MessageTypeInternalInit, InternalInitPayload>
  | Message<typeof MessageTypeInternalRestore, InternalRestorePayload>
  | Message<typeof MessageTypeInternalResult, RoundResult>
  | Message<typeof MessageTypeInternalPing, EmptyPayload>
  | Message<typeof MessageTypeInternalPong, EmptyPayload>;
