package ferry

import (
	"reflect"
	"testing"
)

func TestBlock(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	if err := tr.Block([][2]uint64{{2, 1}}, 10000); err != nil {
		t.Fatal(err)
	}
	isB := func(a, b uint64, now int64) bool {
		t.Helper()
		v, err := tr.IsBlocked(a, b, now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !isB(1, 2, 9999) || isB(1, 2, 10000) || !isB(2, 1, 0) {
		t.Fatal("blocked window wrong")
	}
	if err := tr.Block([][2]uint64{{1, 2}}, 5000); err != nil {
		t.Fatal(err)
	}
	if !isB(1, 2, 9999) {
		t.Fatal("block shrank")
	}
	if err := tr.Block([][2]uint64{{1, 2}}, 20000); err != nil {
		t.Fatal(err)
	}
	if !isB(1, 2, 19999) || isB(1, 2, 20000) {
		t.Fatal("block not extended")
	}
	before := tr.Routes()
	wantErr(t, tr.Block([][2]uint64{{2, 3}, {1, 3}}, 99999), ErrNoRoute)
	if after := tr.Routes(); !reflect.DeepEqual(before, after) {
		t.Fatalf("routes changed: %+v -> %+v", before, after)
	}
	if err := tr.Block(nil, 1); err != nil {
		t.Fatal(err)
	}
	_, err := tr.IsBlocked(1, 3, 0)
	wantErr(t, err, ErrNoRoute)
	if r := tr.Routes(); r[0].ID() != "1-2" || r[1].ID() != "2-3" {
		t.Fatalf("route ids %s %s", r[0].ID(), r[1].ID())
	}
}

func TestBlockWhileSailing(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	at, err := tr.Depart(0, "f1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Block([][2]uint64{{1, 2}}, 100000); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Arrive(at, "f1"); err != nil {
		t.Fatalf("Arrive: %v", err)
	}
	_, err = tr.Depart(at+1500, "f1", 1)
	wantErr(t, err, ErrRouteBlocked)
	if _, err := tr.Depart(at+1500, "f1", 3); err != nil {
		t.Fatalf("Depart to unblocked route: %v", err)
	}
	if _, err := tr.Depart(100000, "f2", 2); err != nil {
		t.Fatal(err)
	}
}

func TestComponents(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	want := func(now int64, w [][]uint64) {
		t.Helper()
		if got := tr.Components(now); !reflect.DeepEqual(got, w) {
			t.Fatalf("Components(%d) = %v, want %v", now, got, w)
		}
	}
	want(0, [][]uint64{{1, 2, 3}})
	if err := tr.Block([][2]uint64{{1, 2}}, 100); err != nil {
		t.Fatal(err)
	}
	want(0, [][]uint64{{1}, {2, 3}})
	if err := tr.Block([][2]uint64{{3, 2}}, 200); err != nil {
		t.Fatal(err)
	}
	want(50, [][]uint64{{1}, {2}, {3}})
	want(100, [][]uint64{{1, 2}, {3}})
	want(200, [][]uint64{{1, 2, 3}})

	// 島の並びが ID の昇順でない設定でも、成分は昇順。
	cfg := baseConfig()
	cfg.Islands = []uint64{3, 1, 2, 4}
	cfg.Routes = []RouteConfig{{A: 4, B: 1, TravelMs: 1}, {A: 3, B: 2, TravelMs: 1}}
	tr2 := newT(t, cfg)
	if got := tr2.Components(0); !reflect.DeepEqual(got, [][]uint64{{1, 4}, {2, 3}}) {
		t.Fatalf("Components = %v", got)
	}
	if r := tr2.Routes(); r[0].A != 1 || r[0].B != 4 {
		t.Fatalf("route not normalized: %+v", r[0])
	}
}

func TestNextHop(t *testing.T) {
	t.Parallel()
	line := newT(t, baseConfig()) // 1 -3750- 2 -5250- 3
	// 1-2-4 と 1-3-4 が同じ長さ（10）。5-4 は 1 区間でも 1-5-4 より遠い。6 はどこともつながらない。
	cfg := baseConfig()
	cfg.Islands = []uint64{4, 2, 6, 1, 3, 5}
	cfg.Routes = []RouteConfig{
		{A: 1, B: 3, TravelMs: 4},
		{A: 3, B: 4, TravelMs: 6},
		{A: 1, B: 2, TravelMs: 5},
		{A: 2, B: 4, TravelMs: 5},
		{A: 1, B: 5, TravelMs: 1},
		{A: 5, B: 4, TravelMs: 20},
	}
	grid := newT(t, cfg)
	if err := grid.Block([][2]uint64{{1, 2}}, 1<<40); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		tr       *Transport
		from, to uint64
		want     uint64
		ok       bool
	}{
		{"line 1→3 via 2", line, 1, 3, 2, true},
		{"line 3→1 via 2", line, 3, 1, 2, true},
		{"line 1→2 direct", line, 1, 2, 2, true},
		{"line 2→3 direct", line, 2, 3, 3, true},
		{"same island", line, 2, 2, 0, false},
		{"unknown to", line, 1, 9, 0, false},
		{"unknown from", line, 9, 1, 0, false},
		{"tie picks the smaller id (blocked route counts)", grid, 1, 4, 2, true},
		{"4→1 tie", grid, 4, 1, 2, true},
		{"shorter time beats fewer hops", grid, 5, 4, 1, true},
		{"unreachable", grid, 1, 6, 0, false},
		{"from isolated", grid, 6, 1, 0, false},
	}
	if !grid.HasRoute(2, 1) || !grid.HasRoute(1, 2) || grid.HasRoute(1, 4) || grid.HasRoute(1, 1) || grid.HasRoute(1, 9) {
		t.Error("HasRoute wrong")
	}
	for _, tt := range tests {
		got, ok := tt.tr.NextHop(tt.from, tt.to)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: NextHop(%d, %d) = %d, %v; want %d, %v", tt.name, tt.from, tt.to, got, ok, tt.want, tt.ok)
		}
	}
}
