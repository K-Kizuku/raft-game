package bot

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// lineConfig は #23 の testConfig() と同じ値（島 3 つ、航路 1-2・2-3、船 f1）。
func lineConfig() sim.Config {
	return sim.Config{
		Islands:       3,
		InitialLeader: 1,
		DurationMs:    120000,
		Raft: sim.RaftConfig{
			TickMs:          500,
			HeartbeatTick:   2,
			ElectionTick:    40,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 4,
		},
		Routes: []ferry.RouteConfig{
			{A: 1, B: 2, TravelMs: 3750},
			{A: 2, B: 3, TravelMs: 5250},
		},
		Ferries: []ferry.FerryConfig{
			{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
		Resources: []ledger.ResourceDef{
			{ID: "firework:a", Kind: ledger.ResourceFirework},
			{ID: "stall:main", Kind: ledger.ResourceStall, Capacity: 2},
		},
	}
}

// triConfig は lineConfig に航路 1-3 と船 f2 を足したもの（#24 の invConfig3() と同じ）。
func triConfig() sim.Config {
	cfg := lineConfig()
	cfg.Routes = append(cfg.Routes, ferry.RouteConfig{A: 1, B: 3, TravelMs: 6000})
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 3, SpeedPermille: 1200})
	return cfg
}

// ringConfig は 5 島の環状の設定（#24 の invConfig5() と同じ）。
func ringConfig() sim.Config {
	cfg := lineConfig()
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

// twinConfig は lineConfig に、島 1 から出る船 f2 を足したもの（A-4 だけで使う）。
func twinConfig() sim.Config {
	cfg := lineConfig()
	cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: "f2", Home: 1, Capacity: 4, SpeedPermille: 1000})
	return cfg
}

// testConfigs は PL-3・PL-4 の設定。
var testConfigs = []struct {
	name string
	cfg  func() sim.Config
}{
	{"line", lineConfig},
	{"tri", triConfig},
	{"ring", ringConfig},
}

// --- 手で作る Snapshot の補助 ---

// snap は時刻 t・島 n 個・航路 routes（ID）・船・手紙の Snapshot を作る。手紙は ID の昇順に並べる。
func snap(t sim.SimTime, n int, routes []sim.RouteView, ferries []sim.FerryView, letters []sim.LetterView) sim.Snapshot {
	s := sim.Snapshot{
		Phase:   sim.PhaseSailing,
		T:       t,
		Ferries: ferries,
		Routes:  routes,
		Letters: slices.Clone(letters),
	}
	for i := 1; i <= n; i++ {
		s.Nodes = append(s.Nodes, sim.NodeState{ID: uint64(i)})
	}
	slices.SortFunc(s.Letters, func(a, b sim.LetterView) int { return cmp.Compare(a.ID, b.ID) })
	return s
}

// routes は ID だけの航路の列。
func routes(ids ...string) []sim.RouteView {
	out := make([]sim.RouteView, len(ids))
	for i, id := range ids {
		out[i] = sim.RouteView{ID: id}
	}
	return out
}

// blocked は rs の ID の航路を遮断中にした複製を返す。
func blocked(rs []sim.RouteView, ids ...string) []sim.RouteView {
	out := slices.Clone(rs)
	until := sim.SimTime(999999)
	for i := range out {
		if slices.Contains(ids, out[i].ID) {
			out[i].BlockedUntil = &until
		}
	}
	return out
}

var (
	lineRoutes = []string{"1-2", "2-3"}
	ringRoutes = []string{"1-2", "2-3", "3-4", "4-5", "1-5"}
)

// docked は島 island に停泊していて動ける船。
func docked(id string, island uint64, capacity int, cargo ...uint64) sim.FerryView {
	if cargo == nil {
		cargo = []uint64{}
	}
	return sim.FerryView{ID: id, Capacity: capacity, State: "docked", IslandID: island, Cargo: cargo}
}

// inPort は港 From にある手紙。
func inPort(id uint64, label string, from, to uint64) sim.LetterView {
	l := sim.LetterView{ID: id, Kind: "raft", From: from, To: to, Loc: sim.LocView{Type: "port", IslandID: from}, Label: label}
	if label == "request" {
		l.Kind = "request"
	}
	return l
}

// staleInPort は港 From にある古い手紙。
func staleInPort(id uint64, label string, from, to uint64) sim.LetterView {
	l := inPort(id, label, from, to)
	l.Raft = &sim.RaftView{Stale: true}
	return l
}

// onFerry は船 ferryID の上の手紙。
func onFerry(id uint64, label string, from, to uint64, ferryID string) sim.LetterView {
	l := inPort(id, label, from, to)
	l.Loc = sim.LocView{Type: "ferry", FerryID: ferryID}
	return l
}

// --- Engine の補助 ---

// newEngine はテストモードの Engine を作る。
func newEngine(t *testing.T, cfg sim.Config, seed uint64) *sim.Engine {
	t.Helper()
	e, err := sim.New(cfg, seed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("sim.New: %v", err)
	}
	return e
}

// startedEngine は cmd.game.start を適用した Engine。
func startedEngine(t *testing.T, cfg sim.Config, seed uint64) *sim.Engine {
	t.Helper()
	e := newEngine(t, cfg, seed)
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	return e
}

func mustNew(t *testing.T, name string, seed uint64) Bot {
	t.Helper()
	b, err := New(name, seed)
	if err != nil {
		t.Fatalf("New(%q): %v", name, err)
	}
	return b
}

// K-1
func TestNamesAndNew(t *testing.T) {
	t.Parallel()
	names := Names()
	if want := []string{"greedy", "random"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("Names() = %q, want %q", names, want)
	}
	names[0] = "x"
	if got := Names(); got[0] != "greedy" {
		t.Fatalf("Names() after write = %q", got)
	}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{"random", false},
		{"greedy", false},
		{"", true},
		{"Random", true},
		{"GREEDY", true},
		{" greedy", true},
		{"x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := New(tt.name, 1)
			if tt.wantErr {
				if err == nil || b != nil {
					t.Fatalf("New(%q) = %v, %v; want error", tt.name, b, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", tt.name, err)
			}
			if b.Name() != tt.name {
				t.Fatalf("Name() = %q, want %q", b.Name(), tt.name)
			}
		})
	}
}

// K-2
func TestActOtherEnginePanics(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := mustNew(t, name, 1)
			if err := b.Act(startedEngine(t, lineConfig(), 1)); err != nil {
				t.Fatalf("Act: %v", err)
			}
			other := startedEngine(t, triConfig(), 1)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("Act on another engine did not panic")
				}
				if msg, _ := r.(string); !strings.Contains(msg, "bot: engine routes changed") {
					t.Fatalf("panic = %v", r)
				}
			}()
			_ = b.Act(other)
		})
	}
}
