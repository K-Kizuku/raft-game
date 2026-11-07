// Package bot はボット船長（DesignDoc §14）。プレイヤーの代わりに sim.Engine の船を動かす自動プレイヤー。
//
//   - random: ランダムに積んで出航する。多数のシードで不変条件を検査するテストに使う
//   - greedy: 優先度の高い手紙が多い島へ向かう。乱数を使わない。ステージの難易度の確認に使う
//   - steady: greedy と同じ動き方で、手紙の優先度を状況で切り替える（T-501c）。代表が健在なあいだは定期便を
//     投票の手紙より先に運び、代表がいない・孤立したときは greedy と同じ優先度にする。嵐の向こう側へ行く手紙は積まず、
//     積み荷がすべて嵐の向こう側へ行く手紙なら、空きがあれば迎えに行く。乱数を使わない
//
// ボットは Engine を、プレイヤーと同じ方法でだけ動かす。状態を読むのは Phase・Now・Snapshot・NextNotableAt だけで、
// イベントは見ない（DrainEvents を呼ばない）。状態を変えるのは Apply（cmd.game.start・cmd.ferry.*）と RunUntil だけ。
// そのため、ボットの操作はすべて入力ログに入り、入力ログだけで同じ結果を再現できる（§10.1）。
//
// ボットの操作は入力ログを通してゲームの結果に効くので、sim と同じ決定性の規則（§9.2）に従う。
// 乱数は sim.Sub(seed, "bot/"+name) から取る（§9.1）。
//
// 使い方:
//
//	b, err := bot.New(name, seed)
//	e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: true})
//	err = bot.Play(e, b)
//	events := e.DrainEvents() // ラウンドの全部のイベント
package bot
