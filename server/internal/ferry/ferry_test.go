package ferry

import (
	"reflect"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func TestFerryInitial(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	f, ok := tr.Ferry("f1")
	if !ok || f.State != Docked || f.At != 1 || f.ReadyAt != 0 || len(f.Cargo) != 0 || f.Capacity != 4 || f.Name != "One" {
		t.Fatalf("f1 = %+v", f)
	}
	if _, ok := tr.Ferry("nope"); ok {
		t.Fatal("unknown ferry found")
	}
	fs := tr.Ferries()
	if len(fs) != 2 || fs[0].ID != "f1" || fs[1].ID != "f2" || fs[1].At != 3 {
		t.Fatalf("Ferries = %+v", fs)
	}
}

func TestTravelMs(t *testing.T) {
	t.Parallel()
	tests := []struct{ route, speed, want int64 }{
		{3750, 1000, 3750}, {3750, 1200, 4500}, {5250, 1200, 6300},
		{5, 1100, 6}, {5, 1300, 7}, {5, 900, 5}, {1, 1, 1},
	}
	for _, tt := range tests {
		if got := TravelMs(tt.route, tt.speed); got != tt.want {
			t.Errorf("TravelMs(%d, %d) = %d, want %d", tt.route, tt.speed, got, tt.want)
		}
	}
}

func TestDepartErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, tr *Transport)
		now   int64
		id    FerryID
		to    uint64
		want  error
	}{
		{"unknown ferry", nil, 0, "zz", 2, ErrUnknownFerry},
		{"sailing", func(t *testing.T, tr *Transport) {
			if _, err := tr.Depart(0, "f1", 2); err != nil {
				t.Fatal(err)
			}
		}, 1, "f1", 2, ErrNotDocked},
		{"not ready", func(t *testing.T, tr *Transport) {
			at, _ := tr.Depart(0, "f1", 2)
			if _, err := tr.Arrive(at, "f1"); err != nil {
				t.Fatal(err)
			}
		}, 3750 + 1499, "f1", 1, ErrNotReady},
		{"same island", nil, 0, "f1", 1, ErrNoRoute},
		{"unknown island", nil, 0, "f1", 9, ErrNoRoute},
		{"no direct route", nil, 0, "f1", 3, ErrNoRoute},
		{"blocked", func(t *testing.T, tr *Transport) {
			if err := tr.Block([][2]uint64{{1, 2}}, 100); err != nil {
				t.Fatal(err)
			}
		}, 99, "f1", 2, ErrRouteBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, baseConfig())
			if tt.setup != nil {
				tt.setup(t, tr)
			}
			before := tr.Ferries()
			_, err := tr.Depart(tt.now, tt.id, tt.to)
			wantErr(t, err, tt.want)
			if after := tr.Ferries(); !reflect.DeepEqual(before, after) {
				t.Fatalf("ferries changed: %+v -> %+v", before, after)
			}
		})
	}
}

func TestDepartArrive(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	at, err := tr.Depart(0, "f1", 2)
	if err != nil || at != 3750 {
		t.Fatalf("Depart = %d, %v", at, err)
	}
	f, _ := tr.Ferry("f1")
	if f.State != Sailing || f.From != 1 || f.To != 2 || f.At != 0 || f.DepartAt != 0 || f.ArriveAt != 3750 {
		t.Fatalf("f1 = %+v", f)
	}
	mustInv(t, tr)
	_, err = tr.Arrive(3749, "f1")
	wantErr(t, err, ErrNotArriving)
	if _, err := tr.Arrive(3750, "f1"); err != nil {
		t.Fatal(err)
	}
	f, _ = tr.Ferry("f1")
	if f.State != Docked || f.At != 2 || f.ReadyAt != 5250 || f.From != 0 || f.To != 0 || f.ArriveAt != 0 {
		t.Fatalf("f1 = %+v", f)
	}
	_, err = tr.Arrive(3750, "f1")
	wantErr(t, err, ErrNotArriving)
	_, err = tr.Arrive(0, "zz")
	wantErr(t, err, ErrUnknownFerry)
	_, err = tr.Depart(5249, "f1", 3)
	wantErr(t, err, ErrNotReady)
	at, err = tr.Depart(5250, "f1", 3)
	if err != nil || at != 5250+5250 {
		t.Fatalf("Depart = %d, %v", at, err)
	}
	// f2 は 1.2 倍。
	at, err = tr.Depart(0, "f2", 2)
	if err != nil || at != 6300 {
		t.Fatalf("Depart f2 = %d, %v", at, err)
	}
	mustInv(t, tr)
}

