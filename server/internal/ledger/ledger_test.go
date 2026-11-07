package ledger

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"
)

func testDefs() []ResourceDef {
	return []ResourceDef{
		{ID: "firework:a", Kind: ResourceFirework},
		{ID: "firework:b", Kind: ResourceFirework},
		{ID: "stall:main", Kind: ResourceStall, Capacity: 2},
		{ID: "stall:side", Kind: ResourceStall, Capacity: 3},
	}
}

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := New(testDefs())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func cmd(reqID string, origin uint64, kind, resource string) []byte {
	c := Command{V: Version, ReqID: reqID, Origin: origin, Kind: kind, Resource: resource}
	b, err := Encode(c)
	if err != nil {
		panic(err)
	}
	return b
}

func fw(reqID, resource string) []byte    { return cmd(reqID, 1, KindFireworkReserve, resource) }
func stall(reqID, resource string) []byte { return cmd(reqID, 1, KindStallReserve, resource) }

func ok(reqID string) Result { return Result{ReqID: reqID, Status: StatusOK} }
func rejected(reqID, reason string) Result {
	return Result{ReqID: reqID, Status: StatusRejected, Reason: reason}
}
func duplicate(reqID string) Result { return Result{ReqID: reqID, Status: StatusDuplicate} }

func mustApply(t *testing.T, l *Ledger, index uint64, data []byte, want Result) {
	t.Helper()
	if got := l.Apply(index, data); got != want {
		t.Fatalf("Apply(%d, %s) = %+v, want %+v", index, data, got, want)
	}
	if err := l.CheckInvariants(); err != nil {
		t.Fatalf("CheckInvariants after Apply(%d): %v", index, err)
	}
}

func holders(t *testing.T, l *Ledger, id string) []string {
	t.Helper()
	for _, r := range l.Snapshot().Resources {
		if r.ID == id {
			return r.Holders
		}
	}
	t.Fatalf("resource %q not found", id)
	return nil
}

// L-1
func TestNewValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		defs    []ResourceDef
		wantErr bool
	}{
		{"valid", testDefs(), false},
		{"nil", nil, false},
		{"empty", []ResourceDef{}, false},
		{"empty id", []ResourceDef{{ID: "", Kind: ResourceFirework}}, true},
		{"duplicate id", []ResourceDef{{ID: "x", Kind: ResourceFirework}, {ID: "x", Kind: ResourceStall, Capacity: 1}}, true},
		{"kind budget", []ResourceDef{{ID: "budget:main", Kind: "budget"}}, true},
		{"kind empty", []ResourceDef{{ID: "x", Kind: ""}}, true},
		{"kind x", []ResourceDef{{ID: "x", Kind: "x"}}, true},
		{"firework capacity 1", []ResourceDef{{ID: "firework:a", Kind: ResourceFirework, Capacity: 1}}, true},
		{"stall capacity 0", []ResourceDef{{ID: "stall:a", Kind: ResourceStall, Capacity: 0}}, true},
		{"stall capacity -1", []ResourceDef{{ID: "stall:a", Kind: ResourceStall, Capacity: -1}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, err := New(tt.defs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && l == nil {
				t.Fatal("New returned nil ledger without error")
			}
		})
	}
}

func TestNewCopiesDefs(t *testing.T) {
	t.Parallel()
	defs := testDefs()
	l, err := New(defs)
	if err != nil {
		t.Fatal(err)
	}
	defs[0].ID = "changed"
	defs[2].Capacity = 100
	s := l.Snapshot()
	if s.Resources[0].ID != "firework:a" || s.Resources[2].Capacity != 2 {
		t.Fatalf("ledger changed with defs: %+v", s.Resources)
	}
}

func TestEmptyDefsRejectsAll(t *testing.T) {
	t.Parallel()
	l, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, l, 1, fw("r1", "firework:a"), rejected("r1", ReasonUnknownResource))
	mustApply(t, l, 2, stall("r2", "stall:main"), rejected("r2", ReasonUnknownResource))
}

