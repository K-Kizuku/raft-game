package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// EvLetterStale は letter.stale の種類。
const EvLetterStale EventType = "letter.stale"

// LetterStale は letter.stale（プロトコル §6）。送信元の Term が上がり、手紙が古くなった。
type LetterStale struct {
	LetterID uint64 `json:"letterId"`
}

func (LetterStale) EventType() EventType { return EvLetterStale }

// emitStale は、id のノードの Term が staleTerm[id-1] より上がっていれば、id が送信元で港と船にある
// Raft の手紙のうち staleTerm[id-1] <= Term < 今の Term のものについて、ID の昇順に letter.stale を出す。
// そのあと staleTerm[id-1] を今の Term にする。handleOutput の中だけで呼ぶ。
//
// staleTerm[id-1] は、送信元 id の手紙を「古い」と判定した基準の Term。港と船にある id が送信元の
// Raft の手紙 L は、L.Term < staleTerm[id-1] のときだけ、古いと知らせ済み（letter.created の
// raft.stale == true、または letter.stale を出した）。Term は減らず、手紙の ID は再利用しないので、
// 各手紙は 1 回だけ古いと知らされる。
func (e *Engine) emitStale(id uint64) {
	old := e.staleTerm[id-1]
	cur := e.nodes[id-1].Status().Term
	if cur <= old {
		return
	}
	ids := e.tr.Port(id)
	for _, f := range e.tr.Ferries() {
		for _, lid := range f.Cargo {
			l, ok := e.tr.Letter(lid)
			if !ok {
				panic(fmt.Sprintf("sim: letter %d on ferry %q not found", lid, f.ID))
			}
			if l.From == id {
				ids = append(ids, lid)
			}
		}
	}
	slices.Sort(ids)
	for _, lid := range ids {
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: stale: letter %d not found", lid))
		}
		if l.Kind != ferry.LetterRaft {
			continue
		}
		if term := l.Raft.GetTerm(); old <= term && term < cur {
			e.emit(LetterStale{LetterID: uint64(lid)})
		}
	}
	e.staleTerm[id-1] = cur
}
