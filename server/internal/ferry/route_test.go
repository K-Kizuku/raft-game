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
