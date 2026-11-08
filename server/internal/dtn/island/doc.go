// Package island は 1 島 = 1 Raft ノードの Island Actor（DTN-03 #160、architecture.md §1・§2）。
//
// Actor は islandcore.Core を 1 つだけ包む（1 つの cluster.Node・台帳・港・中継倉庫・依頼の窓口・島のイベントの
// 待ち行列）。ほかの島のノード・船・航路は持たない。init で開幕式を手元で計算して自島のノードだけを残し
// （simulation-time.md §4）、actor-protocol.md §3.1 の島の要求（advance・deliver・port.remove・unload・
// relay.remove・requests.note・bundle.export・inspect）を処理する。手紙は Bundle の bytes（bpv7）として作り、
// 受け取る。検査に 1 つでも落ちた要求は、何も変えずに invalid で断る（全部か無しか）。
//
// 出力の data は、参照モデル（sim.Engine）の同じドメインイベントの中身に、actor-protocol.md §4 の欄を足したもの。
// 出力の (t, class, seq) は IslandCore が振る（seq は島の中の連番）。
//
// このパッケージは決定的（runtime.Actor の約束）: 実時間・goroutine・チャネル・グローバルな乱数・
// 浮動小数点・map の走査順に依存しない（forbidigo の対象）。I/O は Runtime（dtn/runtime）が持つ。
package island
