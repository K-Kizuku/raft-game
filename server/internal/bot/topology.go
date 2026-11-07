package bot

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// topology は Snapshot から読んだ航路の隣接と最短経路（距離は航路の数）。島は 1..N の添字の slice で持つ
// （添字 0 は使わない）。遮断（BlockedUntil）はこの表に入れず、その時点の Snapshot で見る。
type topology struct {
	n        int
	routeIDs []string // s.Routes の ID の列（最初の Snapshot の順）

	neighbors [][]uint64 // neighbors[i]: 島 i と航路でつながる島の ID の昇順
	routeOf   [][]int    // routeOf[i][k]: neighbors[i][k] への航路の s.Routes の添字
	hops      [][]int    // hops[i][j]: 航路の数の最短。届かないときは -1
	nextHop   [][]uint64 // nextHop[i][j]（i != j）: 最短経路で次に行く隣の島。届かないとき・i == j は 0
}

// newTopology は s から航路の表を作る。航路の ID が読めない・端が 1..N の外・両端が同じ・
// 同じ島の組の航路が 2 本あるときは panic する（Engine の誤り）。
func newTopology(s sim.Snapshot) *topology {
	n := len(s.Nodes)
	t := &topology{
		n:         n,
		routeIDs:  make([]string, len(s.Routes)),
		neighbors: make([][]uint64, n+1),
		routeOf:   make([][]int, n+1),
	}
	type edge struct {
		to    uint64
		route int
	}
	adj := make([][]edge, n+1)
	for k, r := range s.Routes {
		t.routeIDs[k] = r.ID
		a, b := parseRouteID(r.ID, n)
		for _, e := range adj[a] {
			if e.to == b {
				panic(fmt.Sprintf("bot: duplicate route %q", r.ID))
			}
		}
		adj[a] = append(adj[a], edge{to: b, route: k})
		adj[b] = append(adj[b], edge{to: a, route: k})
	}
	for i := 1; i <= n; i++ {
		slices.SortFunc(adj[i], func(x, y edge) int { return cmp.Compare(x.to, y.to) })
		t.neighbors[i] = make([]uint64, len(adj[i]))
		t.routeOf[i] = make([]int, len(adj[i]))
		for k, e := range adj[i] {
			t.neighbors[i][k] = e.to
			t.routeOf[i][k] = e.route
		}
	}

	t.hops = make([][]int, n+1)
	for i := 1; i <= n; i++ {
		t.hops[i] = t.bfs(uint64(i))
	}

	t.nextHop = make([][]uint64, n+1)
	for i := 1; i <= n; i++ {
		t.nextHop[i] = make([]uint64, n+1)
		for j := 1; j <= n; j++ {
			if i == j {
				continue
			}
			best := -1
			for _, d := range t.neighbors[i] {
				cost := 1
				if d != uint64(j) {
					h := t.hops[d][j]
					if h < 0 {
						continue
					}
					cost = 1 + h
				}
				if best < 0 || cost < best {
					best = cost
					t.nextHop[i][j] = d
				}
			}
		}
	}
	return t
}

// bfs は島 src から各島までの航路の数の最短を返す（添字 = 島の ID。届かないときは -1）。
func (t *topology) bfs(src uint64) []int {
	dist := make([]int, t.n+1)
	for i := range dist {
		dist[i] = -1
	}
	dist[src] = 0
	queue := []uint64{src}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, d := range t.neighbors[v] {
			if dist[d] < 0 {
				dist[d] = dist[v] + 1
				queue = append(queue, d)
			}
		}
	}
	return dist
}

// parseRouteID は航路の ID "<A>-<B>"（10 進数）を読む。読めない・端が 1..n の外・両端が同じなら panic する。
func parseRouteID(id string, n int) (uint64, uint64) {
	as, bs, ok := strings.Cut(id, "-")
	if !ok {
		panic(fmt.Sprintf("bot: bad route id %q", id))
	}
	a, errA := strconv.ParseUint(as, 10, 64)
	b, errB := strconv.ParseUint(bs, 10, 64)
	switch {
	case errA != nil || errB != nil:
		panic(fmt.Sprintf("bot: bad route id %q", id))
	case a < 1 || a > uint64(n) || b < 1 || b > uint64(n):
		panic(fmt.Sprintf("bot: route %q is not in 1..%d", id, n))
	case a == b:
		panic(fmt.Sprintf("bot: route %q is a loop", id))
	}
	return a, b
}

// check は s の島の数と航路の ID の列が、表を作ったときと同じであることを確かめる。違えば panic する
// （1 つの Bot を別の Engine に使った誤り）。
func (t *topology) check(s sim.Snapshot) {
	if len(s.Nodes) != t.n || len(s.Routes) != len(t.routeIDs) {
		panic("bot: engine routes changed")
	}
	for k, r := range s.Routes {
		if r.ID != t.routeIDs[k] {
			panic("bot: engine routes changed")
		}
	}
}

// valid は id が島の ID（1..N）か。
func (t *topology) valid(id uint64) bool {
	return id >= 1 && id <= uint64(t.n)
}

// next は、島 i から島 j へ最短経路で次に行く隣の島（届かない・i == j・範囲の外なら 0）。
func (t *topology) next(i, j uint64) uint64 {
	if !t.valid(i) || !t.valid(j) {
		return 0
	}
	return t.nextHop[i][j]
}

// passable は、島 i の隣の島 d への航路が s の時点で通れるか（航路がなければ false）。
func (t *topology) passable(s sim.Snapshot, i, d uint64) bool {
	if !t.valid(i) {
		return false
	}
	k, ok := slices.BinarySearch(t.neighbors[i], d)
	if !ok {
		return false
	}
	return s.Routes[t.routeOf[i][k]].BlockedUntil == nil
}

// openNeighbors は、島 i の隣の島のうち、s の時点で航路が通れるもの（昇順）。
func (t *topology) openNeighbors(s sim.Snapshot, i uint64) []uint64 {
	if !t.valid(i) {
		return nil
	}
	var out []uint64
	for k, d := range t.neighbors[i] {
		if s.Routes[t.routeOf[i][k]].BlockedUntil == nil {
			out = append(out, d)
		}
	}
	return out
}
