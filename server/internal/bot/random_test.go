package bot

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// randomPlanner は s から航路を作った、乱数 rng の random。
func randomPlanner(s sim.Snapshot, rng *rand.Rand) *randomBot {
	b := newRandom(rng)
	b.observe(s)
	return b
}

// r2Snapshot は R-1・R-2 の状態。f1 が島 2 で動ける。積み荷は 10（1→3）と 11（2→1）、港 2 は 20・21・22。
func r2Snapshot() sim.Snapshot {
	return snap(1000, 3, routes(lineRoutes...), []sim.FerryView{docked("f1", 2, 4, 10, 11)}, []sim.LetterView{
		onFerry(10, "heartbeat", 1, 3, "f1"),
		onFerry(11, "heartbeat_resp", 2, 1, "f1"),
		inPort(20, "heartbeat_resp", 2, 1),
		inPort(21, "append_resp", 2, 3),
		inPort(22, "vote_resp", 2, 1),
	})
}

// R-1
func TestRandomSameRandSamePlan(t *testing.T) {
	t.Parallel()
	s := r2Snapshot()
	a := randomPlanner(s, rand.New(rand.NewPCG(7, 0)))
	b := randomPlanner(s, rand.New(rand.NewPCG(7, 0)))
	var pa, pb [][]sim.Command
	for range 100 {
		pa = append(pa, a.plan(s, 0))
		pb = append(pb, b.plan(s, 0))
	}
	if !reflect.DeepEqual(pa, pb) {
		t.Fatal("plans differ for the same rand")
	}
}

// R-2
func TestRandomPlanShapes(t *testing.T) {
	t.Parallel()
	s := r2Snapshot()
	hasTo := func(to uint64) bool {
		return slices.ContainsFunc(portLetters(s, 2), func(l sim.LetterView) bool { return l.To == to })
	}
	shapes := []string{"empty", "unload", "autoload", "load", "depart"}
	seen := make([]bool, len(shapes))
	seenTo := [4]bool{}
	for seed := uint64(1); seed <= 300; seed++ {
		p := randomPlanner(s, rand.New(rand.NewPCG(seed, 0))).plan(s, 0)
		shape := ""
		switch {
		case len(p) == 0:
			shape = "empty"
		case len(p) == 1 && p[0].Type == sim.CmdFerryUnload:
			if !reflect.DeepEqual(p[0], cmdUnload("f1", []uint64{11})) {
				t.Fatalf("seed %d: unload = %+v", seed, p[0])
			}
			shape = "unload"
		default:
			last := p[len(p)-1]
			if last.Type != sim.CmdFerryDepart || last.FerryID != "f1" || (last.To != 1 && last.To != 3) {
				t.Fatalf("seed %d: plan = %+v", seed, p)
			}
			to := last.To
			seenTo[to] = true
			switch {
			case len(p) == 1:
				shape = "depart"
			case len(p) == 2 && p[0].Type == sim.CmdFerryAutoload:
				if !reflect.DeepEqual(p[0], cmdAutoload("f1", to)) || !hasTo(to) {
					t.Fatalf("seed %d: autoload = %+v", seed, p)
				}
				shape = "autoload"
			case len(p) == 2 && p[0].Type == sim.CmdFerryLoad:
				ids := p[0].LetterIDs
				if p[0].FerryID != "f1" || len(ids) < 1 || len(ids) > 2 {
					t.Fatalf("seed %d: load = %+v", seed, p)
				}
				sorted := slices.Clone(ids)
				slices.Sort(sorted)
				if len(slices.Compact(sorted)) != len(ids) {
					t.Fatalf("seed %d: duplicate ids %v", seed, ids)
				}
				for _, id := range ids {
					if id != 20 && id != 21 && id != 22 {
						t.Fatalf("seed %d: load id %d", seed, id)
					}
				}
				shape = "load"
			default:
				t.Fatalf("seed %d: plan = %+v", seed, p)
			}
		}
		seen[slices.Index(shapes, shape)] = true
	}
	for k, shape := range shapes {
		if !seen[k] {
			t.Errorf("shape %q never appeared", shape)
		}
	}
	if !seenTo[1] || !seenTo[3] {
		t.Errorf("destinations seen: 1=%v 3=%v", seenTo[1], seenTo[3])
	}
}

// R-3
func TestRandomStreamName(t *testing.T) {
	t.Parallel()
	b := mustNew(t, "random", 42).(*randomBot)
	want := sim.Sub(42, "bot/random")
	for k := range 8 {
		if got, w := b.rng.IntN(1000), want.IntN(1000); got != w {
			t.Fatalf("IntN #%d = %d, want %d", k, got, w)
		}
	}
}
