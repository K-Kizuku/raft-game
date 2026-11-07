package sim

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// invMustPanic は f が panic し、recover した値の文字列が substr を含むことを確かめる。
func invMustPanic(t *testing.T, substr string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want %q", substr)
		}
		if s := fmt.Sprint(r); !strings.Contains(s, substr) {
			t.Fatalf("panic %q, want it to contain %q", s, substr)
		}
	}()
	f()
}

// invLog は terms の順にエントリを並べたログを作る。index は 1 から、Data は "i<index>-t<term>"。
func invLog(terms ...uint64) []cluster.LogEntry {
	out := make([]cluster.LogEntry, len(terms))
	for i, term := range terms {
		idx := uint64(i + 1)
		out[i] = cluster.LogEntry{Index: idx, Term: term, Type: pb.EntryNormal, Data: fmt.Appendf(nil, "i%d-t%d", idx, term)}
	}
	return out
}

// invWithData は l の複製のうち、index の Data だけを data にしたものを返す。
func invWithData(l []cluster.LogEntry, index uint64, data string) []cluster.LogEntry {
	out := make([]cluster.LogEntry, len(l))
	copy(out, l)
	out[index-1].Data = []byte(data)
	return out
}

// invCheckErr は err が want（"" なら nil）を満たすかを確かめる。
func invCheckErr(t *testing.T, err error, want ...string) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("err = nil, want %q", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("err = %q, want it to contain %q", err, w)
		}
	}
}

func TestObserveLeader(t *testing.T) { // IV-1
	t.Parallel()
	type obs struct{ id, term uint64 }
	tests := []struct {
		name string
		obs  []obs
		want []string
	}{
		{"same leader twice", []obs{{1, 2}, {1, 2}}, nil},
		{"new term new leader", []obs{{1, 2}, {2, 3}}, nil},
		{"two leaders in a term", []obs{{1, 2}, {3, 2}}, []string{"I-1: term 2 has two leaders: 1 and 3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &invariantState{leaders: make(map[uint64]uint64)}
			for i, o := range tt.obs {
				err := s.observeLeader(o.id, o.term)
				if i < len(tt.obs)-1 {
					invCheckErr(t, err)
					continue
				}
				invCheckErr(t, err, tt.want...)
			}
		})
	}
}

func TestCheckLogMatching(t *testing.T) { // IV-2
	t.Parallel()
	skip := []cluster.LogEntry{invLog(1)[0], {Index: 3, Term: 1, Type: pb.EntryNormal}}
	tests := []struct {
		name string
		logs [][]cluster.LogEntry
		want []string
	}{
		{"same", [][]cluster.LogEntry{invLog(1, 1, 1, 2), invLog(1, 1, 1, 2), invLog(1, 1, 1, 2)}, nil},
		{"shorter", [][]cluster.LogEntry{invLog(1, 1, 1, 2), invLog(1, 1)}, nil},
		{"diverge at 3", [][]cluster.LogEntry{invLog(1, 1, 2, 2), invLog(1, 1, 3, 3)}, nil},
		{"same term after divergence", [][]cluster.LogEntry{invLog(1, 2, 3), invLog(1, 3, 3)}, []string{"I-2", "index 3"}},
		{"same term different data", [][]cluster.LogEntry{invLog(1, 1, 2), invWithData(invLog(1, 1, 2), 3, "other")}, []string{"I-2", "index 3"}},
		{"index gap", [][]cluster.LogEntry{skip}, []string{"I-2", "position 1"}},
		{"single node", [][]cluster.LogEntry{invLog(1, 2)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			invCheckErr(t, checkLogMatching(tt.logs), tt.want...)
		})
	}
}