// L-2
func TestFirework(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	mustApply(t, l, 1, fw("r1", "firework:a"), ok("r1"))
	mustApply(t, l, 2, fw("r2", "firework:a"), rejected("r2", ReasonAlreadyReserved))
	mustApply(t, l, 3, fw("r1", "firework:a"), duplicate("r1"))
	if got := holders(t, l, "firework:a"); !reflect.DeepEqual(got, []string{"r1"}) {
		t.Fatalf("Holders = %v, want [r1]", got)
	}
	// 別の花火は独立して予約できる。
	mustApply(t, l, 4, fw("r3", "firework:b"), ok("r3"))
}

// L-3
func TestStall(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	mustApply(t, l, 1, stall("r1", "stall:main"), ok("r1"))
	mustApply(t, l, 2, stall("r2", "stall:main"), ok("r2"))
	mustApply(t, l, 3, stall("r3", "stall:main"), rejected("r3", ReasonFull))
	mustApply(t, l, 4, stall("r3", "stall:main"), duplicate("r3"))
	if got := holders(t, l, "stall:main"); !reflect.DeepEqual(got, []string{"r1", "r2"}) {
		t.Fatalf("Holders = %v, want [r1 r2]", got)
	}
}

func TestStallSameOriginManyTimes(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	for i := range 3 {
		id := "r" + strconv.Itoa(i)
		mustApply(t, l, uint64(i+1), cmd(id, 5, KindStallReserve, "stall:side"), ok(id))
	}
	mustApply(t, l, 4, cmd("r9", 5, KindStallReserve, "stall:side"), rejected("r9", ReasonFull))
}

func TestReserveIgnoresAmount(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	b, err := Encode(Command{V: 1, ReqID: "r1", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a", Amount: 99})
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, l, 1, b, ok("r1"))
}

// L-4
func TestDuplicateDoesNotChangeState(t *testing.T) {
	t.Parallel()
	for _, first := range []struct {
		name string
		data []byte
	}{
		{"after ok", stall("r1", "stall:main")},
		{"after rejected", fw("r1", "firework:zzz")},
	} {
		t.Run(first.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			l.Apply(1, first.data)
			before := l.Snapshot()
			recBefore, _ := l.Lookup("r1")
			// 同じ reqId で、別の資源・種類・origin を指しても duplicate。
			for i, data := range [][]byte{
				first.data,
				fw("r1", "firework:a"),
				cmd("r1", 9, KindStallReserve, "stall:side"),
				rawJSON(Command{V: 2, ReqID: "r1", Origin: 0, Kind: "budget.spend"}),
			} {
				mustApply(t, l, uint64(i+2), data, duplicate("r1"))
			}
			after := l.Snapshot()
			if after.AppliedIndex != 5 {
				t.Fatalf("AppliedIndex = %d, want 5", after.AppliedIndex)
			}
			after.AppliedIndex = before.AppliedIndex
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("Snapshot changed by duplicate:\nbefore %+v\nafter  %+v", before, after)
			}
			recAfter, _ := l.Lookup("r1")
			if recBefore != recAfter {
				t.Fatalf("Lookup changed: %+v -> %+v", recBefore, recAfter)
			}
		})
	}
}

