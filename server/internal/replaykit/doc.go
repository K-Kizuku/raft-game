// Package replaykit は、リプレイ（FR-LOG-04）に要る I/O を持たない部品をまとめる（T-720a。ADR-0028 の R1）。
//
// 入力ログ（DesignDoc §10.1）と記録（§13.5）の形式の正本はここ。simcli・server・WASM（ADR-0028）が共有する。
//
//   - 入力ログの厳密な読み込み DecodeInputLog と、決定的な書き出し EncodeInputLog
//   - 記録のヘッダー NewRecordHeader と組み立て EncodeRecord
//   - snapshot の stage（プロトコル §5.1 の StageView）を作る StageView・RouteID
//   - リザルトの文章に使う名前 StoryNames
//
// I/O を持たない。io.Reader は受けるが、ファイル・環境変数・ネットワーク・時刻・標準出力を使わない
// （WASM の中でも simcli でも同じに動く）。決定性の検査（forbidigo。DesignDoc §9.2）の対象で、
// goroutine・チャネル・select・map の走査順への依存も使わない。
//
// internal/bot・internal/server・net/http を import しない（WASM を小さく保ち、ボットを入れない。ADR-0028）。
// import してよいのは sim・stage・game・protocol・protocol/stageconv と標準ライブラリ。
package replaykit
