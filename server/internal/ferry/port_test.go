package ferry

import (
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func TestPortR1Heartbeat(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	a, d := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
	wantDrops(t, d, nil)
	c, d := post1(t, tr, msg(pb.MsgHeartbeat, 1, 3, 1))
	wantDrops(t, d, nil)
	r, d := post1(t, tr, msg(pb.MsgHeartbeatResp, 1, 2, 1))
	wantDrops(t, d, nil)
	b, d := post1(t, tr, msg(pb.MsgHeartbeat, 1, 2, 1))
	wantDrops(t, d, []Drop{{Letter: a, Reason: DropSuperseded, Rule: "R1"}})
	wantIDs(t, "port", tr.Port(1), []LetterID{c, r, b})
	if got := loc(t, tr, a); got.Kind != LocDropped {
		t.Fatalf("dropped letter location = %+v", got)
	}
}

func TestPortR2Election(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	v1, _ := post1(t, tr, msg(pb.MsgVote, 1, 2, 3))
	pv, _ := post1(t, tr, msg(pb.MsgPreVote, 1, 2, 3))
	v2, d := post1(t, tr, msg(pb.MsgVote, 1, 2, 3))
	wantDrops(t, d, []Drop{{Letter: v1, Reason: DropSuperseded, Rule: "R2"}})
	v3, d := post1(t, tr, msg(pb.MsgVote, 1, 2, 4))
	wantDrops(t, d, []Drop{{Letter: v2, Reason: DropSuperseded, Rule: "R2"}})
	wantIDs(t, "port", tr.Port(1), []LetterID{pv, v3})

	v5, _ := post1(t, tr, msg(pb.MsgVote, 1, 2, 5))
	v4, d := post1(t, tr, msg(pb.MsgVote, 1, 2, 4))
	wantDrops(t, d, nil)
	wantIDs(t, "port", tr.Port(1), []LetterID{pv, v5, v4})
}

// 降ろして戻した古い選挙の手紙は、港の新しい手紙（Term が同じ以上）に負けて破棄される。
func TestPortR2ReturnedOlderLoses(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	old, _ := post1(t, tr, msg(pb.MsgVote, 1, 2, 3))
	if err := tr.Load("f1", []LetterID{old}); err != nil {
		t.Fatal(err)
	}
	newer, _ := post1(t, tr, msg(pb.MsgVote, 1, 2, 3))
	d, err := tr.Unload("f1", []LetterID{old})
	if err != nil {
		t.Fatal(err)
	}
	wantDrops(t, d, []Drop{{Letter: old, Reason: DropSuperseded, Rule: "R2"}})
	wantIDs(t, "port", tr.Port(1), []LetterID{newer})
	mustInv(t, tr)
}

func TestPortR3AppResp(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	a, _ := post1(t, tr, msg(pb.MsgAppResp, 2, 1, 1))
	r1, _ := post1(t, tr, rejectResp(2, 1, 1))
	b, d := post1(t, tr, msg(pb.MsgAppResp, 2, 1, 1))
	wantDrops(t, d, []Drop{{Letter: a, Reason: DropSuperseded, Rule: "R3"}})
	r2, d := post1(t, tr, rejectResp(2, 1, 1))
	wantDrops(t, d, nil)
	r3, d := post1(t, tr, rejectResp(2, 1, 1))
	wantDrops(t, d, nil)
	wantIDs(t, "port", tr.Port(2), []LetterID{r1, b, r2, r3})
}

func TestPortR4App(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	var ids []LetterID
	for range 4 {
		id, d := post1(t, tr, msg(pb.MsgApp, 1, 2, 1))
		wantDrops(t, d, nil)
		ids = append(ids, id)
	}
	other, d := post1(t, tr, msg(pb.MsgApp, 1, 3, 1))
	wantDrops(t, d, nil)
	fifth, d := post1(t, tr, msg(pb.MsgApp, 1, 2, 1))
	wantDrops(t, d, []Drop{{Letter: ids[0], Reason: DropOverflow, Rule: "R4"}})
	wantIDs(t, "port", tr.Port(1), []LetterID{ids[1], ids[2], ids[3], other, fifth})
}

func TestPortR5Overflow(t *testing.T) {
	t.Parallel()
	small := func() Config {
		c := baseConfig()
		c.PortCap = 6
		return c
	}

	t.Run("lowest class oldest", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, small())
		c, _ := post(t, tr, 0, 1,
			msg(pb.MsgVote, 1, 2, 1),
			msg(pb.MsgApp, 1, 2, 1),
			msg(pb.MsgAppResp, 1, 2, 1),
			msg(pb.MsgHeartbeat, 1, 2, 1),
			msg(pb.MsgHeartbeatResp, 1, 3, 1),
			msg(pb.MsgApp, 1, 3, 1),
		)
		_, d := post(t, tr, 0, 1, msg(pb.MsgPreVote, 1, 3, 1))
		wantDrops(t, d, []Drop{{Letter: c[3], Reason: DropOverflow, Rule: "R5"}})
	})

	t.Run("other before heartbeat", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, small())
		c, _ := post(t, tr, 0, 1,
			msg(pb.MsgHeartbeat, 1, 2, 1),
			msg(pb.MsgApp, 1, 2, 1),
			msg(pb.MsgTimeoutNow, 1, 2, 1),
			msg(pb.MsgVote, 1, 2, 1),
			msg(pb.MsgAppResp, 1, 2, 1),
			msg(pb.MsgApp, 1, 3, 1),
		)
		_, d := post(t, tr, 0, 1, msg(pb.MsgVote, 1, 3, 1))
		wantDrops(t, d, []Drop{{Letter: c[2], Reason: DropOverflow, Rule: "R5"}})
	})

	t.Run("new letter itself dropped", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, small())
		post(t, tr, 0, 1,
			msg(pb.MsgVote, 1, 2, 5),
			msg(pb.MsgVote, 1, 3, 5),
			msg(pb.MsgPreVote, 1, 2, 5),
			msg(pb.MsgPreVote, 1, 3, 5),
			msg(pb.MsgVoteResp, 1, 2, 5),
			msg(pb.MsgPreVoteResp, 1, 2, 5),
		)
		c, d := post(t, tr, 0, 1, msg(pb.MsgHeartbeat, 1, 2, 5))
		wantDrops(t, d, []Drop{{Letter: c[0], Reason: DropOverflow, Rule: "R5"}})
		if got := loc(t, tr, c[0]); got.Kind != LocDropped {
			t.Fatalf("location = %+v", got)
		}
		if n := len(tr.Port(1)); n != 6 {
			t.Fatalf("port size = %d", n)
		}
	})
}

