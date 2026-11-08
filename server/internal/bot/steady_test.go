package bot

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// steadyPlan は s から航路を作った steady で、船 s.Ferries[i] の計画を作る。
func steadyPlan(s sim.Snapshot, i int) []sim.Command {
	b := newSteady()
	b.observe(s)
	return b.plan(s, i)
}

// node は島 id の Raft の状態（Snapshot の Nodes の 1 要素）。
func node(id uint64, role string, term uint64) sim.NodeState {
	return sim.NodeState{ID: id, Role: role, Term: term}
}

// withNodes は s の Nodes を nodes に差し替えた複製を返す。
func withNodes(s sim.Snapshot, nodes ...sim.NodeState) sim.Snapshot {
	s.Nodes = nodes
	return s
}

// healthyLine は島 1 が Term 2 の代表で、ほかが Term 2 の Follower の 3 島。
var healthyLine = []sim.NodeState{node(1, "leader", 2), node(2, "follower", 2), node(3, "follower", 2)}

// S-1
func TestLeaderHealthy(t *testing.T) {
	t.Parallel()
	line := routes(lineRoutes...)
	ring := routes(ringRoutes...)
	ringNodes := []sim.NodeState{
		node(1, "leader", 3), node(2, "follower", 3), node(3, "follower", 3), node(4, "follower", 3), node(5, "follower", 3),
	}
	tests := []struct {
		name  string
		rs    []sim.RouteView
		nodes []sim.NodeState
		want  bool
	}{
		{"leader and all open", line, healthyLine, true},
		{"no leader", line, []sim.NodeState{node(1, "follower", 2), node(2, "follower", 2), node(3, "follower", 2)}, false},
		{"pre_candidate does not raise the term", line, []sim.NodeState{node(1, "leader", 2), node(2, "follower", 2), node(3, "pre_candidate", 2)}, true},
		{"candidate with a higher term", line, []sim.NodeState{node(1, "leader", 2), node(2, "follower", 2), node(3, "candidate", 3)}, false},
		{"old leader and new leader", line, []sim.NodeState{node(1, "leader", 2), node(2, "follower", 3), node(3, "leader", 3)}, true},
		{"leader cut off alone", blocked(line, "1-2"), healthyLine, false},
		{"leader with a majority", blocked(line, "2-3"), healthyLine, true},
		{"ring: leader cut off", blocked(ring, "1-2", "1-5"), ringNodes, false},
		{"ring: one route blocked", blocked(ring, "1-2"), ringNodes, true},
		{"ring: leader with 2 of 5", blocked(ring, "1-2", "4-5"), ringNodes, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n := len(tt.nodes)
			s := withNodes(snap(1000, n, tt.rs, nil, nil), tt.nodes...)
			topo := newTopology(s)
			if got := leaderHealthy(topo, s); got != tt.want {
				t.Fatalf("leaderHealthy = %v, want %v", got, tt.want)
			}
		})
	}
}

// S-2
func TestTopologyPathOpenAndReachable(t *testing.T) {
	t.Parallel()
	s := snap(1000, 3, blocked(routes(lineRoutes...), "2-3"), nil, nil)
	topo := newTopology(s)
	paths := []struct {
		from, to uint64
		want     bool
	}{
		{1, 1, true},
		{1, 2, true},
		{2, 1, true},
		{1, 3, false},
		{3, 1, false},
		{2, 3, false},
		{0, 1, false},
		{1, 4, false},
	}
	for _, p := range paths {
		if got := topo.pathOpen(s, p.from, p.to); got != p.want {
			t.Errorf("pathOpen(%d, %d) = %v, want %v", p.from, p.to, got, p.want)
		}
	}
	for src, want := range []int{0, 2, 2, 1} {
		if got := topo.reachable(s, uint64(src)); got != want {
			t.Errorf("reachable(%d) = %d, want %d", src, got, want)
		}
	}
}

