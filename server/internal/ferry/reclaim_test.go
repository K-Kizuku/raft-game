package ferry

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

// reclaimSetup は 1→2 の Heartbeat を 2 通置き（1 通目が R1 で破棄）、1→2 の MsgApp を f1 で 2 に
// 配達した状態を作る。破棄した手紙・港の Heartbeat・配達した手紙の ID と、配達した手紙の Raft を返す。
func reclaimSetup(t *testing.T) (tr *Transport, dropped, kept, delivered LetterID, app *pb.Message) {
	t.Helper()
	tr = newT(t, baseConfig())
	dropped, _ = post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
	kept, d := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
	wantDrops(t, d, []Drop{{Letter: dropped, Reason: DropSuperseded, Rule: "R1"}})
	app = msg(pb.MsgApp, 1, 2, 1)
	delivered, _ = post1(t, tr, app)
	if err := tr.Load("f1", []LetterID{delivered}); err != nil {
		t.Fatal(err)
	}
	at, err := tr.Depart(0, "f1", 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Arrive(at, "f1")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "delivered", got, []LetterID{delivered})
	mustInv(t, tr)
	return tr, dropped, kept, delivered, app
}

// C-1
func TestReclaimBasic(t *testing.T) {
	t.Parallel()
	tr, dropped, kept, delivered, _ := reclaimSetup(t)
	port, ferries, routes := tr.Port(1), tr.Ferries(), tr.Routes()
	if n := tr.Reclaim(); n != 2 {
		t.Fatalf("Reclaim = %d, want 2", n)
	}
	for _, id := range []LetterID{dropped, delivered} {
		if _, ok := tr.Letter(id); ok {
			t.Fatalf("letter %d still found", id)
		}
	}
	if l := loc(t, tr, kept); l != (Location{Kind: LocPort, Island: 1}) {
		t.Fatalf("kept letter at %+v", l)
	}
	wantIDs(t, "Port(1)", tr.Port(1), port)
	if got := tr.Ferries(); !reflect.DeepEqual(got, ferries) {
		t.Fatalf("Ferries = %+v, want %+v", got, ferries)
	}
	if got := tr.Routes(); !slices.Equal(got, routes) {
		t.Fatalf("Routes = %+v, want %+v", got, routes)
	}
	mustInv(t, tr)
	if n := tr.Reclaim(); n != 0 {
		t.Fatalf("second Reclaim = %d, want 0", n)
	}
	if n := newT(t, baseConfig()).Reclaim(); n != 0 {
		t.Fatalf("Reclaim on new Transport = %d, want 0", n)
	}
}

// C-2: 手放した最大の ID のあとも、ID は戻らない。
func TestReclaimKeepsIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		next func(t *testing.T, tr *Transport) LetterID
	}{
		{"PostRaft", func(t *testing.T, tr *Transport) LetterID {
			id, _ := post1(t, tr, msg(pb.MsgApp, 1, 2, 1))
			return id
		}},
		{"PostRequest", func(t *testing.T, tr *Transport) LetterID {
			id, _, err := tr.PostRequest(0, 1, 2, RequestNote{ReqID: "r"})
			if err != nil {
				t.Fatal(err)
			}
			return id
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			cfg.PortCap = 6
			tr := newT(t, cfg)
			for term := uint64(6); term >= 1; term-- {
				if _, d := post1(t, tr, msg(pb.MsgVote, 1, 2, term)); len(d) != 0 {
					t.Fatalf("vote term %d: drops %+v", term, d)
				}
			}
			hb, d := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
			wantDrops(t, d, []Drop{{Letter: hb, Reason: DropOverflow, Rule: "R5"}})
			if l := loc(t, tr, hb); l.Kind != LocDropped {
				t.Fatalf("heartbeat at %+v", l)
			}
			if n := tr.Reclaim(); n != 1 {
				t.Fatalf("Reclaim = %d, want 1", n)
			}
			if _, ok := tr.Letter(hb); ok {
				t.Fatal("reclaimed heartbeat still found")
			}
			if id := tt.next(t, tr); id != hb+1 {
				t.Fatalf("next id = %d, want %d", id, hb+1)
			}
			mustInv(t, tr)
		})
	}
}

// C-3: Reclaim の前は、Engine がイベントを作るために終わった手紙を引ける。
func TestTerminalLetterBeforeReclaim(t *testing.T) {
	t.Parallel()
	tr, dropped, _, delivered, app := reclaimSetup(t)
	if l := loc(t, tr, dropped); l != (Location{Kind: LocDropped}) {
		t.Fatalf("dropped letter at %+v", l)
	}
	l, ok := tr.Letter(delivered)
	if !ok {
		t.Fatal("delivered letter not found")
	}
	if l.Location != (Location{Kind: LocDelivered, Island: 2}) {
		t.Fatalf("delivered letter at %+v", l.Location)
	}
	if l.Raft != app {
		t.Fatal("delivered letter Raft is not the posted pointer")
	}
}

