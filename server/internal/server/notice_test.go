package server

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// noticeKeys は §7.3 の 10 個のキー。
var noticeKeys = map[string]bool{
	noticeRequestCreated:    true,
	noticeRequestSucceeded:  true,
	noticeRequestRejected:   true,
	noticeRequestExpired:    true,
	noticeIslandSilent:      true,
	noticeNodeCampaign:      true,
	noticeLeaderElected:     true,
	noticeLeaderSteppedDown: true,
	noticeStormForecast:     true,
	noticeFerryBroken:       true,
}

// testNoticer は島が 3 つ、silenceAfterMs が silence の noticer。
func testNoticer(silence int64) *noticer {
	st := &stage.Stage{Islands: make([]stage.Island, 3)}
	st.Satisfaction.SilenceAfterMs = silence
	return newNoticer(st)
}

// domainEvent は d の DomainEvent。
func domainEvent(seq uint64, t sim.SimTime, d sim.EventData) sim.DomainEvent {
	return sim.DomainEvent{Seq: seq, T: t, Type: d.EventType(), Data: d}
}

// observeAt は ev（seq 1、時刻 t）を observe に渡す。
func observeAt(n *noticer, t sim.SimTime, d sim.EventData) []noticeData {
	return n.observe(domainEvent(1, t, d))
}

// wantNotices は got のキーと話す島が want と一致することを確かめる。
func wantNotices(t *testing.T, what string, got []noticeData, want ...noticeData) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %+v, want %+v", what, got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%s: [%d] = %+v, want %+v", what, i, got[i], want[i])
		}
	}
}

// noticeJSON は notice の DomainEvent の JSON。
func noticeJSON(t *testing.T, ev sim.DomainEvent) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// N-1: leader.elected。
func TestNoticeLeaderElected(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	out := n.expand([]sim.DomainEvent{domainEvent(5, 1000, sim.LeaderElected{ID: 2, Term: 3})})
	if len(out) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if out[0].Seq != 5 || out[0].Type != sim.EvLeaderElected {
		t.Errorf("out[0] = %+v", out[0])
	}
	want := `{"seq":6,"t":1000,"type":"notice","islandId":2,"key":"leader.elected","params":{}}`
	if got := noticeJSON(t, out[1]); got != want {
		t.Errorf("notice = %s, want %s", got, want)
	}
	if got := n.wireSeq(5); got != 6 {
		t.Errorf("wireSeq(5) = %d, want 6", got)
	}
}

// N-2: leader.stepped_down。
func TestNoticeSteppedDown(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	wantNotices(t, "check_quorum", observeAt(n, 0, sim.SteppedDown{ID: 1, Term: 2, Reason: "check_quorum"}),
		noticeData{IslandID: 1, Key: noticeLeaderSteppedDown})
	wantNotices(t, "higher_term", observeAt(n, 0, sim.SteppedDown{ID: 1, Term: 3, Reason: "higher_term"}))
}

// N-3: node.campaign（pre_candidate・candidate のまま・開会式では出さない）。
func TestNoticeCampaign(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	steps := []struct {
		role string
		term uint64
		want bool
	}{
		{"follower", 1, false},
		{"pre_candidate", 1, false},
		{"candidate", 2, true},
		{"candidate", 3, false},
		{"follower", 3, false},
		{"candidate", 4, true},
	}
	for i, s := range steps {
		got := observeAt(n, 0, sim.NodeState{ID: 3, Role: s.role, Term: s.term})
		if s.want {
			wantNotices(t, s.role, got, noticeData{IslandID: 3, Key: noticeNodeCampaign})
		} else if len(got) != 0 {
			t.Errorf("step %d (%s): got %+v", i+1, s.role, got)
		}
	}

	n = testNoticer(30000)
	wantNotices(t, "opening", observeAt(n, 0, sim.PhaseChanged{Phase: sim.PhaseOpening}))
	wantNotices(t, "candidate in opening", observeAt(n, 0, sim.NodeState{ID: 1, Role: "candidate", Term: 2}))
	wantNotices(t, "leader.elected in opening", observeAt(n, 0, sim.LeaderElected{ID: 1, Term: 2}),
		noticeData{IslandID: 1, Key: noticeLeaderElected})
	observeAt(n, 0, sim.NodeState{ID: 1, Role: "leader", Term: 2})
	wantNotices(t, "sailing", observeAt(n, 0, sim.PhaseChanged{Phase: sim.PhaseSailing}))
	wantNotices(t, "candidate in sailing", observeAt(n, 0, sim.NodeState{ID: 2, Role: "candidate", Term: 3}),
		noticeData{IslandID: 2, Key: noticeNodeCampaign})
}

