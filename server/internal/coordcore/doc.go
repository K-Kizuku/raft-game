// Package coordcore は Coordinator の核（CoordinatorCore、DTN-00 #363）。参照モデル（sim.Engine）と、分散版の
// Coordinator（DTN-05）が同じ部品として使う。
//
// 持つもの: 仮想時計とスケジューラ（Coordinator のイベント: 船の到着・嵐・依頼の期限・沈黙の確認・ラウンドの終了）・
// 航路の表と船の状態・時刻表・すべての手紙のメタデータの写し（所在と Term。ferry.Fleet）・古い手紙の検出
// （StaleLetters）・自動の積み込みの選択（SelectAutoload）。島の状態（ノード・台帳・港の中身・依頼の窓口）は
// 持たない（islandcore が持つ）。
//
// DTN-00 の時点では、依頼の帳簿の表示（request.*）・スコア・統計・嵐などの台本のイベントのドメインイベントへの
// 変換は sim.Engine に残る（ドメインイベントの型が sim にあるため）。sim.Engine は、島の出力をこのパッケージの
// 部品と組み合わせてドメインイベントにする（docs/dtn/architecture.md §2）。
//
// 決定性: time・goroutine・チャネル・グローバルな乱数・浮動小数点を使わず、map は検索だけに使う。
// 1 つの goroutine から使う。
package coordcore