func TestArriveDelivers(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	c, _ := post(t, tr, 0, 1,
		msg(pb.MsgApp, 1, 2, 1),
		msg(pb.MsgApp, 1, 3, 1),
		msg(pb.MsgVote, 1, 2, 1),
	)
	if err := tr.Load("f1", []LetterID{c[2], c[1], c[0]}); err != nil {
		t.Fatal(err)
	}
	at, _ := tr.Depart(0, "f1", 2)
	got, err := tr.Arrive(at, "f1")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "delivered", got, []LetterID{c[2], c[0]})
	for _, id := range got {
		if l := loc(t, tr, id); l != (Location{Kind: LocDelivered, Island: 2}) {
			t.Fatalf("letter %d location %+v", id, l)
		}
	}
	f, _ := tr.Ferry("f1")
	wantIDs(t, "cargo", f.Cargo, []LetterID{c[1]})
	mustInv(t, tr)
	at, _ = tr.Depart(at+1500, "f1", 3)
	got, err = tr.Arrive(at, "f1")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "delivered", got, []LetterID{c[1]})
	if l := loc(t, tr, c[1]); l != (Location{Kind: LocDelivered, Island: 3}) {
		t.Fatalf("location %+v", l)
	}
	mustInv(t, tr)
}

func TestLoad(t *testing.T) {
	t.Parallel()
	// 港 1: c[0..4]（1→2 の App 4 通 + 1→3 の App）。港 2: other。
	setup := func(t *testing.T) (*Transport, []LetterID, map[string]LetterID) {
		tr := newT(t, baseConfig())
		c, _ := post(t, tr, 0, 1,
			msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1),
			msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1),
		)
		o, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 1, 1))
		hb, _ := post(t, tr, 0, 1, msg(pb.MsgHeartbeat, 1, 2, 1), msg(pb.MsgHeartbeat, 1, 2, 1))
		if err := tr.Load("f2", nil); err == nil {
			t.Fatal("expected error")
		}
		// 配達済みの手紙を作る: f2 は 3 にいるので、3→2 の手紙を積んで 2 に運ぶ。
		dl, _ := post(t, tr, 0, 3, msg(pb.MsgApp, 3, 2, 1), msg(pb.MsgApp, 3, 2, 1))
		if err := tr.Load("f2", []LetterID{dl[0]}); err != nil {
			t.Fatal(err)
		}
		at, _ := tr.Depart(0, "f2", 2)
		if _, err := tr.Arrive(at, "f2"); err != nil {
			t.Fatal(err)
		}
		return tr, c, map[string]LetterID{"other": o[0], "dropped": hb[0], "delivered": dl[0], "loaded": dl[1]}
	}

	tests := []struct {
		name    string
		letters func(c []LetterID, x map[string]LetterID) []LetterID
		want    error
	}{
		{"empty", func(c []LetterID, _ map[string]LetterID) []LetterID { return nil }, ErrInvalidArgument},
		{"duplicate", func(c []LetterID, _ map[string]LetterID) []LetterID { return []LetterID{c[0], c[0]} }, ErrInvalidArgument},
		{"other port", func(c []LetterID, x map[string]LetterID) []LetterID { return []LetterID{c[0], x["other"]} }, ErrLetterNotHere},
		{"dropped", func(c []LetterID, x map[string]LetterID) []LetterID { return []LetterID{c[0], x["dropped"]} }, ErrLetterNotHere},
		{"delivered", func(c []LetterID, x map[string]LetterID) []LetterID { return []LetterID{c[0], x["delivered"]} }, ErrLetterNotHere},
		{"missing", func(c []LetterID, _ map[string]LetterID) []LetterID { return []LetterID{c[0], 999} }, ErrLetterNotHere},
		{"zero", func(c []LetterID, _ map[string]LetterID) []LetterID { return []LetterID{0} }, ErrLetterNotHere},
		{"capacity", func(c []LetterID, _ map[string]LetterID) []LetterID { return c }, ErrCapacityExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, c, x := setup(t)
			before := tr.Port(1)
			err := tr.Load("f1", tt.letters(c, x))
			wantErr(t, err, tt.want)
			wantIDs(t, "port", tr.Port(1), before)
			if f, _ := tr.Ferry("f1"); len(f.Cargo) != 0 {
				t.Fatalf("cargo = %v", f.Cargo)
			}
			mustInv(t, tr)
		})
	}

	t.Run("already loaded", func(t *testing.T) {
		t.Parallel()
		tr, c, _ := setup(t)
		if err := tr.Load("f1", []LetterID{c[1]}); err != nil {
			t.Fatal(err)
		}
		wantErr(t, tr.Load("f1", []LetterID{c[0], c[1]}), ErrLetterNotHere)
	})

	t.Run("sailing", func(t *testing.T) {
		t.Parallel()
		tr, c, _ := setup(t)
		if _, err := tr.Depart(0, "f1", 2); err != nil {
			t.Fatal(err)
		}
		wantErr(t, tr.Load("f1", []LetterID{c[0]}), ErrNotDocked)
		wantErr(t, tr.Load("zz", []LetterID{c[0]}), ErrUnknownFerry)
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		tr, c, _ := setup(t)
		// f1 を 2 往復させて ReadyAt > now の状態にする。
		at, _ := tr.Depart(0, "f1", 2)
		if _, err := tr.Arrive(at, "f1"); err != nil {
			t.Fatal(err)
		}
		at, _ = tr.Depart(at+1500, "f1", 1)
		if _, err := tr.Arrive(at, "f1"); err != nil {
			t.Fatal(err)
		}
		if f, _ := tr.Ferry("f1"); f.ReadyAt <= at {
			t.Fatalf("ReadyAt = %d", f.ReadyAt)
		}
		if err := tr.Load("f1", []LetterID{c[4], c[0]}); err != nil {
			t.Fatal(err)
		}
		f, _ := tr.Ferry("f1")
		wantIDs(t, "cargo", f.Cargo, []LetterID{c[4], c[0]})
		for _, id := range []LetterID{c[4], c[0]} {
			if l := loc(t, tr, id); l != (Location{Kind: LocFerry, Ferry: "f1"}) {
				t.Fatalf("location %+v", l)
			}
		}
		for _, id := range tr.Port(1) {
			if id == c[0] || id == c[4] {
				t.Fatalf("letter %d still in port", id)
			}
		}
		mustInv(t, tr)
	})
}