// N-4: storm.forecast。
func TestNoticeForecast(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	out := n.expand([]sim.DomainEvent{domainEvent(1, 5000, sim.Forecast{Event: "storm", At: 15000, RouteIDs: []string{"2-3", "1-2"}})})
	if len(out) != 2 {
		t.Fatalf("out = %+v", out)
	}
	want := `{"seq":2,"t":5000,"type":"notice","islandId":1,"key":"storm.forecast","params":{"routeIds":["2-3","1-2"],"at":15000}}`
	if got := noticeJSON(t, out[1]); got != want {
		t.Errorf("notice = %s, want %s", got, want)
	}
	for _, f := range []sim.Forecast{
		{Event: "storm", At: 15000, RouteIDs: []string{}},
		{Event: "storm", At: 15000, RouteIDs: []string{"x-y"}},
		{Event: "storm", At: 15000, RouteIDs: []string{"0-1", "3-4", "1", "-1-2", "+1-2"}},
		{Event: "rush", At: 15000, RouteIDs: []string{"1-2"}},
	} {
		wantNotices(t, mustJSON(t, f), observeAt(n, 5000, f))
	}
	// 読めない航路は話す島の計算から除くが、params.routeIds はそのまま。
	wantNotices(t, "mixed", observeAt(n, 5000, sim.Forecast{Event: "storm", At: 9000, RouteIDs: []string{"x-y", "2-3"}}),
		noticeData{IslandID: 2, Key: noticeStormForecast, Params: noticeParams{RouteIDs: []string{"x-y", "2-3"}, At: ptr(int64(9000))}})
}

func ptr[T any](v T) *T { return &v }

// ferryNoticer は島が 3 つ、船が f1（島 1）・f2（島 3）の noticer。
func ferryNoticer() *noticer {
	st := &stage.Stage{
		Islands: make([]stage.Island, 3),
		Ferries: []stage.Ferry{{ID: "f1", Home: 1}, {ID: "f2", Home: 3}},
	}
	st.Satisfaction.SilenceAfterMs = 30000
	return newNoticer(st)
}

// NT-B1: ferry.broken。話すのは、その船が停泊している島。
func TestNoticeFerryBroken(t *testing.T) {
	t.Parallel()
	n := ferryNoticer()
	out := n.expand([]sim.DomainEvent{domainEvent(1, 10000, sim.FerryBroken{FerryID: "f2", Until: 30000})})
	if len(out) != 2 {
		t.Fatalf("out = %+v", out)
	}
	want := `{"seq":2,"t":10000,"type":"notice","islandId":3,"key":"ferry.broken","params":{"ferryId":"f2","until":30000}}`
	if got := noticeJSON(t, out[1]); got != want {
		t.Errorf("notice = %s, want %s", got, want)
	}

	// 船が動いたら、着いた島が話す。延びた故障（2 回目）でも出す。
	wantNotices(t, "departed", observeAt(n, 31000, sim.FerryDeparted{FerryID: "f1", From: 1, To: 2}))
	wantNotices(t, "sailing", observeAt(n, 32000, sim.FerryBroken{FerryID: "f1", Until: 40000}))
	wantNotices(t, "arrived", observeAt(n, 34750, sim.FerryArrived{FerryID: "f1", IslandID: 2}))
	wantNotices(t, "at 2", observeAt(n, 34750, sim.FerryBroken{FerryID: "f1", Until: 39750}),
		noticeData{IslandID: 2, Key: noticeFerryBroken, Params: noticeParams{FerryID: "f1", Until: ptr(int64(39750))}})
	wantNotices(t, "extended", observeAt(n, 36000, sim.FerryBroken{FerryID: "f1", Until: 50000}),
		noticeData{IslandID: 2, Key: noticeFerryBroken, Params: noticeParams{FerryID: "f1", Until: ptr(int64(50000))}})
	// 知らない船は出さない。修理は通知しない。
	wantNotices(t, "unknown", observeAt(n, 36000, sim.FerryBroken{FerryID: "zz", Until: 50000}))
	wantNotices(t, "repaired", observeAt(n, 50000, sim.FerryRepaired{FerryID: "f1", Until: 50000}))

	// resync はスナップショットの船の居場所を使う。
	n.resync(sim.Snapshot{T: 60000, Ferries: []sim.FerryView{
		{ID: "f1", State: "sailing", From: 2, To: 1},
		{ID: "f2", State: "docked", IslandID: 2},
	}})
	wantNotices(t, "resync sailing", observeAt(n, 60000, sim.FerryBroken{FerryID: "f1", Until: 70000}))
	wantNotices(t, "resync docked", observeAt(n, 60000, sim.FerryBroken{FerryID: "f2", Until: 70000}),
		noticeData{IslandID: 2, Key: noticeFerryBroken, Params: noticeParams{FerryID: "f2", Until: ptr(int64(70000))}})
}