func TestPortR6Request(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.PortCap = 2
	tr := newT(t, cfg)
	post(t, tr, 0, 1, msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1))
	a, d, err := tr.PostRequest(10, 1, 2, RequestNote{ReqID: "r1"})
	if err != nil || len(d) != 0 {
		t.Fatalf("PostRequest: %v %v", d, err)
	}
	o, d, err := tr.PostRequest(10, 1, 3, RequestNote{ReqID: "r2", Hops: 1})
	if err != nil || len(d) != 0 {
		t.Fatalf("PostRequest: %v %v", d, err)
	}
	b, d, err := tr.PostRequest(11, 1, 2, RequestNote{ReqID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	wantDrops(t, d, []Drop{{Letter: a, Reason: DropSuperseded, Rule: "R6"}})
	mustInv(t, tr)
	if n := len(tr.Port(1)); n != 4 {
		t.Fatalf("port size = %d, want 4 (requests do not count for R5)", n)
	}
	l, _ := tr.Letter(o)
	if l.Kind != LetterRequest || l.Req.ReqID != "r2" || l.Req.Hops != 1 || l.CreatedAt != 10 || l.To != 3 {
		t.Fatalf("letter = %+v", l)
	}
	l.Req.ReqID = "mutated"
	if l2, _ := tr.Letter(o); l2.Req.ReqID != "r2" {
		t.Fatal("Letter did not copy Req")
	}
	_ = b
}

func TestPortMultiple(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	c, d := post(t, tr, 42, 1,
		msg(pb.MsgHeartbeat, 1, 2, 1),
		msg(pb.MsgApp, 1, 2, 1),
		msg(pb.MsgHeartbeat, 1, 2, 1),
	)
	wantIDs(t, "created", c, []LetterID{1, 2, 3})
	wantDrops(t, d, []Drop{{Letter: 1, Reason: DropSuperseded, Rule: "R1"}})
	for _, id := range c {
		if l, _ := tr.Letter(id); l.CreatedAt != 42 || l.Kind != LetterRaft || l.From != 1 || l.To != 2 {
			t.Fatalf("letter %d = %+v", id, l)
		}
	}
	wantIDs(t, "port", tr.Port(1), []LetterID{2, 3})
}

func TestPortValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from uint64
		msgs []*pb.Message
		want error
	}{
		{"nil", 1, []*pb.Message{msg(pb.MsgApp, 1, 2, 1), nil}, ErrBadMessage},
		{"from differs", 1, []*pb.Message{msg(pb.MsgApp, 2, 3, 1)}, ErrBadMessage},
		{"unknown to", 1, []*pb.Message{msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 9, 1)}, ErrBadMessage},
		{"to itself", 1, []*pb.Message{msg(pb.MsgApp, 1, 1, 1)}, ErrBadMessage},
		{"unknown from", 9, []*pb.Message{msg(pb.MsgApp, 9, 1, 1)}, ErrUnknownIsland},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, baseConfig())
			first, _ := post1(t, tr, msg(pb.MsgApp, 1, 2, 1))
			c, d, err := tr.PostRaft(0, tt.from, tt.msgs)
			wantErr(t, err, tt.want)
			if c != nil || d != nil {
				t.Fatalf("created=%v dropped=%v on error", c, d)
			}
			wantIDs(t, "port", tr.Port(1), []LetterID{first})
			next, _ := post1(t, tr, msg(pb.MsgApp, 1, 2, 1))
			if next != first+1 {
				t.Fatalf("next id = %d, want %d", next, first+1)
			}
		})
	}

	reqTests := []struct {
		name     string
		from, to uint64
		note     RequestNote
		want     error
	}{
		{"same", 1, 1, RequestNote{ReqID: "r"}, ErrInvalidArgument},
		{"empty", 1, 2, RequestNote{}, ErrInvalidArgument},
		{"unknown from", 9, 2, RequestNote{ReqID: "r"}, ErrUnknownIsland},
		{"unknown to", 1, 9, RequestNote{ReqID: "r"}, ErrUnknownIsland},
	}
	for _, tt := range reqTests {
		t.Run("request "+tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, baseConfig())
			_, _, err := tr.PostRequest(0, tt.from, tt.to, tt.note)
			wantErr(t, err, tt.want)
			id, _, err := tr.PostRequest(0, 1, 2, RequestNote{ReqID: "ok"})
			if err != nil || id != 1 {
				t.Fatalf("id=%d err=%v", id, err)
			}
		})
	}

	t.Run("empty msgs", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, baseConfig())
		c, d, err := tr.PostRaft(0, 1, nil)
		if err != nil || c != nil || d != nil {
			t.Fatalf("c=%v d=%v err=%v", c, d, err)
		}
	})
}

