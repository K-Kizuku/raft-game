package bot

import (
	"reflect"
	"testing"
)

// P-1
func TestTopologyLine(t *testing.T) {
	t.Parallel()
	tp := newTopology(snap(0, 3, routes(lineRoutes...), nil, nil))
	if got, want := tp.neighbors[2], []uint64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("neighbors[2] = %v, want %v", got, want)
	}
	if got := tp.nextHop[1][3]; got != 2 {
		t.Errorf("nextHop[1][3] = %d, want 2", got)
	}
	if got := tp.nextHop[3][1]; got != 2 {
		t.Errorf("nextHop[3][1] = %d, want 2", got)
	}
	if got := tp.hops[1][3]; got != 2 {
		t.Errorf("hops[1][3] = %d, want 2", got)
	}
}

// P-2〜P-4
func TestTopologyNextHop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		n      int
		routes []string
		i, j   uint64
		next   uint64
		hops   int
	}{
		{"P-2 ring 1->3", 5, ringRoutes, 1, 3, 2, 2},
		{"P-2 ring 1->4", 5, ringRoutes, 1, 4, 5, 2},
		{"P-3 same length", 4, []string{"1-2", "2-4", "1-3", "3-4"}, 1, 4, 2, 2},
		{"P-4 unreachable 1->3", 4, []string{"1-2", "3-4"}, 1, 3, 0, -1},
		{"P-4 unreachable 1->4", 4, []string{"1-2", "3-4"}, 1, 4, 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tp := newTopology(snap(0, tt.n, routes(tt.routes...), nil, nil))
			if got := tp.nextHop[tt.i][tt.j]; got != tt.next {
				t.Errorf("nextHop[%d][%d] = %d, want %d", tt.i, tt.j, got, tt.next)
			}
			if got := tp.hops[tt.i][tt.j]; got != tt.hops {
				t.Errorf("hops[%d][%d] = %d, want %d", tt.i, tt.j, got, tt.hops)
			}
		})
	}
}

// P-5
func TestTopologyBadRoute(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"1-x", "2-2", "1-4"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatalf("newTopology with route %q did not panic", id)
				}
			}()
			newTopology(snap(0, 3, routes(id), nil, nil))
		})
	}
}
