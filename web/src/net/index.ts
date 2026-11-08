// net の入口（T-402）。画面とストアはここから import する。
// mock/ と fixtures/ は本番のビルドに入れないので、ここから再輸出しない（記録の再生の本体は replay/ にあり、ここから再輸出する）。
export { buildWsUrl, WS_PATH_PATTERN } from "./url.ts";
export { decodeServerMessage, encodeClientMessage } from "./codec.ts";
export type { ClientMessageType, ClientPayload, DecodeResult } from "./codec.ts";
export { acceptEvents, checkEnvelopeSeq } from "./sequence.ts";
export type { EnvelopeSeqVerdict } from "./sequence.ts";
export { browserTimers, createManualTimers } from "./timers.ts";
export type { ManualTimers, TimerId, Timers } from "./timers.ts";
export { createBrowserSocketFactory } from "./socket.ts";
export type { SocketFactory, SocketHandlers, SocketLike } from "./socket.ts";
export {
  CLIENT_NAME,
  CommandError,
  createGameConnection,
  DEFAULT_CONNECTION_CONFIG,
} from "./client.ts";
export type {
  CommandErrorReason,
  CommandType,
  ConnectionConfig,
  ConnectionHandlers,
  ConnectionState,
  ConnectionStatus,
  FailureReason,
  GameConnection,
  GameConnectionOptions,
} from "./client.ts";
export {
  DEFAULT_ALLOWED_SPEEDS,
  parseRecord,
  RecordFormatError,
  snapshotFromRecord,
} from "./replay/record.ts";
export type { GameRecord, RecordHeader, RecordSnapshot } from "./replay/record.ts";
export { createRecordServer, placeholderRoundResult, readOnlyAckPolicy } from "./replay/server.ts";
export type {
  AckPolicy,
  ReceivedMessage,
  RecordServer,
  RecordServerOptions,
} from "./replay/server.ts";