func TestPortDoesNotMutate(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	ms := []*pb.Message{
		msg(pb.MsgHeartbeat, 1, 2, 1),
		msg(pb.MsgHeartbeat, 1, 2, 1),
		msg(pb.MsgApp, 1, 2, 1),
		msg(pb.MsgApp, 1, 3, 1),
		rejectResp(1, 2, 1),
	}
	ms[2].Entries = []*pb.Entry{{Term: u64(1), Index: u64(1), Data: []byte("x")}}
	before := make([]string, len(ms))
	for i, m := range ms {
		before[i] = m.String()
	}
	check := func(stage string) {
		t.Helper()
		for i, m := range ms {
			if m.String() != before[i] {
				t.Fatalf("%s: message %d mutated: %s", stage, i, m.String())
			}
		}
	}
	c, _ := post(t, tr, 0, 1, ms...)
	for i, id := range c {
		if l, _ := tr.Letter(id); l.Raft != ms[i] {
			t.Fatalf("letter %d does not hold the same pointer", id)
		}
	}
	check("post")
	if err := tr.Load("f1", []LetterID{c[2], c[3], c[4]}); err != nil {
		t.Fatal(err)
	}
	check("load")
	if _, err := tr.Unload("f1", []LetterID{c[4]}); err != nil {
		t.Fatal(err)
	}
	check("unload")
	at, err := tr.Depart(0, "f1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Arrive(at, "f1"); err != nil {
		t.Fatal(err)
	}
	check("arrive")
	mustInv(t, tr)
}
