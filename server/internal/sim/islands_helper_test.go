package sim

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
)

// postRequestForTest は、依頼書を from の港に直接置く（窓口を通さない。テスト用）。ドメインイベントは出さず、
// Coordinator の写し（e.tr）にだけ反映する（DTN-00 より前の e.tr.PostRequest と同じ）。
func (e *Engine) postRequestForTest(now int64, from, to uint64, note ferry.RequestNote) (ferry.LetterID, []ferry.Drop, error) {
	if from < 1 || from > uint64(len(e.islands)) {
		return 0, nil, fmt.Errorf("%w: from %d", ferry.ErrUnknownIsland, from)
	}
	isl := e.islands[from-1]
	l, drops, err := isl.PostRequest(now, to, note)
	if err != nil {
		return 0, nil, err
	}
	for _, o := range isl.Drain() {
		switch d := o.Data.(type) {
		case islandcore.LetterCreatedOut:
			if err := e.tr.Track(d.Letter); err != nil {
				panic(err)
			}
		case islandcore.LetterDroppedOut:
			if err := e.tr.MarkDropped(d.LetterID); err != nil {
				panic(err)
			}
		}
	}
	return l.ID, drops, nil
}

// nextEventAt は、Coordinator のスケジューラと全島の待ち行列のうち、いちばん早いイベントの時刻を返す。
func (e *Engine) nextEventAt() (SimTime, bool) {
	at, ok := SimTime(0), false
	if ev, o := e.coord.Sched.Peek(); o {
		at, ok = ev.At, true
	}
	for _, isl := range e.islands {
		if ev, o := isl.Peek(); o && (!ok || SimTime(ev.At) < at) {
			at, ok = SimTime(ev.At), true
		}
	}
	return at, ok
}

// staleTerms は送信元ごとの古い手紙の基準の Term（coordcore.Core.StaleTerm）を ID の順に返す。
func (e *Engine) staleTerms() []uint64 {
	out := make([]uint64, len(e.islands))
	for i := range out {
		out[i] = e.coord.StaleTerm(uint64(i + 1))
	}
	return out
}