// S-3〜S-6
func TestSteadyPlan(t *testing.T) {
	t.Parallel()
	line := routes(lineRoutes...)
	noLeader := []sim.NodeState{node(1, "follower", 3), node(2, "pre_candidate", 3), node(3, "candidate", 4)}
	votePort := []sim.LetterView{inPort(1, "prevote", 1, 2), inPort(2, "heartbeat", 1, 2)}

	tests := []struct {
		name string
		s    sim.Snapshot
		want []sim.Command
	}{
		{
			// 代表が健在なあいだは、定期便を投票の手紙より先に積む（直行の手紙も load で積む）。
			name: "S-3 healthy: heartbeat before prevote",
			s:    withNodes(snap(1000, 3, line, []sim.FerryView{docked("f1", 1, 1)}, votePort), healthyLine...),
			want: []sim.Command{cmdLoad("f1", []uint64{2}), cmdDepart("f1", 2)},
		},
		{
			// 代表が健在でも、記録した（append_resp）はお元気ですか（heartbeat）より先（greedy と同じ）。
			name: "S-3 healthy: append_resp before heartbeat",
			s: withNodes(snap(1000, 3, line, []sim.FerryView{docked("f1", 2, 1)}, []sim.LetterView{
				inPort(1, "heartbeat_resp", 2, 1), inPort(2, "append_resp", 2, 1), inPort(3, "vote", 2, 1),
			}), healthyLine...),
			want: []sim.Command{cmdLoad("f1", []uint64{2}), cmdDepart("f1", 1)},
		},
		{
			// 代表がいないときは greedy と同じ（投票の手紙が先。おまかせ積み込み）。
			name: "S-4 no leader: same as greedy",
			s:    withNodes(snap(1000, 3, line, []sim.FerryView{docked("f1", 1, 1)}, votePort), noLeader...),
			want: []sim.Command{cmdAutoload("f1", 2), cmdDepart("f1", 2)},
		},
		{
			// 代表が孤立したときも greedy と同じ。
			name: "S-4 leader cut off: same as greedy",
			s: withNodes(snap(1000, 3, blocked(line, "1-2"), []sim.FerryView{docked("f1", 2, 1)}, []sim.LetterView{
				inPort(1, "prevote", 2, 3), inPort(2, "heartbeat_resp", 2, 3),
			}), healthyLine...),
			want: []sim.Command{cmdAutoload("f1", 3), cmdDepart("f1", 3)},
		},
		{
			// 行き先までの途中に通れない航路がある手紙（1 → 3）は積まない。
			name: "S-5 skip letters behind a storm",
			s: withNodes(snap(1000, 3, blocked(line, "2-3"), []sim.FerryView{docked("f1", 1, 4)}, []sim.LetterView{
				inPort(1, "append", 1, 3), inPort(2, "heartbeat", 1, 2),
			}), healthyLine...),
			want: []sim.Command{cmdLoad("f1", []uint64{2}), cmdDepart("f1", 2)},
		},
		{
			// 代表がいないときも、嵐の向こうへ行く手紙は積まない。運ぶものがなければ動かない。
			name: "S-5 skip letters behind a storm without a leader",
			s: withNodes(snap(1000, 3, blocked(line, "2-3"), []sim.FerryView{docked("f1", 1, 4)}, []sim.LetterView{
				inPort(1, "vote", 1, 3),
			}), noLeader...),
			want: nil,
		},
		{
			// 積み荷がすべて嵐の向こうへ行く手紙でも、空きがあれば迎えに行く。
			name: "S-6 pick up with blocked cargo",
			s: withNodes(snap(1000, 3, blocked(line, "2-3"), []sim.FerryView{docked("f1", 2, 2, 5)}, []sim.LetterView{
				onFerry(5, "append", 1, 3, "f1"), inPort(6, "heartbeat", 1, 2),
			}), healthyLine...),
			want: []sim.Command{cmdDepart("f1", 1)},
		},
		{
			// 空きがなければ迎えに行かない。
			name: "S-6 no pick up when full",
			s: withNodes(snap(1000, 3, blocked(line, "2-3"), []sim.FerryView{docked("f1", 2, 1, 5)}, []sim.LetterView{
				onFerry(5, "append", 1, 3, "f1"), inPort(6, "heartbeat", 1, 2),
			}), healthyLine...),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := steadyPlan(tt.s, 0)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("plan = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// S-7 代表がいないとき・孤立したときに嵐がなければ、steady の計画は greedy と同じ。
func TestSteadyMatchesGreedyWithoutLeader(t *testing.T) {
	t.Parallel()
	noLeader := []sim.NodeState{node(1, "follower", 3), node(2, "follower", 3), node(3, "follower", 3)}
	ports := [][]sim.LetterView{
		{inPort(1, "heartbeat", 1, 2), inPort(2, "heartbeat", 1, 3)},
		{inPort(1, "append", 1, 3), inPort(2, "append", 1, 2), inPort(3, "append", 1, 2), inPort(4, "append_probe", 1, 3)},
		{inPort(1, "prevote", 1, 2), inPort(2, "vote_resp", 1, 3), inPort(3, "request", 1, 2), staleInPort(4, "append", 1, 3)},
	}
	for k, port := range ports {
		for _, capacity := range []int{1, 2, 4} {
			s := withNodes(snap(1000, 3, routes(lineRoutes...), []sim.FerryView{docked("f1", 1, capacity)}, port), noLeader...)
			if got, want := steadyPlan(s, 0), greedyPlan(s, 0); !reflect.DeepEqual(got, want) {
				t.Errorf("port %d capacity %d: steady %+v, greedy %+v", k, capacity, got, want)
			}
		}
	}
}

// S-8 代表が健在なとき、steady の順位は greedy の順位から投票の手紙だけを下げたもの。
func TestSteadyRankOrder(t *testing.T) {
	t.Parallel()
	labels := []string{"request", "append", "append_probe", "append_resp", "append_reject", "heartbeat", "heartbeat_resp", "other"}
	for _, a := range labels {
		for _, b := range labels {
			la, lb := sim.LetterView{Label: a}, sim.LetterView{Label: b}
			if (rank(la) < rank(lb)) != (steadyRank(la) < steadyRank(lb)) {
				t.Errorf("%s / %s: greedy rank %d / %d, steady rank %d / %d", a, b, rank(la), rank(lb), steadyRank(la), steadyRank(lb))
			}
		}
	}
	for _, v := range []string{"prevote", "prevote_resp", "vote", "vote_resp"} {
		lv := sim.LetterView{Label: v}
		for _, b := range []string{"request", "append", "append_resp", "heartbeat", "heartbeat_resp"} {
			if lb := (sim.LetterView{Label: b}); steadyRank(lv) <= steadyRank(lb) {
				t.Errorf("steadyRank(%s) = %d, want > steadyRank(%s) = %d", v, steadyRank(lv), b, steadyRank(lb))
			}
		}
		if got := steadyWeight(lv); got > steadyWeight(sim.LetterView{Label: "heartbeat"}) {
			t.Errorf("steadyWeight(%s) = %d, want <= heartbeat", v, got)
		}
	}
	if got := steadyWeight(staleInPort(1, "request", 1, 2)); got != staleWeight {
		t.Errorf("steadyWeight(stale request) = %d, want %d", got, staleWeight)
	}
}