// L-5
func TestRejectReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		c      Command
		reason string
	}{
		{"v2", Command{V: 2, ReqID: "x", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a"}, ReasonUnsupportedVersion},
		{"v0", Command{V: 0, ReqID: "x", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a"}, ReasonUnsupportedVersion},
		{"origin0", Command{V: 1, ReqID: "x", Origin: 0, Kind: KindFireworkReserve, Resource: "firework:a"}, ReasonInvalidCommand},
		{"budget.spend", Command{V: 1, ReqID: "x", Origin: 1, Kind: "budget.spend", Resource: "budget:main", Amount: 10}, ReasonUnknownKind},
		{"kind x", Command{V: 1, ReqID: "x", Origin: 1, Kind: "x", Resource: "firework:a"}, ReasonUnknownKind},
		{"kind empty", Command{V: 1, ReqID: "x", Origin: 1, Kind: "", Resource: "firework:a"}, ReasonUnknownKind},
		{"unknown resource", Command{V: 1, ReqID: "x", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:zzz"}, ReasonUnknownResource},
		{"firework on stall", Command{V: 1, ReqID: "x", Origin: 1, Kind: KindFireworkReserve, Resource: "stall:main"}, ReasonResourceKindMismatch},
		{"stall on firework", Command{V: 1, ReqID: "x", Origin: 1, Kind: KindStallReserve, Resource: "firework:a"}, ReasonResourceKindMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			// Encode は V・Origin・Kind を検査するので、json で直接作る。
			data := rawJSON(tt.c)
			mustApply(t, l, 1, data, rejected("x", tt.reason))
			rec, found := l.Lookup("x")
			if !found {
				t.Fatal("rejected command is not recorded")
			}
			want := Record{Index: 1, Origin: tt.c.Origin, Kind: tt.c.Kind, Resource: tt.c.Resource, Result: rejected("x", tt.reason)}
			if rec != want {
				t.Fatalf("Record = %+v, want %+v", rec, want)
			}
			if n := len(l.Snapshot().Records); n != 1 {
				t.Fatalf("len(Records) = %d, want 1", n)
			}
			mustApply(t, l, 2, data, duplicate("x"))
			// 資源は消費されていない。
			for _, r := range l.Snapshot().Resources {
				if len(r.Holders) != 0 {
					t.Fatalf("resource %s consumed: %v", r.ID, r.Holders)
				}
			}
		})
	}
}

// rawJSON は Encode の検査を通さずにコマンドを JSON にする。
func rawJSON(c Command) []byte {
	return []byte(`{"v":` + strconv.Itoa(c.V) +
		`,"reqId":` + strconv.Quote(c.ReqID) +
		`,"origin":` + strconv.FormatUint(c.Origin, 10) +
		`,"kind":` + strconv.Quote(c.Kind) +
		`,"resource":` + strconv.Quote(c.Resource) +
		`,"amount":` + strconv.FormatInt(c.Amount, 10) + `}`)
}

// L-6
func TestRejectWithoutRecord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"broken", []byte(`{"v":1,"reqId":`)},
		{"null", []byte("null")},
		{"empty reqId", rawJSON(Command{V: 1, ReqID: "", Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a"})},
		{"no reqId", []byte(`{"v":1,"origin":1,"kind":"firework.reserve","resource":"firework:a"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			mustApply(t, l, 1, fw("r1", "firework:a"), ok("r1"))
			mustApply(t, l, 4, tt.data, Result{Status: StatusRejected, Reason: ReasonInvalidCommand})
			s := l.Snapshot()
			if len(s.Records) != 1 {
				t.Fatalf("len(Records) = %d, want 1", len(s.Records))
			}
			if s.AppliedIndex != 4 {
				t.Fatalf("AppliedIndex = %d, want 4", s.AppliedIndex)
			}
		})
	}
}

func expectPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	f()
}

// L-7
func TestApplyIndex(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	if l.AppliedIndex() != 0 {
		t.Fatalf("initial AppliedIndex = %d", l.AppliedIndex())
	}
	mustApply(t, l, 1, fw("r1", "firework:a"), ok("r1"))
	mustApply(t, l, 5, stall("r2", "stall:main"), ok("r2"))
	if l.AppliedIndex() != 5 {
		t.Fatalf("AppliedIndex = %d, want 5", l.AppliedIndex())
	}
	before := l.Snapshot()
	for _, idx := range []uint64{5, 3, 0} {
		expectPanic(t, func() { l.Apply(idx, stall("r3", "stall:main")) })
		if after := l.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("Snapshot changed by panicking Apply(%d)", idx)
		}
	}

	var zero Ledger
	zeroBefore := zero.Snapshot()
	expectPanic(t, func() { zero.Apply(0, fw("r1", "firework:a")) })
	if !reflect.DeepEqual(zeroBefore, zero.Snapshot()) {
		t.Fatal("zero ledger changed by panicking Apply(0)")
	}
	fresh := newTestLedger(t)
	expectPanic(t, func() { fresh.Apply(0, fw("r1", "firework:a")) })
	if fresh.AppliedIndex() != 0 {
		t.Fatal("fresh ledger changed by panicking Apply(0)")
	}
}

// L-8
func TestLookup(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	mustApply(t, l, 3, cmd("r1", 2, KindFireworkReserve, "firework:a"), ok("r1"))
	mustApply(t, l, 4, cmd("r2", 4, KindFireworkReserve, "firework:a"), rejected("r2", ReasonAlreadyReserved))
	mustApply(t, l, 7, cmd("r1", 2, KindFireworkReserve, "firework:a"), duplicate("r1"))

	tests := []struct {
		reqID string
		want  Record
	}{
		{"r1", Record{Index: 3, Origin: 2, Kind: KindFireworkReserve, Resource: "firework:a", Result: ok("r1")}},
		{"r2", Record{Index: 4, Origin: 4, Kind: KindFireworkReserve, Resource: "firework:a", Result: rejected("r2", ReasonAlreadyReserved)}},
	}
	for _, tt := range tests {
		got, found := l.Lookup(tt.reqID)
		if !found || got != tt.want {
			t.Fatalf("Lookup(%q) = %+v, %v; want %+v, true", tt.reqID, got, found, tt.want)
		}
	}
	if _, found := l.Lookup("r3"); found {
		t.Fatal("Lookup(r3) found an unrecorded reqId")
	}
	if _, found := l.Lookup(""); found {
		t.Fatal(`Lookup("") found a record`)
	}
}

// L-9
func TestOrderDecides(t *testing.T) {
	t.Parallel()
	a, b := newTestLedger(t), newTestLedger(t)
	r1, r2 := cmd("r1", 1, KindFireworkReserve, "firework:a"), cmd("r2", 2, KindFireworkReserve, "firework:a")

	mustApply(t, a, 1, r1, ok("r1"))
	mustApply(t, a, 2, r2, rejected("r2", ReasonAlreadyReserved))

	mustApply(t, b, 1, r2, ok("r2"))
	mustApply(t, b, 2, r1, rejected("r1", ReasonAlreadyReserved))

	if got := holders(t, a, "firework:a"); !reflect.DeepEqual(got, []string{"r1"}) {
		t.Fatalf("a Holders = %v", got)
	}
	if got := holders(t, b, "firework:a"); !reflect.DeepEqual(got, []string{"r2"}) {
		t.Fatalf("b Holders = %v", got)
	}
}

// L-10
func TestReplicasConverge(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	resources := []string{"firework:a", "firework:b", "stall:main", "stall:side", "firework:zzz", "stall:none", ""}
	kinds := []string{KindFireworkReserve, KindStallReserve, "budget.spend", "x"}
	broken := [][]byte{nil, []byte("null"), []byte(`{"v":1`), []byte(`{"v":1,"reqId":"r1","x":1}`), []byte("[]")}

	const n = 3
	ls := make([]*Ledger, n)
	for i := range ls {
		ls[i] = newTestLedger(t)
	}
	var index uint64
	var okCount int
	for range 500 {
		index += 1 + uint64(rng.IntN(3)) // index は飛ぶことがある
		var data []byte
		switch p := rng.IntN(20); {
		case p == 0:
			data = broken[rng.IntN(len(broken))]
		case p == 1:
			data = rawJSON(Command{V: rng.IntN(3), ReqID: "r" + strconv.Itoa(rng.IntN(50)), Origin: uint64(rng.IntN(3)), Kind: KindStallReserve, Resource: "stall:side"})
		default:
			data = cmd("r"+strconv.Itoa(rng.IntN(50)), uint64(1+rng.IntN(5)), kinds[rng.IntN(len(kinds))], resources[rng.IntN(len(resources))])
		}
		want := ls[0].Apply(index, data)
		if want.Status == StatusOK {
			okCount++
		}
		for i := 1; i < n; i++ {
			if got := ls[i].Apply(index, data); got != want {
				t.Fatalf("index %d: ledger %d result %+v, ledger 0 %+v", index, i, got, want)
			}
		}
		s0 := ls[0].Snapshot()
		for i, l := range ls {
			if err := l.CheckInvariants(); err != nil {
				t.Fatalf("index %d: ledger %d: %v", index, i, err)
			}
			if i > 0 && !reflect.DeepEqual(s0, l.Snapshot()) {
				t.Fatalf("index %d: ledger %d snapshot differs", index, i)
			}
		}
	}
	if okCount == 0 {
		t.Fatal("no ok results; the generated sequence is too weak")
	}
	for _, r := range ls[0].Snapshot().Resources {
		limit := r.Capacity
		if r.Kind == ResourceFirework {
			limit = 1
		}
		if len(r.Holders) > limit {
			t.Fatalf("%s has %d holders, limit %d", r.ID, len(r.Holders), limit)
		}
	}
}

// L-11
func TestSnapshotDeepCopy(t *testing.T) {
	t.Parallel()
	empty := newTestLedger(t).Snapshot()
	if empty.Resources == nil || empty.Records == nil {
		t.Fatal("Snapshot of new ledger has nil slices")
	}
	for _, r := range empty.Resources {
		if r.Holders == nil {
			t.Fatalf("Holders of %s is nil", r.ID)
		}
	}
	if s, _ := New(nil); s.Snapshot().Resources == nil || s.Snapshot().Records == nil {
		t.Fatal("Snapshot of empty-defs ledger has nil slices")
	}

	l := newTestLedger(t)
	mustApply(t, l, 1, fw("r1", "firework:a"), ok("r1"))
	mustApply(t, l, 2, stall("r2", "stall:main"), ok("r2"))
	before := l.Snapshot()
	s := l.Snapshot()
	s.Resources[0].Holders[0] = "evil"
	s.Resources[2].Holders = append(s.Resources[2].Holders, "evil")
	s.Resources[1].ID = "evil"
	s.Records[0].Result.Status = "evil"
	s.Records = append(s.Records, Record{})
	if after := l.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("ledger changed through Snapshot:\nbefore %+v\nafter  %+v", before, after)
	}
	if err := l.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// L-12
func TestCheckInvariantsDetectsViolations(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) *Ledger {
		t.Helper()
		l := newTestLedger(t)
		mustApply(t, l, 1, fw("r1", "firework:a"), ok("r1"))
		mustApply(t, l, 2, stall("r2", "stall:main"), ok("r2"))
		mustApply(t, l, 3, stall("r3", "stall:main"), ok("r3"))
		mustApply(t, l, 4, fw("r4", "firework:a"), rejected("r4", ReasonAlreadyReserved))
		return l
	}
	const fwA, stallMain, stallSide = 0, 2, 3
	tests := []struct {
		name    string
		corrupt func(l *Ledger)
	}{
		{"firework two holders", func(l *Ledger) {
			l.resources[fwA].Holders = append(l.resources[fwA].Holders, "r4")
			l.records["r4"] = Record{Index: 4, Origin: 1, Kind: KindFireworkReserve, Resource: "firework:a", Result: ok("r4")}
		}},
		{"stall over capacity", func(l *Ledger) {
			l.resources[stallMain].Holders = append(l.resources[stallMain].Holders, "r4")
			l.records["r4"] = Record{Index: 4, Origin: 1, Kind: KindStallReserve, Resource: "stall:main", Result: ok("r4")}
		}},
		{"holder without record", func(l *Ledger) {
			l.resources[stallSide].Holders = append(l.resources[stallSide].Holders, "ghost")
		}},
		{"holder with rejected record", func(l *Ledger) {
			l.resources[stallSide].Holders = append(l.resources[stallSide].Holders, "r4")
		}},
		{"holder record on another resource", func(l *Ledger) {
			l.resources[stallSide].Holders = []string{"r2"}
			l.resources[stallMain].Holders = []string{"r3"}
		}},
		{"ok record not in holders", func(l *Ledger) {
			l.resources[stallMain].Holders = []string{"r2"}
		}},
		{"same reqId in two holders", func(l *Ledger) {
			l.resources[stallSide].Holders = []string{"r2"}
		}},
		{"same reqId twice in holders", func(l *Ledger) {
			l.resources[stallMain].Holders = []string{"r2", "r2"}
		}},
		{"record index exceeds applied", func(l *Ledger) {
			l.appliedIndex = 3
		}},
		{"record index not increasing", func(l *Ledger) {
			rec := l.records["r3"]
			rec.Index = 2
			l.records["r3"] = rec
		}},
		{"record missing from order", func(l *Ledger) {
			l.order = l.order[:len(l.order)-1]
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := setup(t)
			tt.corrupt(l)
			if err := l.CheckInvariants(); err == nil {
				t.Fatal("CheckInvariants = nil, want violation")
			}
		})
	}
}
