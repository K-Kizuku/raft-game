// Package islandcore は 1 島の核（IslandCore、DTN-00 #363）。参照モデル（sim.Engine）と、分散版の島の Actor
// （DTN-03）が同じ部品として使う。
//
// Core が持つのは 1 島の状態だけ: 1 つの cluster.Node・台帳（ledger）・港と中継倉庫（ferry.Port。規則 R1〜R6）・
// 依頼の窓口（route・game.Decide）と待っている依頼の再試行・依頼の再送のタイマー・島のイベント
// （RaftTick・RequestSpawn・RequestRetry）の待ち行列。船・航路・ほかの島・スコア・依頼の表示の状態は持たない
// （CoordinatorCore と sim.Engine が持つ）。
//
// 入力は普通の Go のメソッド（AdvanceTo・Deliver・Unload・Take・TakeRelay・MarkResolved など）で、出力は
// (T, Class, Seq) 付きの Output の列（Drain で取り出す）。Seq は島の中の連番で、出力を作った順に振る。
// 分散版の Coordinator は、全島の出力を (T, Class, 島の ID, Seq) の順に並べて処理する
// （docs/dtn/simulation-time.md §3）。
//
// 時刻の進め方（docs/dtn/simulation-time.md §2.1）: AdvanceTo(Mark{T, Class}) は、待ち行列のイベントのうち
// (At, Class) < (T, Class) のものを (At, Class, Seq) の順にすべて処理し、イベントごとに Process と node.state の
// 差分の出力を行う。上限の区切り方によらず、状態と出力は同じになる（D-9。core_test.go で確かめる）。
//
// 参照モデルは、今の sim.Engine と同じ順序（全ノードの Tick → 全島の待ちの再試行 → 全ノードの Process →
// 全ノードの node.state）で呼ぶため、AdvanceTo の部品（Pop・HandleTick・TakeWaits・RetryWait・Handle・Process・
// ReportState）も公開する。分散版は AdvanceTo だけを使う。
//
// 決定性: time・goroutine・チャネル・グローバルな乱数・浮動小数点を使わず、map は検索だけに使う。
// 1 つの goroutine から使う。誤った使い方（Raft・台帳・港の内部の誤り）は panic する。
package islandcore