// C-4: 手放した ID・0・振っていない ID は、どれも ErrLetterNotHere になり、文は手放す前と同じ。
func TestReclaimErrorsUnchanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(t *testing.T, tr *Transport, dropped, delivered LetterID) error
	}{
		{"load dropped", func(t *testing.T, tr *Transport, dropped, _ LetterID) error {
			before := tr.Load("f2", []LetterID{dropped}) // f2 は島 3。破棄した手紙はどこの港にもない
			before1 := tr.Load("f1", []LetterID{dropped})
			wantErr(t, before, ErrLetterNotHere)
			wantErr(t, before1, ErrLetterNotHere)
			tr.Reclaim()
			after := tr.Load("f2", []LetterID{dropped})
			after1 := tr.Load("f1", []LetterID{dropped})
			if before.Error() != after.Error() || before1.Error() != after1.Error() {
				t.Fatalf("errors differ: %q/%q vs %q/%q", before, before1, after, after1)
			}
			return after1
		}},
		{"load 0", func(_ *testing.T, tr *Transport, _, _ LetterID) error {
			tr.Reclaim()
			return tr.Load("f2", []LetterID{0})
		}},
		{"load lastID+1", func(_ *testing.T, tr *Transport, _, _ LetterID) error {
			tr.Reclaim()
			return tr.Load("f2", []LetterID{tr.lastID + 1})
		}},
		{"unload reclaimed", func(_ *testing.T, tr *Transport, _, delivered LetterID) error {
			tr.Reclaim()
			_, err := tr.Unload("f1", []LetterID{delivered})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, dropped, _, delivered, _ := reclaimSetup(t)
			// f1 を島 1 に戻し、島 1 の港で Load を試せるようにする。
			at, err := tr.Depart(tr.ferries[0].ReadyAt, "f1", 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tr.Arrive(at, "f1"); err != nil {
				t.Fatal(err)
			}
			err = tt.run(t, tr, dropped, delivered)
			if !errors.Is(err, ErrLetterNotHere) {
				t.Fatalf("err = %v, want %v", err, ErrLetterNotHere)
			}
			mustInv(t, tr)
		})
	}
}

// C-5: 1 種類の手紙を 10 万通流し、100 通ごとに Reclaim する。
func TestReclaimLongRun(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	tests := []struct {
		name string
		typ  pb.MessageType
		live int // Reclaim の直後に持つ手紙の数
	}{
		{"heartbeat", pb.MsgHeartbeat, 1},
		{"app", pb.MsgApp, cfg.PerLinkAppCap},
	}
	const total, every = 100000, 100
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, cfg)
			for i := 1; i <= total; i++ {
				if _, _, err := tr.PostRaft(int64(i), 1, []*pb.Message{msg(tt.typ, 1, 2, 1)}); err != nil {
					t.Fatal(err)
				}
				if n := len(tr.letters); n > tt.live+every {
					t.Fatalf("post %d: %d letters, want <= %d", i, n, tt.live+every)
				}
				if i%every == 0 {
					tr.Reclaim()
					if n := len(tr.letters); n != tt.live {
						t.Fatalf("post %d: %d letters after Reclaim, want %d", i, n, tt.live)
					}
				}
				if i%1000 == 0 {
					mustInv(t, tr)
				}
			}
			if tr.lastID != total {
				t.Fatalf("lastID = %d, want %d", tr.lastID, total)
			}
		})
	}
}

// C-8: Reclaim のあと、スライスの末尾に手放した手紙（*pb.Message）への参照が残らない。
func TestReclaimClearsTail(t *testing.T) {
	t.Parallel()
	tr, _, _, _, _ := reclaimSetup(t)
	post(t, tr, 0, 1, msg(pb.MsgVote, 1, 3, 2), msg(pb.MsgVote, 1, 3, 3)) // 1 通目が R2 で破棄
	if _, _, err := tr.PostRequest(0, 1, 3, RequestNote{ReqID: "x"}); err != nil {
		t.Fatal(err)
	}
	if n := tr.Reclaim(); n != 3 {
		t.Fatalf("Reclaim = %d, want 3", n)
	}
	tail := tr.letters[len(tr.letters):cap(tr.letters)]
	if len(tail) < 3 {
		t.Fatalf("tail has %d elements, want >= 3", len(tail))
	}
	for i, l := range tail {
		if l != (Letter{}) {
			t.Fatalf("tail[%d] = %+v, want zero", i, l)
		}
	}
	mustInv(t, tr)
}
