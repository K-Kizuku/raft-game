// ブラウザでのリプレイ（FR-LOG-04・ADR-0028 R4・T-720d）の上限。
// R2 の WASM（server/cmd/replaywasm）の too_large と同じ値（1 MiB）。
export const MAX_INPUT_LOG_BYTES = 1_048_576;
