package sim

import (
	"bytes"
	"fmt"
	"reflect"

	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// invariantState は、テストモードの検査が前回までに観測した値（DesignDoc §11）。
// e.inv に持ち、最初の checkInvariants で作る（テストモードでなければ nil のまま）。
type invariantState struct {
	seq     uint64            // 前回の検査までに見たドメインイベントの Seq（e.seq の値）
	leaders map[uint64]uint64 // Term → その Term で観測した Leader の ID。検索だけに使い、走査しない
	prev    []progress        // 前回の検査で見た値。添字 = ID - 1
}

// progress は I-5 で比べる、1 ノードの値。
type progress struct{ term, commit, applied uint64 }

// checkInvariants はテストモードの検査（§11 の I-1〜I-7）。違反したら、最初の 1 つで panic する。
// 1 ステップの後処理の最後（Reclaim の後）に呼ぶ。Raft・台帳・港・手紙の写し・イベントを読むだけで、何も変えない
// （e.inv だけを更新する）。
func (e *Engine) checkInvariants() {
	n := len(e.nodes)
	if e.inv == nil {
		e.inv = &invariantState{
			leaders: make(map[uint64]uint64),
			prev:    make([]progress, n),
		}
	}
	sts := make([]cluster.Status, n)
	logs := make([][]cluster.LogEntry, n)
	applied := make([]uint64, n)
	commit := make([]uint64, n)
	cur := make([]progress, n)
	for i, node := range e.nodes {
		sts[i] = node.Status()
		l, err := node.Log()
		if err != nil {
			panic(fmt.Sprintf("sim: node %d: log: %v", i+1, err))
		}
		logs[i] = l
		applied[i] = sts[i].Applied
		commit[i] = sts[i].Commit
		cur[i] = progress{term: sts[i].Term, commit: sts[i].Commit, applied: sts[i].Applied}
	}
	if err := e.findViolation(sts, logs, applied, commit, cur); err != nil {
		panic(fmt.Sprintf("sim: invariant violated at t=%d: %v", e.Now(), err))
	}
	e.inv.seq = e.seq
	e.inv.prev = cur
}

// findViolation は I-1〜I-7 をこの順に検査し、最初の違反を返す。
func (e *Engine) findViolation(sts []cluster.Status, logs [][]cluster.LogEntry, applied, commit []uint64, cur []progress) error {
	// I-1: 前回の検査より後に出た node.leader_elected と、今の Status の Leader。
	start := max(0, len(e.pending)-int(e.seq-e.inv.seq))
	for _, ev := range e.pending[start:] {
		if d, ok := ev.Data.(LeaderElected); ok {
			if err := e.inv.observeLeader(d.ID, d.Term); err != nil {
				return err
			}
		}
	}
	for _, st := range sts {
		if st.Role == raft.StateLeader {
			if err := e.inv.observeLeader(st.ID, st.Term); err != nil {
				return err
			}
		}
	}
	if err := checkLogMatching(logs); err != nil {
		return err
	}
	if err := checkAppliedAgreement(logs, applied); err != nil {
		return err
	}
	if err := checkCommitQuorum(logs, commit); err != nil {
		return err
	}
	if err := checkProgress(e.inv.prev, cur); err != nil {
		return err
	}
	if err := checkLedgers(e.ledgers, applied); err != nil {
		return err
	}
	if err := e.tr.CheckInvariants(); err != nil {
		return fmt.Errorf("I-7: %w", err)
	}
	for _, isl := range e.islands {
		if err := isl.Port().CheckInvariants(); err != nil {
			return fmt.Errorf("I-7: %w", err)
		}
		if err := ferry.CheckPortMirror(e.tr, isl.Port()); err != nil {
			return fmt.Errorf("I-7: %w", err)
		}
	}
	return nil
}

// observeLeader は、Term term で id が Leader だったことを記録する（I-1: Election Safety）。
// 同じ Term で別の Leader を観測済みならエラーを返す。
func (s *invariantState) observeLeader(id, term uint64) error {
	if prev, ok := s.leaders[term]; ok && prev != id {
		return fmt.Errorf("I-1: term %d has two leaders: %d and %d", term, prev, id)
	}
	s.leaders[term] = id
	return nil
}

// sameEntry は 2 つのエントリの Index・Term・Type・Data が等しいかを返す。
func sameEntry(a, b cluster.LogEntry) bool {
	return a.Index == b.Index && a.Term == b.Term && a.Type == b.Type && bytes.Equal(a.Data, b.Data)
}

// checkLogMatching は I-2（Log Matching）を検査する。logs の添字 = ID - 1。
// 2 つのログが index k で初めて違ったら、k 以降に Term の等しい index があってはならない。
func checkLogMatching(logs [][]cluster.LogEntry) error {
	for i, l := range logs {
		for p, ent := range l {
			if ent.Index != uint64(p+1) {
				return fmt.Errorf("I-2: island %d: entry at position %d has index %d", i+1, p, ent.Index)
			}
		}
	}
	for a := range logs {
		for b := a + 1; b < len(logs); b++ {
			m := min(len(logs[a]), len(logs[b]))
			k := -1 // 最初に違った位置（0 から）
			for p := range m {
				if k < 0 {
					if sameEntry(logs[a][p], logs[b][p]) {
						continue
					}
					k = p
				}
				if logs[a][p].Term == logs[b][p].Term {
					return fmt.Errorf("I-2: islands %d and %d: entries at index %d have term %d but the logs differ at index %d",
						a+1, b+1, p+1, logs[a][p].Term, k+1)
				}
			}
		}
	}
	return nil
}

// checkAppliedAgreement は I-3（適用済みのエントリの一致、V-2）を検査する。applied の添字 = ID - 1。
func checkAppliedAgreement(logs [][]cluster.LogEntry, applied []uint64) error {
	for i, l := range logs {
		if applied[i] > uint64(len(l)) {
			return fmt.Errorf("I-3: island %d: applied %d exceeds last index %d", i+1, applied[i], len(l))
		}
	}
	for a := range logs {
		for b := a + 1; b < len(logs); b++ {
			m := min(applied[a], applied[b])
			for idx := uint64(1); idx <= m; idx++ {
				if !sameEntry(logs[a][idx-1], logs[b][idx-1]) {
					return fmt.Errorf("I-3: islands %d and %d: applied entries differ at index %d", a+1, b+1, idx)
				}
			}
		}
	}
	return nil
}

// checkCommitQuorum は I-4（Commit されたエントリが過半数のストレージにある、V-1）を検査する。
// Commit 済みのすべての index を毎回検査する。commit の添字 = ID - 1。全員が投票者とする。
func checkCommitQuorum(logs [][]cluster.LogEntry, commit []uint64) error {
	for i, l := range logs {
		if commit[i] > uint64(len(l)) {
			return fmt.Errorf("I-4: island %d: commit %d exceeds last index %d", i+1, commit[i], len(l))
		}
	}
	q := len(logs)/2 + 1
	for x, lx := range logs {
		for idx := uint64(1); idx <= commit[x]; idx++ {
			ent := lx[idx-1]
			count := 0
			for _, ly := range logs {
				if uint64(len(ly)) >= idx && sameEntry(ly[idx-1], ent) {
					count++
				}
			}
			if count < q {
				return fmt.Errorf("I-4: island %d: committed entry %d (term %d) is stored on %d islands, need %d",
					x+1, idx, ent.Term, count, q)
			}
		}
	}
	return nil
}

// checkProgress は I-5（Term・Commit・Applied が減らない）と、補助の条件 Applied ≤ Commit を検査する。
// prev・cur の添字 = ID - 1。
func checkProgress(prev, cur []progress) error {
	for i, c := range cur {
		p := prev[i]
		for _, f := range []struct {
			name     string
			old, now uint64
		}{
			{"term", p.term, c.term},
			{"commit", p.commit, c.commit},
			{"applied", p.applied, c.applied},
		} {
			if f.now < f.old {
				return fmt.Errorf("I-5: island %d: %s decreased from %d to %d", i+1, f.name, f.old, f.now)
			}
		}
		if c.applied > c.commit {
			return fmt.Errorf("I-5: island %d: applied %d exceeds commit %d", i+1, c.applied, c.commit)
		}
	}
	return nil
}

// checkLedgers は I-6（台帳の不変条件、§7.3・V-3）を検査する。ls・applied の添字 = ID - 1。
// 各台帳の CheckInvariants、台帳の AppliedIndex ≤ ノードの Applied、
// AppliedIndex が等しい 2 つの台帳の Snapshot の一致を、この順に見る。
func checkLedgers(ls []*ledger.Ledger, applied []uint64) error {
	for i, l := range ls {
		if err := l.CheckInvariants(); err != nil {
			return fmt.Errorf("I-6: island %d: %w", i+1, err)
		}
	}
	for i, l := range ls {
		if got := l.AppliedIndex(); got > applied[i] {
			return fmt.Errorf("I-6: island %d: ledger applied index %d exceeds node applied %d", i+1, got, applied[i])
		}
	}
	for a := range ls {
		for b := a + 1; b < len(ls); b++ {
			ia := ls[a].AppliedIndex()
			if ia != ls[b].AppliedIndex() {
				continue
			}
			if !reflect.DeepEqual(ls[a].Snapshot(), ls[b].Snapshot()) {
				return fmt.Errorf("I-6: islands %d and %d: ledgers differ at applied index %d", a+1, b+1, ia)
			}
		}
	}
	return nil
}