func TestCheckAppliedAgreement(t *testing.T) { // IV-3
	t.Parallel()
	base := invLog(1, 1, 2, 2)
	other := invWithData(base, 3, "other")
	tests := []struct {
		name    string
		logs    [][]cluster.LogEntry
		applied []uint64
		want    []string
	}{
		{"same", [][]cluster.LogEntry{base, base, base}, []uint64{4, 4, 2}, nil},
		{"differ in range", [][]cluster.LogEntry{base, other}, []uint64{3, 3}, []string{"I-3", "index 3"}},
		{"differ out of range", [][]cluster.LogEntry{base, other}, []uint64{2, 3}, nil},
		{"applied beyond log", [][]cluster.LogEntry{base, base}, []uint64{4, 5}, []string{"I-3: island 2: applied 5 exceeds last index 4"}},
		{"single node", [][]cluster.LogEntry{base}, []uint64{4}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			invCheckErr(t, checkAppliedAgreement(tt.logs, tt.applied), tt.want...)
		})
	}
}

func TestCheckCommitQuorum(t *testing.T) { // IV-4
	t.Parallel()
	tests := []struct {
		name   string
		logs   [][]cluster.LogEntry
		commit []uint64
		want   []string
	}{
		{"majority", [][]cluster.LogEntry{invLog(1, 1, 2), invLog(1, 1, 2), invLog(1, 1)}, []uint64{3, 3, 2}, nil},
		{"only leader", [][]cluster.LogEntry{invLog(1, 1, 2), invLog(1, 1), invLog(1, 1)}, []uint64{3, 2, 2},
			[]string{"I-4: island 1: committed entry 3 (term 2) is stored on 1 islands, need 2"}},
		{"different term not counted", [][]cluster.LogEntry{invLog(1, 1, 2), invLog(1, 1, 3), invLog(1, 1)}, []uint64{3, 2, 2},
			[]string{"I-4", "stored on 1 islands, need 2"}},
		{"commit beyond log", [][]cluster.LogEntry{invLog(1, 1), invLog(1, 1), invLog(1, 1)}, []uint64{2, 3, 2},
			[]string{"I-4: island 2: commit 3 exceeds last index 2"}},
		{"five nodes three have it", [][]cluster.LogEntry{invLog(1, 1, 2), invLog(1, 1, 2), invLog(1, 1, 2), invLog(1, 1), invLog(1, 1)},
			[]uint64{3, 2, 2, 2, 2}, nil},
		{"five nodes two have it", [][]cluster.LogEntry{invLog(1, 1, 2), invLog(1, 1, 2), invLog(1, 1), invLog(1, 1), invLog(1, 1)},
			[]uint64{3, 2, 2, 2, 2}, []string{"I-4", "stored on 2 islands, need 3"}},
		{"single node", [][]cluster.LogEntry{invLog(1, 2)}, []uint64{2}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			invCheckErr(t, checkCommitQuorum(tt.logs, tt.commit), tt.want...)
		})
	}
}

func TestCheckProgress(t *testing.T) { // IV-5
	t.Parallel()
	prev := []progress{{term: 2, commit: 4, applied: 4}, {term: 2, commit: 4, applied: 3}}
	tests := []struct {
		name string
		cur  []progress
		want []string
	}{
		{"equal", prev, nil},
		{"increase", []progress{{3, 5, 5}, {2, 6, 4}}, nil},
		{"term decreased", []progress{{2, 4, 4}, {1, 4, 3}}, []string{"I-5: island 2: term decreased from 2 to 1"}},
		{"commit decreased", []progress{{2, 3, 3}, {2, 4, 3}}, []string{"I-5: island 1: commit decreased from 4 to 3"}},
		{"applied decreased", []progress{{2, 4, 4}, {2, 4, 2}}, []string{"I-5: island 2: applied decreased from 3 to 2"}},
		{"applied exceeds commit", []progress{{2, 4, 5}, {2, 4, 3}}, []string{"I-5: island 1: applied 5 exceeds commit 4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			invCheckErr(t, checkProgress(prev, tt.cur), tt.want...)
		})
	}
	t.Run("zero prev", func(t *testing.T) {
		t.Parallel()
		invCheckErr(t, checkProgress(make([]progress, 2), prev))
	})
}

