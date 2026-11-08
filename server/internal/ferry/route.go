package ferry

import (
	"fmt"
	"slices"
	"strconv"
)

// Route は 2 つの島を結ぶ無向の航路。
type Route struct {
	A, B         uint64 // A < B（無向）
	TravelMs     int64
	BlockedUntil int64 // この時刻より前は遮断。0 なら遮断なし
}

// ID は "A-B"（例: "1-2"。プロトコル §5.1 の routes[].id）。
func (r Route) ID() string {
	return strconv.FormatUint(r.A, 10) + "-" + strconv.FormatUint(r.B, 10)
}

func (r Route) blocked(now int64) bool { return now < r.BlockedUntil }

type routeKey struct{ a, b uint64 }

func makeRouteKey(a, b uint64) routeKey {
	if a > b {
		a, b = b, a
	}
	return routeKey{a, b}
}

// route は a と b を結ぶ航路の添字を返す。
func (t *Transport) route(a, b uint64) (int, bool) {
	i, ok := t.routeIdx[makeRouteKey(a, b)]
	return i, ok
}

// Block は各組（向きを問わない）の航路の BlockedUntil を max(BlockedUntil, until) にする。
// 組の航路がなければ ErrNoRoute で、何も変えない。
func (t *Transport) Block(routes [][2]uint64, until int64) error {
	idx := make([]int, 0, len(routes))
	for _, p := range routes {
		i, ok := t.route(p[0], p[1])
		if !ok {
			return fmt.Errorf("%w: %d-%d", ErrNoRoute, p[0], p[1])
		}
		idx = append(idx, i)
	}
	for _, i := range idx {
		if until > t.routes[i].BlockedUntil {
			t.routes[i].BlockedUntil = until
		}
	}
	return nil
}

// IsBlocked は now に a-b の航路が遮断されているか（now < BlockedUntil）を返す。
func (t *Transport) IsBlocked(a, b uint64, now int64) (bool, error) {
	i, ok := t.route(a, b)
	if !ok {
		return false, fmt.Errorf("%w: %d-%d", ErrNoRoute, a, b)
	}
	return t.routes[i].blocked(now), nil
}

// Components は now に遮断されていない航路だけでの連結成分を返す。
// 各成分は島の ID の昇順、成分の並びは先頭の島の ID の昇順。
func (t *Transport) Components(now int64) [][]uint64 {
	islands := slices.Clone(t.islands)
	slices.Sort(islands)
	pos := make(map[uint64]int, len(islands))
	for i, id := range islands {
		pos[id] = i
	}
	parent := make([]int, len(islands))
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, r := range t.routes {
		if r.blocked(now) {
			continue
		}
		ra, rb := find(pos[r.A]), find(pos[r.B])
		if ra == rb {
			continue
		}
		// 小さい添字を根にする（決定的）。
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}
	var out [][]uint64
	compOf := make([]int, len(islands)) // 根の添字 → out の添字 + 1
	for i, id := range islands {
		r := find(i)
		if compOf[r] == 0 {
			out = append(out, nil)
			compOf[r] = len(out)
		}
		out[compOf[r]-1] = append(out[compOf[r]-1], id)
	}
	return out
}

// HasRoute は a と b を直接結ぶ航路があるか（遮断中でも true）を返す。
func (t *Transport) HasRoute(a, b uint64) bool {
	_, ok := t.route(a, b)
	return ok
}

// NextHop は、航路（遮断中のものを含む）と所要時間（TravelMs）での from から to への最短の経路で、
// from の次に寄る島を返す（ゲームデザイン §3.4 の経由の手紙）。最短の経路が複数あるときは、次の島の ID の
// 小さい方。from と to が同じ・どちらかがステージにない・経路がないときは (0, false)。
func (t *Transport) NextHop(from, to uint64) (uint64, bool) {
	if from == to {
		return 0, false
	}
	dist, ok := t.distancesTo(to)
	if !ok {
		return 0, false
	}
	if _, ok := dist[from]; !ok {
		return 0, false
	}
	var next uint64
	best := int64(-1)
	for _, r := range t.routes {
		var n uint64
		switch from {
		case r.A:
			n = r.B
		case r.B:
			n = r.A
		default:
			continue
		}
		d, ok := dist[n]
		if !ok {
			continue
		}
		c := r.TravelMs + d
		if best < 0 || c < best || (c == best && n < next) {
			best, next = c, n
		}
	}
	if best < 0 {
		return 0, false
	}
	return next, true
}

// distancesTo は、各島から to までの最短の所要時間を返す（Dijkstra 法。遮断中の航路も使う）。
// 経路のない島は含めない。to がステージになければ (nil, false)。map は引くだけで、走査しない。
func (t *Transport) distancesTo(to uint64) (map[uint64]int64, bool) {
	if !slices.Contains(t.islands, to) {
		return nil, false
	}
	dist := map[uint64]int64{to: 0}
	done := make(map[uint64]bool, len(t.islands))
	for {
		// 未確定で最短の島を、島の並び（t.islands）の順に探す（決定的）。
		var cur uint64
		found := false
		for _, id := range t.islands {
			d, ok := dist[id]
			if !ok || done[id] {
				continue
			}
			if !found || d < dist[cur] {
				cur, found = id, true
			}
		}
		if !found {
			return dist, true
		}
		done[cur] = true
		for _, r := range t.routes {
			var n uint64
			switch cur {
			case r.A:
				n = r.B
			case r.B:
				n = r.A
			default:
				continue
			}
			nd := dist[cur] + r.TravelMs
			if d, ok := dist[n]; !ok || nd < d {
				dist[n] = nd
			}
		}
	}
}
