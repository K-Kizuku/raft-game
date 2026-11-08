// Package ferryactor は 1 隻 = 1 Actor の Ferry Actor（DTN-04 #161、architecture.md §1・§2）。
//
// Actor は船 1 隻の状態（Docked・Sailing）と、Cargo の実データ（Bundle の bytes）だけを持つ。
// 航路の表・嵐・他の船・島の状態は持たない。どこへいつ着くか・何を積むかは Coordinator が決め、
// 船は要求（actor-protocol.md §3.1 の arrive・depart・cargo.add・cargo.remove・bundle.export・inspect）を
// 確かめて状態を変える。検査に 1 つでも落ちた要求は、何も変えずに invalid で断る（全部か無しか）。
//
// このパッケージは決定的（runtime.Actor の約束）: 実時間・goroutine・チャネル・グローバルな乱数・
// 浮動小数点・map の走査順に依存しない（forbidigo の対象）。I/O は Runtime（dtn/runtime）が持つ。
package ferryactor
