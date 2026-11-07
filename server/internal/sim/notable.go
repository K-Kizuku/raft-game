package sim

import "github.com/K-Kizuku/raft-game/server/internal/ferry"

// NextNotableAt は、次に「プレイヤーに見せたい出来事」が起きる時刻を返す（DesignDoc §4.3、プロトコル §4 の cmd.game.skip）。
// 今は船の到着と依頼の発生（まだ発生していない最初の依頼の時刻）。台本の出来事は T-204 が足す。
// 航行中でないとき・該当がないときは (0, false)。状態を変えない。
//
// DurationMs より後に着く船は、ラウンドが先に終わって処理されないので含めない（ちょうどに着く船は含める）。
// 依頼は New で DurationMs 以下の時刻のものだけを作るので、範囲を確かめない。
// 停泊時間の終わり（FerryReady）・RaftTick・ラウンドの終わりは含めない（設計書にない判断。出来事の一覧にないため）。
// スケジューラは見ずに、ferry の状態から求める。
func (e *Engine) NextNotableAt() (SimTime, bool) {
	if e.phase != PhaseSailing {
		return 0, false
	}
	var best SimTime
	found := false
	for _, f := range e.tr.Ferries() {
		if f.State != ferry.Sailing {
			continue
		}
		t := SimTime(f.ArriveAt)
		if t > e.cfg.DurationMs {
			continue
		}
		if !found || t < best {
			best, found = t, true
		}
	}
	if t, ok := e.nextRequestAt(); ok && (!found || t < best) {
		best, found = t, true
	}
	return best, found
}
