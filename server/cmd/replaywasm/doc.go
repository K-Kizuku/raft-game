// Command replaywasm は、ブラウザでのリプレイ（FR-LOG-04。ADR-0028 の R2・T-720b。DesignDoc §10.4）の WASM のエントリ。
//
// 入力ログ（DesignDoc §10.1）を厳密に読み、埋め込んだステージ定義（-tags embeddata。ADR-0025）で
// サーバーと同じ Go のパッケージ（sim・stage・replaykit・protocol/simconv・game）で再シミュレーションし、
// 記録（DesignDoc §13.5。bot は空）と画面に要る値を返す。internal/bot は入れない。
//
// # ビルド
//
//	scripts/build-replay-wasm.sh [--verify] [--out DIR]   # make wasm・make wasm-check
//
// 中身は make -s sync-data の後に
//
//	GOOS=js GOARCH=wasm go -C server build -trimpath -tags embeddata \
//	  -ldflags "-s -w -X main.build=${BUILD_ID:-dev}" -o web/public/wasm/raft-replay.wasm ./cmd/replaywasm
//
// と、$(go -C server env GOROOT)/lib/wasm/wasm_exec.js の複製。出力の web/public/wasm/ はコミットしない。
// ネイティブでビルドしたもの（main_other.go）は、メッセージを出して終了コード 2 で終わる。
//
// # JS との約束（globalThis.raftReplay）
//
//	// 入力ログの JSON の文字列（ファイルの中身そのまま）を受け、応答の JSON の文字列を返す。同期の呼び出し。
//	raftReplay(inputLogText: string): string
//
// 成功の応答:
//
//	{"ok":true,"build":"git:1234567","ignored":0,
//	 "record":{…記録（DesignDoc §13.5）。replaykit.EncodeRecord の出力をそのまま入れる。bot は ""…},
//	 "stage":{…StageView（プロトコル §5.1 の stage）…},
//	 "islandsState":[…プロトコル §5.1 の islandsState（初めのスナップショットの値）…],
//	 "score":{…プロトコル §5.1 の score（初めのスナップショットの値）…},
//	 "result":{…RoundResult（プロトコル §5.5）…}}
//
// 失敗の応答:
//
//	{"ok":false,"build":"git:1234567","code":"stage_mismatch","message":"…","stageId":"stage2","logBuild":"git:abcdef0"}
//
// 応答のキー:
//
//   - build は WASM のビルドの ID（-ldflags "-X main.build=…"。既定 dev）。ignored は捨てたセッションのコマンドの件数（InputLog.Ignored）
//   - stageId・logBuild は、入力ログを読めたときだけ付ける（logBuild は入力ログの build）
//
// code の一覧（R4 はこの値で文言を決める）:
//
//	| code          | いつ                                                                                                   |
//	|----------------|--------------------------------------------------------------------------------------------------------|
//	| too_large      | 入力が 1 MiB（1,048,576 バイト）を超える。読む前に断る                                                 |
//	| bad_input      | replaykit.DecodeInputLog の誤り（JSON でない・未知のキー・必須のキーがない・形の誤り・後ろの余分なデータ）。message は Decode の誤りの文 |
//	| unknown_stage  | 入力ログの stageId が、埋め込んだステージ定義にない                                                    |
//	| stage_mismatch | stageHash が埋め込んだ定義と違う（sim.Replay の ErrReplayStage）                                       |
//	| diverged       | 再生が入力ログを再現できない（sim.ErrReplayDiverged・sim.ErrReplayOrder。版が違うときに起きうる）      |
//	| not_started    | 再生の後も phase が intro（cmd.game.start がない入力ログ）                                             |
//	| internal       | ステージ定義を埋め込んでいない・読めない、記録を作れない、panic（recover する）                        |
//
// 引数が文字列 1 つでないときは bad_input を返す。
//
// # 再生の手順（handle）
//
//  1. 長さが 1 MiB を超えれば too_large
//  2. replaykit.DecodeInputLog で読む
//  3. 埋め込んだ一覧から stageId のステージを探し（なければ unknown_stage）、stage.SimConfig で Config を作る
//  4. 初めのスナップショット: sim.New の直後の Snapshot()（simcli replay と同じ時点。phase は intro、lastSeq は 0）
//  5. sim.Replay（Until は 0）。ErrReplayStage → stage_mismatch、ErrReplayDiverged・ErrReplayOrder → diverged、ほか → internal
//  6. 航行中ならラウンドの終わり（durationMs）まで進める。intro のままなら not_started
//  7. DrainEvents と TraceHash から記録を作る（replaykit.EncodeRecord。bot は ""）
//  8. stage は replaykit.StageView、islandsState・score は初めのスナップショットから、result は simconv.RoundResultFromSim（inputLogAvailable は false）
//
// ステージ定義の一覧は起動のときに 1 回だけ読む。不変条件の検査は付けない（結果は変わらない）。
// 同じ入力で何度呼んでも、同じ応答のバイト列になる。入力ログの中身を console に出さない
// （panic のときだけ、panic の値とスタックを標準エラーに出す）。
//
// # Node での確かめのモード
//
//	node "$(go -C server env GOROOT)/lib/wasm/wasm_exec_node.js" raft-replay.wasm --input FILE
//
// 引数があるとき（len(os.Args) > 1）は、FILE を読んで再生し、成功なら "traceHash=<traceHash> events=<数>" を
// 標準出力に 1 行出して終了コード 0、失敗なら "code=<code> message=<message>" を標準エラーに出して 1。
// ブラウザ（os.Args が ["js"]）では、このモードに入らない。scripts/build-replay-wasm.sh --verify が、
// この出力の traceHash をネイティブの simcli replay と比べる。
package main
