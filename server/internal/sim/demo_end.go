package sim

import "github.com/K-Kizuku/raft-game/server/internal/game"

// ★2 でラウンドを終える（ステージ定義の demo.endOnAllCriticalSecured。T-305b。データ仕様 §5.4）。
//
// ★2 の条件を満たしたステップの後処理（refreshScore の中）で、ラウンドの終わりのイベント（kindRoundEnd、
// ClassGameTimer）を今の時刻に 1 回だけスケジューラに積む。終わりの処理（finishScore の公平ボーナス →
// phase.changed{ended}）と RunUntil の打ち切りは、DurationMs のときと同じく handleEvent・RunUntil が行う。
// 終わる時刻はスケジューラのイベントの順だけで決まるので、入力ログからの再生（Replay）でも同じ時刻に終わる。
//
// 同じ時刻 T のほかのイベントのうち、クラスの順で先のもの（同じ時刻の船の到着・RaftTick など）と、先に積まれた
// 同じクラスのもの（同じ時刻の期限切れ・音信不通）を処理してから終わる。T == DurationMs なら、先に積まれた
// 元の終わりのイベント（Seq が小さい）で終わり、ここで積んだものは処理されない。
//
// Apply のコマンドは Raft のノードに手紙を渡さず、前のステップの後処理で適用はすべて済んでいるので、
// Apply の後処理の refreshScore で新しく精算されることはない。したがって終わりは必ず RunUntil の中で積まれて
// 処理される（queueStar2End は呼ばれた場所を見分けない。demo_end_test.go の DE-7 で確かめる）。

// queueStar2End は、★2 で終えるステージ（Config.Score.EndOnStar2）で ★2 の条件（game.ReachedStar2）を満たしたら、
// ラウンドの終わり（kindRoundEnd）を今の時刻に 1 回だけ積む（refreshScore の中、依頼の精算の直後）。
// 採点しない・EndOnStar2 が偽・航行中でない・積んだ後（または終わりの精算を始めた後）は何もしない。
// 乱数を引かず、イベントを出さない（スケジューラに積むだけ）。
func (e *Engine) queueStar2End() {
	if !e.score.on || !e.cfg.Score.EndOnStar2 || e.score.endQueued || e.phase != PhaseSailing {
		return
	}
	if !e.star2Reached() {
		return
	}
	e.score.endQueued = true
	e.push(e.Now(), ClassGameTimer, kindRoundEnd, nil)
}

// star2Reached は、今の確定数と確定した重要な資源で ★2 の条件を満たすかを返す。
// 重要な資源が 1 つもないステージは「すべて確定」とみなす（Result と同じ）。
func (e *Engine) star2Reached() bool {
	all := true
	for _, s := range e.score.secured {
		if !s {
			all = false
			break
		}
	}
	return game.ReachedStar2(e.cfg.Score, e.score.successes, all)
}