// invLedgers は testConfig の資源で台帳を n 個作る。
func invLedgers(t *testing.T, n int) []*ledger.Ledger {
	t.Helper()
	out := make([]*ledger.Ledger, n)
	for i := range out {
		l, err := ledger.New(testConfig().Resources)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = l
	}
	return out
}

// invEncode は ledger.Encode の結果を返す。
func invEncode(t *testing.T, c ledger.Command) []byte {
	t.Helper()
	data, err := ledger.Encode(c)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

func invStall(reqID string) ledger.Command {
	return ledger.Command{V: ledger.Version, ReqID: reqID, Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main"}
}

func invFirework(reqID string) ledger.Command {
	return ledger.Command{V: ledger.Version, ReqID: reqID, Origin: 1, Kind: ledger.KindFireworkReserve, Resource: "firework:a"}
}

func TestCheckLedgers(t *testing.T) { // IV-6
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, ls []*ledger.Ledger)
		applied []uint64
		want    []string
	}{
		{"fresh", nil, []uint64{4, 4, 4}, nil},
		{"same command", func(t *testing.T, ls []*ledger.Ledger) {
			ls[0].Apply(3, invEncode(t, invStall("s-1")))
			ls[1].Apply(3, invEncode(t, invStall("s-1")))
		}, []uint64{4, 4, 4}, nil},
		{"different command", func(t *testing.T, ls []*ledger.Ledger) {
			ls[1].Apply(3, invEncode(t, invStall("s-1")))
			ls[2].Apply(3, invEncode(t, invFirework("f-1")))
		}, []uint64{4, 4, 4}, []string{"I-6: islands 2 and 3: ledgers differ at applied index 3"}},
		{"different applied index", func(t *testing.T, ls []*ledger.Ledger) {
			ls[1].Apply(3, invEncode(t, invStall("s-1")))
		}, []uint64{4, 4, 4}, nil},
		{"ledger ahead of node", func(t *testing.T, ls []*ledger.Ledger) {
			ls[0].Apply(5, invEncode(t, invStall("s-1")))
		}, []uint64{4, 4, 4}, []string{"I-6: island 1: ledger applied index 5 exceeds node applied 4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := invLedgers(t, 3)
			if tt.setup != nil {
				tt.setup(t, ls)
			}
			invCheckErr(t, checkLedgers(ls, tt.applied), tt.want...)
		})
	}
}

// invBreakLedgers は台帳 2・3 に、同じ index 3 で違うコマンドを適用する（I-6 の違反）。
func invBreakLedgers(t *testing.T, e *Engine) {
	t.Helper()
	e.ledgers[1].Apply(3, invEncode(t, invStall("s-1")))
	e.ledgers[2].Apply(3, invEncode(t, invFirework("f-1")))
}

func TestCheckInvariantsPanics(t *testing.T) { // IV-7
	t.Parallel()
	tests := []struct {
		name    string
		corrupt func(t *testing.T, e *Engine)
		want    string
	}{
		{"recorded leader", func(t *testing.T, e *Engine) { e.inv.leaders[2] = 3 },
			"sim: invariant violated at t=0: I-1: term 2 has two leaders: 3 and 1"},
		{"leader event", func(t *testing.T, e *Engine) { e.emit(LeaderElected{ID: 3, Term: 2}) },
			"sim: invariant violated at t=0: I-1: term 2 has two leaders: 1 and 3"},
		{"commit decreased", func(t *testing.T, e *Engine) { e.inv.prev[0].commit = 100 },
			"sim: invariant violated at t=0: I-5: island 1: commit decreased"},
		{"ledgers differ", invBreakLedgers,
			"sim: invariant violated at t=0: I-6: islands 2 and 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := startedEngine(t)
			if e.inv == nil {
				t.Fatal("e.inv is nil after cmd.game.start in test mode")
			}
			tt.corrupt(t, e)
			invMustPanic(t, tt.want, e.checkInvariants)
		})
	}
}

