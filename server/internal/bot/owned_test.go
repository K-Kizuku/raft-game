package bot

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

func mustNewFor(t *testing.T, name string, seed uint64, ferries []string) Bot {
	t.Helper()
	b, err := NewFor(name, seed, ferries)
	if err != nil {
		t.Fatalf("NewFor(%q, %q): %v", name, ferries, err)
	}
	return b
}

// O-1: NewFor の引数の検査。
func TestNewForArgs(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		b, err := NewFor(name, 1, []string{"f2", "f1", "f2"})
		if err != nil {
			t.Fatalf("NewFor(%q): %v", name, err)
		}
		if b.Name() != name {
			t.Fatalf("Name() = %q, want %q", b.Name(), name)
		}
	}
	tests := []struct {
		label   string
		name    string
		ferries []string
	}{
		{"unknown bot", "x", []string{"f1"}},
		{"empty bot", "", []string{"f1"}},
		{"nil ferries", "novice", nil},
		{"no ferries", "novice", []string{}},
		{"empty id", "novice", []string{"f1", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()
			b, err := NewFor(tt.name, 1, tt.ferries)
			if err == nil || b != nil {
				t.Fatalf("NewFor(%q, %q) = %v, %v; want error", tt.name, tt.ferries, b, err)
			}
		})
	}
}

// O-2（AC-1）: 担当の船を渡したボットは、担当でない船に操作を出さない。担当の船は動かす。
func TestNewForOnlyOwnedFerries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		owned []string
	}{
		{"f1", []string{"f1"}},
		{"f2", []string{"f2"}},
		{"f1+f3", []string{"f3", "f1"}},
	}
	for _, name := range Names() {
		for _, c := range cases {
			t.Run(name+"/"+c.label, func(t *testing.T) {
				t.Parallel()
				cfg := ringConfig()
				for seed := uint64(1); seed <= 4; seed++ {
					e := newEngine(t, cfg, seed)
					if err := Play(e, mustNewFor(t, name, seed, c.owned)); err != nil {
						t.Fatalf("seed %d: Play: %v", seed, err)
					}
					acted := map[string]bool{}
					for _, in := range e.InputLog() {
						if in.Cmd.Type == sim.CmdGameStart {
							continue
						}
						if !slices.Contains(c.owned, in.Cmd.FerryID) {
							t.Fatalf("seed %d: command for unowned ferry: %+v", seed, in)
						}
						acted[in.Cmd.FerryID] = true
					}
					for _, id := range c.owned {
						if !acted[id] {
							t.Errorf("seed %d: owned ferry %s got no command", seed, id)
						}
					}
					for _, ev := range e.DrainEvents() {
						if d, ok := ev.Data.(sim.FerryDeparted); ok && !slices.Contains(c.owned, d.FerryID) {
							t.Fatalf("seed %d: unowned ferry departed: %+v", seed, d)
						}
					}
				}
			})
		}
	}
}

// O-3: Engine にない船だけを担当するボットは、何も操作しない。
func TestNewForUnknownFerry(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEngine(t, ringConfig(), 1)
			if err := Play(e, mustNewFor(t, name, 1, []string{"f9"})); err != nil {
				t.Fatalf("Play: %v", err)
			}
			if log := e.InputLog(); len(log) != 1 || log[0].Cmd.Type != sim.CmdGameStart {
				t.Fatalf("input log = %+v, want only %s", log, sim.CmdGameStart)
			}
		})
	}
}

// O-4: 担当を分けた 2 つのボットを同じ Engine で動かすと、それぞれ自分の船だけを動かす
// （ほかの船をプレイヤーが動かすときと同じ形）。
func TestNewForSplitBots(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := startedEngine(t, ringConfig(), 1)
			a := mustNewFor(t, name, 1, []string{"f1"})
			b := mustNewFor(t, nameNovice, 1, []string{"f2", "f3"})
			for e.Phase() == sim.PhaseSailing {
				before := len(e.InputLog())
				if err := a.Act(e); err != nil {
					t.Fatalf("a.Act: %v", err)
				}
				mid := e.InputLog()
				for _, in := range mid[before:] {
					if in.Cmd.FerryID != "f1" {
						t.Fatalf("bot a acted on %+v", in)
					}
				}
				if err := b.Act(e); err != nil {
					t.Fatalf("b.Act: %v", err)
				}
				for _, in := range e.InputLog()[len(mid):] {
					if in.Cmd.FerryID == "f1" {
						t.Fatalf("bot b acted on %+v", in)
					}
				}
				at, ok := e.NextNotableAt()
				e.RunUntil(nextWake(e.Snapshot(), at, ok, pollMs))
			}
		})
	}
}

// O-5（AC-2）: すべての船を担当する NewFor のボットは、New のボットと同じ入力ログ・トレースのハッシュになる。
func TestNewForAllFerriesSameAsNew(t *testing.T) {
	t.Parallel()
	for _, name := range Names() {
		for _, c := range testConfigs {
			for seed := uint64(1); seed <= 3; seed++ {
				t.Run(name+"/"+c.name+"/"+strconv.FormatUint(seed, 10), func(t *testing.T) {
					t.Parallel()
					cfg := c.cfg()
					all := make([]string, 0, len(cfg.Ferries))
					for _, f := range slices.Backward(cfg.Ferries) {
						all = append(all, string(f.ID))
					}
					e1, _ := playRun(t, name, cfg, seed)
					e2 := newEngine(t, cfg, seed)
					if err := Play(e2, mustNewFor(t, name, seed, all)); err != nil {
						t.Fatalf("Play: %v", err)
					}
					if !reflect.DeepEqual(e1.InputLog(), e2.InputLog()) {
						t.Fatal("input logs differ")
					}
					if e1.TraceHash() != e2.TraceHash() {
						t.Fatal("trace hashes differ")
					}
				})
			}
		}
	}
}
