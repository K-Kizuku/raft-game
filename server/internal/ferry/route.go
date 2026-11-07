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
	var find func(int) int
	find = func(x int) int {
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