// invObserved は IV-8 で、検査の前後で比べる状態。
type invObserved struct {
	hash    [32]byte
	status  []cluster.Status
	logs    [][]cluster.LogEntry
	ledgers []ledger.Snapshot
	ports   [][]ferry.LetterID
	ferries []ferry.Ferry
}

func invObserve(t *testing.T, e *Engine) invObserved {
	t.Helper()
	o := invObserved{hash: e.TraceHash(), ferries: e.tr.Ferries()}
	for i, n := range e.nodes {
		o.status = append(o.status, n.Status())
		l, err := n.Log()
		if err != nil {
			t.Fatal(err)
		}
		o.logs = append(o.logs, l)
		o.ledgers = append(o.ledgers, e.ledgers[i].Snapshot())
		o.ports = append(o.ports, e.tr.Port(uint64(i+1)))
	}
	return o
}

func TestCheckInvariantsReadOnly(t *testing.T) { // IV-8
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(10000)
	e.DrainEvents()
	before := invObserve(t, e)

	e.checkInvariants()
	prev, seq := append([]progress(nil), e.inv.prev...), e.inv.seq
	e.checkInvariants()
	e.checkInvariants()

	if after := invObserve(t, e); !reflect.DeepEqual(before, after) {
		t.Errorf("checkInvariants changed the state:\n before %+v\n after  %+v", before, after)
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("checkInvariants emitted events: %+v", evs)
	}
	if !reflect.DeepEqual(e.inv.prev, prev) || e.inv.seq != seq {
		t.Errorf("e.inv changed: prev %+v → %+v, seq %d → %d", prev, e.inv.prev, seq, e.inv.seq)
	}
}

func TestNoCheckWithoutTestMode(t *testing.T) { // IV-9
	t.Parallel()
	e := startedEngineWith(t, testConfig(), Options{})
	e.RunUntil(10000)
	if e.inv != nil {
		t.Fatalf("e.inv = %+v without test mode, want nil", e.inv)
	}
	invBreakLedgers(t, e)
	e.RunUntil(20000)
	if e.inv != nil {
		t.Errorf("e.inv = %+v without test mode, want nil", e.inv)
	}
}

// invConfig3 は testConfig に航路 1-3 と船 f2 を足した 3 島の設定。
func invConfig3() Config {
	cfg := testConfig()
	cfg.Routes = append(cfg.Routes, ferry.RouteConfig{A: 1, B: 3, TravelMs: 6000})
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 3, SpeedPermille: 1200})
	return cfg
}

// invConfig5 は 5 島の環状の設定（ほかの値は testConfig のまま）。
func invConfig5() Config {
	cfg := testConfig()
	cfg.Islands = 5
	cfg.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 3000},
		{A: 2, B: 3, TravelMs: 4000},
		{A: 3, B: 4, TravelMs: 3500},
		{A: 4, B: 5, TravelMs: 4500},
		{A: 1, B: 5, TravelMs: 5000},
	}
	cfg.Ferries = []ferry.FerryConfig{
		{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		{ID: "f2", Home: 3, Capacity: 4, SpeedPermille: 1000},
		{ID: "f3", Home: 5, Capacity: 3, SpeedPermille: 800},
	}
	return cfg
}

// invNeighbors は island と航路でつながる島を、e.tr.Routes() の順に返す。
func invNeighbors(e *Engine, island uint64) []uint64 {
	var out []uint64
	for _, r := range e.tr.Routes() {
		switch island {
		case r.A:
			out = append(out, r.B)
		case r.B:
			out = append(out, r.A)
		}
	}
	return out
}

