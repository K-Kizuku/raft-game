// Package protocol は、WebSocket の電文と公開 API の本文の型（docs/06-protocol.md §2〜§9）を定義する。
//
// このパッケージが型の正本（ADR-0014）。`make gen` が tygo で TypeScript の型
// （web/src/protocol/protocol.gen.ts）を生成する。生成物もコミットし、`make gen-check` で差分がないことを確かめる。
//
// 標準ライブラリだけに依存し、internal/sim などに依存しない（docs/02-architecture.md §3）。
// sim の型との変換は protocol/simconv が行う。
//
// tygo で正しい TS を出すため、次の書き方を守る（T-301）。
//   - 文字列の列挙は「type X string」と、X で始まる名前の定数を 2 つ以上。iota や式の定数を置かない
//   - 埋め込みの構造体には tstype:",extends" を付ける
//   - 空の構造体の名前付きの型を作らない（TS では EmptyPayload を使う）
//   - interface{}・any・map[string]any を使わない（TS で any になる）
package protocol
