package bot

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// greedyPlan は s から航路を作った greedy で、船 s.Ferries[i] の計画を作る。
func greedyPlan(s sim.Snapshot, i int) []sim.Command {
	b := newGreedy()
	b.observe(s)
	return b.plan(s, i)
}

// departTo は計画の最後の depart の行き先（depart がなければ 0）。
func departTo(cmds []sim.Command) uint64 {
	if len(cmds) == 0 || cmds[len(cmds)-1].Type != sim.CmdFerryDepart {
		return 0
	}
	return cmds[len(cmds)-1].To
}

func TestGreedyPlan(t *testing.T) {
	t.Parallel()
	line := routes(lineRoutes...)
	// G-1 の港。
	g1Port := []sim.LetterView{inPort(1, "heartbeat", 1, 2), inPort(2, "heartbeat", 1, 3)}
	// G-3 の港。
	g3Port := []sim.LetterView{inPort(1, "heartbeat_resp", 2, 1), inPort(2, "heartbeat", 2, 3)}
	// G-4 の港。
	g4Port := []sim.LetterView{
		inPort(1, "append_resp", 2, 1), inPort(2, "append_resp", 2, 1), inPort(3, "append_resp", 2, 1),
		inPort(4, "append", 2, 3),
	}

	tests := []struct {
		name string
		s    sim.Snapshot
		i    int
		want []sim.Command // nil なら wantTo だけを見る
		to   uint64        // want が nil のときの行き先（0 = 計画が空）
	}{
		{
			name: "G-1 direct and relay",
			s:    snap(1000, 3, line, []sim.FerryView{docked("f1", 1, 4)}, g1Port),
			want: []sim.Command{cmdAutoload("f1", 2), cmdLoad("f1", []uint64{2}), cmdDepart("f1", 2)},
		},
		{
			name: "G-2 heavier letter",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, []sim.LetterView{
				inPort(1, "heartbeat_resp", 2, 1), inPort(2, "heartbeat_resp", 2, 1), inPort(3, "vote", 2, 3),
			}),
			to: 3,
		},
		{
			name: "G-3 tie",
			s:    snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, g3Port),
			to:   1,
		},
		{
			name: "G-4 free space 4",
			s:    snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, g4Port),
			to:   1,
		},
		{
			name: "G-4 free space 1",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4, 101, 102, 103)}, append([]sim.LetterView{
				onFerry(101, "heartbeat", 1, 3, "f1"), onFerry(102, "heartbeat", 1, 3, "f1"), onFerry(103, "heartbeat", 1, 3, "f1"),
			}, g4Port...)),
			to: 3,
		},
		{
			name: "G-5 cargo only",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4, 10)}, []sim.LetterView{
				onFerry(10, "append", 1, 3, "f1"), inPort(1, "heartbeat_resp", 2, 1),
			}),
			want: []sim.Command{cmdDepart("f1", 3)},
		},
		{
			name: "G-6 full",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 1, 4, 11, 12, 13, 14)}, append([]sim.LetterView{
				onFerry(11, "heartbeat", 1, 2, "f1"), onFerry(12, "heartbeat", 1, 2, "f1"),
				onFerry(13, "heartbeat", 1, 2, "f1"), onFerry(14, "heartbeat", 1, 2, "f1"),
			}, g1Port...)),
			want: []sim.Command{cmdDepart("f1", 2)},
		},
		{
			name: "G-7 blocked 1-2",
			s:    snap(1000, 3, blocked(line, "1-2"), []sim.FerryView{docked("f1", 2, 4)}, g3Port),
			to:   3,
		},
		{
			name: "G-7 blocked both",
			s:    snap(1000, 3, blocked(line, "1-2", "2-3"), []sim.FerryView{docked("f1", 2, 4)}, g3Port),
			to:   0,
		},
		{
			name: "G-8 stale",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, []sim.LetterView{
				staleInPort(1, "vote", 2, 1), inPort(2, "heartbeat", 2, 3), inPort(3, "heartbeat", 2, 3),
			}),
			to: 3,
		},
		{
			name: "G-9 request",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, []sim.LetterView{
				inPort(1, "request", 2, 1), inPort(2, "vote", 2, 3), inPort(3, "vote", 2, 3), inPort(4, "vote", 2, 3),
			}),
			to: 1,
		},
		{
			name: "G-10 pick up",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 3, 4)}, []sim.LetterView{
				inPort(1, "heartbeat", 1, 2),
			}),
			want: []sim.Command{cmdDepart("f1", 2)},
		},
		{
			name: "G-10 nothing anywhere",
			s:    snap(1000, 3, line, []sim.FerryView{docked("f1", 3, 4)}, nil),
			to:   0,
		},
		{
			name: "G-10 undeliverable cargo",
			s: snap(1000, 4, routes("1-2", "3-4"), []sim.FerryView{docked("f1", 1, 4, 5)}, []sim.LetterView{
				onFerry(5, "heartbeat", 1, 3, "f1"), inPort(6, "heartbeat_resp", 2, 1),
			}),
			to: 0,
		},
		{
			name: "G-11 other ferry's cargo",
			s: snap(1000, 3, line, []sim.FerryView{docked("f1", 1, 4, 1), docked("f2", 1, 4)}, []sim.LetterView{
				onFerry(1, "heartbeat", 1, 2, "f1"), inPort(2, "heartbeat", 1, 2),
			}),
			i:    1,
			want: []sim.Command{cmdAutoload("f2", 2), cmdDepart("f2", 2)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := greedyPlan(tt.s, tt.i)
			if tt.want != nil {
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("plan = %+v, want %+v", got, tt.want)
				}
				return
			}
			if tt.to == 0 {
				if len(got) != 0 {
					t.Fatalf("plan = %+v, want empty", got)
				}
				return
			}
			if to := departTo(got); to != tt.to {
				t.Fatalf("plan = %+v, want to %d", got, tt.to)
			}
		})
	}
}