// NT-B1: Engine で故障を起こし、ferry.broken の直後に、Engine の船の居場所の島の notice が入る。
func TestNoticeFerryBrokenEngine(t *testing.T) {
	t.Parallel()
	cfg := sim.Config{
		Islands:       3,
		InitialLeader: 1,
		DurationMs:    60000,
		Raft:          sim.RaftConfig{TickMs: 500, HeartbeatTick: 2, ElectionTick: 40, MaxSizePerMsg: 4096, MaxInflightMsgs: 4},
		Routes:        []ferry.RouteConfig{{A: 1, B: 2, TravelMs: 3750}, {A: 2, B: 3, TravelMs: 5250}},
		Ferries:       []ferry.FerryConfig{{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000}},
		Breakdowns:    []game.BreakdownDef{{WindowStart: 2000, WindowEnd: 2000, DurationMs: 10000, Ferry: "f1"}},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
	}
	e, err := sim.New(cfg, 1, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	n := newNoticer(&stage.Stage{Islands: make([]stage.Island, 3), Ferries: []stage.Ferry{{ID: "f1", Home: 1}}})
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatal(ack)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}); !ack.OK {
		t.Fatal(ack)
	}
	n.expand(e.DrainEvents())
	e.RunUntil(3750) // 航行中に故障の時刻が来て、着いたときに壊れる
	out := n.expand(e.DrainEvents())
	i := slices.IndexFunc(out, func(ev sim.DomainEvent) bool { return ev.Type == sim.EvFerryBroken })
	if i < 0 || i+1 >= len(out) {
		t.Fatalf("ferry.broken がない: %+v", out)
	}
	f := e.Snapshot().Ferries[0]
	want := noticeData{IslandID: f.IslandID, Key: noticeFerryBroken, Params: noticeParams{FerryID: "f1", Until: ptr(int64(13750))}}
	if got, ok := out[i+1].Data.(noticeData); !ok || !reflect.DeepEqual(got, want) || f.IslandID != 2 {
		t.Errorf("ferry.broken の次 = %+v, want %+v（船は島 %d）", out[i+1].Data, want, f.IslandID)
	}
}

// N-5: 依頼。
func TestNoticeRequests(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	created := func(id string, origin uint64) []noticeData {
		return observeAt(n, 0, sim.RequestCreated{Request: sim.RequestState{ID: id, Origin: origin, Status: "received"}})
	}
	updated := func(id, status string, expired bool) []noticeData {
		return observeAt(n, 0, sim.RequestUpdated{ID: id, Status: status, Expired: expired})
	}
	nd := func(origin uint64, key, id string) noticeData {
		return noticeData{IslandID: origin, Key: key, Params: noticeParams{ReqID: id}}
	}

	wantNotices(t, "created", created("r1", 3), nd(3, noticeRequestCreated, "r1"))
	wantNotices(t, "proposed", updated("r1", "proposed", false))
	wantNotices(t, "succeeded", updated("r1", "succeeded", false), nd(3, noticeRequestSucceeded, "r1"))
	wantNotices(t, "succeeded again", updated("r1", "succeeded", false))

	wantNotices(t, "created r2", created("r2", 1), nd(1, noticeRequestCreated, "r2"))
	wantNotices(t, "rejected", updated("r2", "rejected", false), nd(1, noticeRequestRejected, "r2"))
	wantNotices(t, "rejected again", updated("r2", "rejected", false))

	wantNotices(t, "created r3", created("r3", 2), nd(2, noticeRequestCreated, "r3"))
	wantNotices(t, "expired", updated("r3", "proposed", true), nd(2, noticeRequestExpired, "r3"))
	wantNotices(t, "expired again", updated("r3", "proposed", true))

	wantNotices(t, "created r4", created("r4", 2), nd(2, noticeRequestCreated, "r4"))
	wantNotices(t, "expired and succeeded", updated("r4", "succeeded", true),
		nd(2, noticeRequestExpired, "r4"), nd(2, noticeRequestSucceeded, "r4"))

	wantNotices(t, "unknown", updated("r9", "succeeded", true))
}