// invShuttleRun は IV-10 のボットを、テストモードでラウンドの終わりまで動かし、全イベントを返す。
func invShuttleRun(t *testing.T, cfg Config, seed uint64) []DomainEvent {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 7))
	mustApply(t, e, Command{Type: CmdGameStart})
	all := e.DrainEvents()
	wait := func() { e.RunUntil(e.Now() + SimTime(100+r.IntN(1400))) }
	reqN := 0
	for e.Phase() != PhaseEnded {
		switch x := r.IntN(10); {
		case x < 7: // 船を動かす
			for _, f := range e.tr.Ferries() {
				if f.State != ferry.Docked || f.ReadyAt > int64(e.Now()) {
					continue
				}
				nb := invNeighbors(e, f.At)
				var dest uint64
				var ids []uint64
				space := f.Capacity - len(f.Cargo)
				for _, id := range e.tr.Port(f.At) {
					l, ok := e.tr.Letter(id)
					if !ok || !slices.Contains(nb, l.To) {
						continue
					}
					if dest == 0 {
						dest = l.To
					}
					if l.To == dest && len(ids) < space {
						ids = append(ids, uint64(id))
					}
				}
				if len(ids) > 0 {
					mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: ids})
				}
				to := dest
				if to == 0 && len(f.Cargo) > 0 {
					if l, ok := e.tr.Letter(f.Cargo[0]); ok && slices.Contains(nb, l.To) {
						to = l.To
					}
				}
				if to == 0 {
					to = nb[r.IntN(len(nb))]
				}
				mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: to})
			}
			wait()
		case x < 8: // 屋台の予約を提案する
			island := 1 + uint64(r.IntN(cfg.Islands))
			for _, n := range e.nodes {
				if st := n.Status(); st.Role == raft.StateLeader {
					island = st.ID
					break
				}
			}
			data := invEncode(t, ledger.Command{
				V: ledger.Version, ReqID: fmt.Sprintf("s-%d", reqN%7), Origin: island,
				Kind: ledger.KindStallReserve, Resource: "stall:main",
			})
			reqN++
			if err := e.propose(island, data); err != nil && !errors.Is(err, raft.ErrProposalDropped) {
				t.Fatalf("propose: %v", err)
			}
			e.afterStep()
		default:
			wait()
		}
		all = append(all, e.DrainEvents()...)
	}
	return all
}

func TestInvariantsHoldOverManySeeds(t *testing.T) { // IV-10
	t.Parallel()
	const seeds = 6
	configs := []struct {
		name string
		cfg  func() Config
	}{
		{"3 islands", invConfig3},
		{"5 islands", invConfig5},
	}
	// changed・ok[設定][シード - 1]: Leader の交代・ok の Commit が起きたか。各サブテストは自分の要素だけに書く。
	changed := make([][seeds]bool, len(configs))
	ok := make([][seeds]bool, len(configs))
	// グループ自体は並列にしない（t.Run が、並列のサブテストがすべて終わるまで待つように）。
	t.Run("runs", func(t *testing.T) {
		for ci, c := range configs {
			for seed := uint64(1); seed <= seeds; seed++ {
				t.Run(fmt.Sprintf("%s/seed %d", c.name, seed), func(t *testing.T) {
					t.Parallel()
					evs := invShuttleRun(t, c.cfg(), seed)
					for _, le := range ofType[LeaderElected](evs) {
						if le.Term > 2 {
							changed[ci][seed-1] = true
						}
					}
					for _, la := range ofType[LedgerApplied](evs) {
						if la.Result == ledger.StatusOK {
							ok[ci][seed-1] = true
						}
					}
				})
			}
		}
	})
	if t.Failed() {
		return
	}
	changes, oks := 0, 0
	for ci, c := range configs {
		var cs, os []int
		for i := range seeds {
			if changed[ci][i] {
				cs = append(cs, i+1)
				changes++
			}
			if ok[ci][i] {
				os = append(os, i+1)
				oks++
			}
		}
		t.Logf("%s: leader changes in seeds %v, ok commits in seeds %v", c.name, cs, os)
	}
	if changes == 0 {
		t.Error("no leader change after the opening in any run")
	}
	if oks == 0 {
		t.Error("no ok ledger.applied in any run")
	}
}
