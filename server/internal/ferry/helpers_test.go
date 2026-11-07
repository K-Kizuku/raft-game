package ferry

import (
	"errors"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func u64(v uint64) *uint64 { return &v }
func bptr(v bool) *bool    { return &v }

func msg(typ pb.MessageType, from, to, term uint64) *pb.Message {
	return &pb.Message{Type: typ.Enum(), From: u64(from), To: u64(to), Term: u64(term)}
}

func rejectResp(from, to, term uint64) *pb.Message {
	m := msg(pb.MsgAppResp, from, to, term)
	m.Reject = bptr(true)
	return m
}

func baseConfig() Config {
	return Config{
		Islands: []uint64{1, 2, 3},
		Routes:  []RouteConfig{{A: 1, B: 2, TravelMs: 3750}, {A: 2, B: 3, TravelMs: 5250}},
		Ferries: []FerryConfig{
			{ID: "f1", Name: "One", Home: 1, Capacity: 4, SpeedPermille: 1000},
			{ID: "f2", Name: "Two", Home: 3, Capacity: 2, SpeedPermille: 1200},
		},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
		RelayCap:      0, // 積み替えなし（runRandom の上限は港・船だけを数える）。倉庫のテストは relayConfig を使う
	}
}

func newT(t *testing.T, cfg Config) *Transport {
	t.Helper()
	tr, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func mustInv(t *testing.T, tr *Transport) {
	t.Helper()
	if err := tr.CheckInvariants(); err != nil {
		t.Fatalf("CheckInvariants: %v", err)
	}
}

func post(t *testing.T, tr *Transport, now int64, from uint64, msgs ...*pb.Message) ([]LetterID, []Drop) {
	t.Helper()
	c, d, err := tr.PostRaft(now, from, msgs)
	if err != nil {
		t.Fatalf("PostRaft: %v", err)
	}
	mustInv(t, tr)
	return c, d
}

func post1(t *testing.T, tr *Transport, m *pb.Message) (LetterID, []Drop) {
	t.Helper()
	c, d := post(t, tr, 0, m.GetFrom(), m)
	return c[0], d
}

func wantDrops(t *testing.T, got, want []Drop) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("drops = %+v, want %+v", got, want)
	}
}

func wantIDs(t *testing.T, name string, got, want []LetterID) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
}

func loc(t *testing.T, tr *Transport, id LetterID) Location {
	t.Helper()
	l, ok := tr.Letter(id)
	if !ok {
		t.Fatalf("letter %d not found", id)
	}
	return l.Location
}