// N-6: island.silent。
func TestNoticeSilent(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	silent := noticeData{IslandID: 2, Key: noticeIslandSilent}
	wantNotices(t, "T 30000", observeAt(n, 30000, sim.IslandUpdated{ID: 2, Satisfaction: 58, LastDeliveredAt: 0}), silent)
	wantNotices(t, "T 40000", observeAt(n, 40000, sim.IslandUpdated{ID: 2, Satisfaction: 56, LastDeliveredAt: 0}))
	wantNotices(t, "T 50000", observeAt(n, 50000, sim.IslandUpdated{ID: 2, Satisfaction: 66, LastDeliveredAt: 45000}))
	wantNotices(t, "T 75000", observeAt(n, 75000, sim.IslandUpdated{ID: 2, Satisfaction: 64, LastDeliveredAt: 45000}), silent)

	n = testNoticer(0)
	wantNotices(t, "silenceAfterMs 0", observeAt(n, 30000, sim.IslandUpdated{ID: 2, LastDeliveredAt: 0}))
}

// N-7: seq の振り直し。
func TestNoticeExpandSeq(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	first := []sim.DomainEvent{
		domainEvent(1, 0, sim.PhaseChanged{Phase: sim.PhaseOpening}),
		domainEvent(2, 0, sim.LeaderElected{ID: 1, Term: 2}),
		domainEvent(3, 0, sim.NodeState{ID: 1, Role: "leader", Term: 2}),
		domainEvent(4, 0, sim.PhaseChanged{Phase: sim.PhaseSailing}),
	}
	second := []sim.DomainEvent{
		domainEvent(5, 5000, sim.RequestCreated{Request: sim.RequestState{ID: "r1", Origin: 3}}),
		domainEvent(6, 5000, sim.LedgerApplied{IslandID: 3, Index: 1}),
	}
	out := append(n.expand(first), n.expand(second)...)
	if len(out) != 8 {
		t.Fatalf("out = %+v", out)
	}
	for i, ev := range out {
		if ev.Seq != uint64(i+1) {
			t.Errorf("out[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
		isNotice := ev.Type == sim.EventType(protocol.EventTypeNotice)
		if wantNotice := ev.Seq == 3 || ev.Seq == 7; isNotice != wantNotice {
			t.Errorf("seq %d: type %s", ev.Seq, ev.Type)
		}
	}
	for i, ev := range append(first, second...) {
		if ev.Seq != uint64(i+1) {
			t.Errorf("input [%d].Seq = %d, was changed", i, ev.Seq)
		}
	}
	if got := n.expand(nil); len(got) != 0 {
		t.Errorf("expand(nil) = %+v", got)
	}
	if got := n.wireSeq(6); got != 8 {
		t.Errorf("wireSeq(6) = %d, want 8", got)
	}
}

// N-8: resync。
func TestNoticeResync(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	n.expand([]sim.DomainEvent{domainEvent(1, 0, sim.LeaderElected{ID: 1, Term: 2})})
	off := n.off
	n.resync(sim.Snapshot{
		Phase: sim.PhaseSailing,
		T:     40000,
		Nodes: []sim.NodeState{
			{ID: 1, Role: "follower", Term: 3},
			{ID: 2, Role: "candidate", Term: 3},
			{ID: 3, Role: "follower", Term: 3},
		},
		Requests: []sim.RequestState{
			{ID: "r1", Origin: 3, Status: "succeeded"},
			{ID: "r2", Origin: 2, Status: "proposed", Expired: true},
		},
		Islands: []sim.IslandState{
			{ID: 1, Satisfaction: 58, LastDeliveredAt: 0},
			{ID: 2, Satisfaction: 60, LastDeliveredAt: 20000},
			{ID: 3, Satisfaction: 60, LastDeliveredAt: 20000},
		},
	})
	if n.off != off {
		t.Errorf("resync changed off: %d → %d", off, n.off)
	}
	wantNotices(t, "candidate", observeAt(n, 40000, sim.NodeState{ID: 2, Role: "candidate", Term: 4}))
	wantNotices(t, "succeeded", observeAt(n, 40000, sim.RequestUpdated{ID: "r1", Status: "succeeded"}))
	wantNotices(t, "expired", observeAt(n, 40000, sim.RequestUpdated{ID: "r2", Status: "proposed", Expired: true}))
	wantNotices(t, "silent", observeAt(n, 40000, sim.IslandUpdated{ID: 1, Satisfaction: 56, LastDeliveredAt: 0}))
	// まだ音信不通でなかった島は、その後に出せる。
	wantNotices(t, "silent later", observeAt(n, 50000, sim.IslandUpdated{ID: 2, Satisfaction: 58, LastDeliveredAt: 20000}),
		noticeData{IslandID: 2, Key: noticeIslandSilent})
	// 確定していなかった依頼は、その後に出せる。
	n.resync(sim.Snapshot{T: 0, Requests: []sim.RequestState{{ID: "r5", Origin: 1, Status: "proposed"}}})
	wantNotices(t, "r5", observeAt(n, 1000, sim.RequestUpdated{ID: "r5", Status: "rejected"}),
		noticeData{IslandID: 1, Key: noticeRequestRejected, Params: noticeParams{ReqID: "r5"}})
	// 採点しないステージ（Islands が nil）。
	wantNotices(t, "nil islands", observeAt(n, 40000, sim.IslandUpdated{ID: 3, LastDeliveredAt: 0}),
		noticeData{IslandID: 3, Key: noticeIslandSilent})
}

// N-9: JSON。
func TestNoticeJSON(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	evs := []sim.DomainEvent{
		domainEvent(1, 100, sim.LeaderElected{ID: 2, Term: 3}),
		domainEvent(2, 200, sim.RequestCreated{Request: sim.RequestState{ID: "r1", Origin: 3}}),
		domainEvent(3, 300, sim.Forecast{Event: "storm", At: 900, RouteIDs: []string{"1-2"}}),
	}
	out := n.expand(evs)
	want := []string{
		`{"seq":2,"t":100,"type":"notice","islandId":2,"key":"leader.elected","params":{}}`,
		`{"seq":4,"t":200,"type":"notice","islandId":3,"key":"request.created","params":{"reqId":"r1"}}`,
		`{"seq":6,"t":300,"type":"notice","islandId":1,"key":"storm.forecast","params":{"routeIds":["1-2"],"at":900}}`,
	}
	var got []string
	for _, ev := range out {
		if ev.Type != sim.EventType(protocol.EventTypeNotice) {
			continue
		}
		b := noticeJSON(t, ev)
		got = append(got, b)
		v, ok := protocol.NewEvent(protocol.EventTypeNotice)
		if !ok {
			t.Fatal("NewEvent(notice)")
		}
		if err := json.Unmarshal([]byte(b), v); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		pe := v.(*protocol.NoticeEvent)
		d := ev.Data.(noticeData)
		if pe.Seq != ev.Seq || pe.T != int64(ev.T) || pe.Type != protocol.EventTypeNotice ||
			pe.IslandID != d.IslandID || pe.Key != d.Key || pe.Params == nil || !noticeKeys[pe.Key] {
			t.Errorf("decoded %+v from %s", pe, b)
		}
		if d.Params.ReqID != "" && string(pe.Params["reqId"]) != `"`+d.Params.ReqID+`"` {
			t.Errorf("params.reqId = %s", pe.Params["reqId"])
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
}

// N-10: 範囲の外の島。
func TestNoticeOutOfRange(t *testing.T) {
	t.Parallel()
	n := testNoticer(30000)
	for _, d := range []sim.EventData{
		sim.LeaderElected{ID: 0, Term: 1},
		sim.LeaderElected{ID: 4, Term: 1},
		sim.NodeState{ID: 4, Role: "candidate", Term: 2},
		sim.NodeState{ID: 0, Role: "candidate", Term: 2},
		sim.SteppedDown{ID: 4, Reason: "check_quorum"},
		sim.IslandUpdated{ID: 4, LastDeliveredAt: 0},
		sim.RequestCreated{Request: sim.RequestState{ID: "rx", Origin: 0}},
		sim.RequestCreated{Request: sim.RequestState{ID: "ry", Origin: 4}},
		sim.RequestUpdated{ID: "ry", Status: "succeeded"},
		sim.LetterDelivered{LetterID: 1, IslandID: 2, Accepted: true},
		sim.ScoreUpdated{CriticalSecured: []string{}},
	} {
		wantNotices(t, mustJSON(t, d), observeAt(n, 40000, d))
	}
	if n.off != 0 {
		t.Errorf("off = %d", n.off)
	}
}