func TestUnload(t *testing.T) {
	t.Parallel()

	t.Run("returns to ordered position", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, baseConfig())
		c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 2, 1))
		if err := tr.Load("f1", []LetterID{c[1], c[0]}); err != nil {
			t.Fatal(err)
		}
		d, err := tr.Unload("f1", []LetterID{c[1], c[0]})
		if err != nil || len(d) != 0 {
			t.Fatalf("Unload: %v %v", d, err)
		}
		wantIDs(t, "port", tr.Port(1), c)
		mustInv(t, tr)
	})

	t.Run("keeps remaining order", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, baseConfig())
		c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 2, 1))
		if err := tr.Load("f1", []LetterID{c[2], c[0], c[1]}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Unload("f1", []LetterID{c[0]}); err != nil {
			t.Fatal(err)
		}
		f, _ := tr.Ferry("f1")
		wantIDs(t, "cargo", f.Cargo, []LetterID{c[2], c[1]})
	})

	t.Run("superseded on return", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, baseConfig())
		old, _ := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
		if err := tr.Load("f1", []LetterID{old}); err != nil {
			t.Fatal(err)
		}
		newer, _ := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
		d, err := tr.Unload("f1", []LetterID{old})
		if err != nil {
			t.Fatal(err)
		}
		wantDrops(t, d, []Drop{{Letter: old, Reason: DropSuperseded, Rule: "R1"}})
		wantIDs(t, "port", tr.Port(1), []LetterID{newer})
		mustInv(t, tr)
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, baseConfig())
		c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1))
		_, err := tr.Unload("f1", []LetterID{c[0]})
		wantErr(t, err, ErrLetterNotHere)
		if err := tr.Load("f1", []LetterID{c[1]}); err != nil {
			t.Fatal(err)
		}
		_, err = tr.Unload("f1", nil)
		wantErr(t, err, ErrInvalidArgument)
		_, err = tr.Unload("f1", []LetterID{c[1], c[1]})
		wantErr(t, err, ErrInvalidArgument)
		_, err = tr.Unload("zz", []LetterID{c[1]})
		wantErr(t, err, ErrUnknownFerry)
		at, _ := tr.Depart(0, "f1", 2)
		_, err = tr.Unload("f1", []LetterID{c[1]})
		wantErr(t, err, ErrNotDocked)
		if _, err := tr.Arrive(at, "f1"); err != nil {
			t.Fatal(err)
		}
		_, err = tr.Unload("f1", []LetterID{c[1]})
		wantErr(t, err, ErrRelayNotSupported)
		f, _ := tr.Ferry("f1")
		wantIDs(t, "cargo", f.Cargo, []LetterID{c[1]})
		mustInv(t, tr)
	})
}
