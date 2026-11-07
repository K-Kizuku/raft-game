// Package cluster は、etcd-io/raft の RawNode を 1 ノードずつ包み、Raft の出力
// （Ready）を処理する土台を提供する（DesignDoc §5、ADR-0003）。
//
// 約束:
//
//   - Ready を扱ってよいのは (*Node).processReady だけ。ほかの場所で
//     RawNode.Ready・RawNode.Advance を呼ばない（テストの補助も processReady を使う）
//   - processReady の処理の順序は「追記（Append）→ HardState の保存（SetHardState）→
//     送信（proto.Clone で複製）→ 適用 → Advance」で固定する（CLAUDE.md・V-5）
//   - Raft のメッセージを書き換えない。ゲーム側で Leader や Commit を決めず、
//     Raft の出力を観察するだけにする
//   - 決定性のため、time.Now・goroutine・チャネル・グローバルな math/rand・
//     map の走査順への依存・浮動小数点を使わない。乱数は NodeConfig.RandIntn で注入する
//     （ADR-0005）
//
// このパッケージは internal/sim を import しない（依存の向きは sim → cluster、
// docs/02-architecture.md §3）。
package cluster
