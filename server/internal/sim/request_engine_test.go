package sim

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// reqTestConfig は依頼のテストの基本の設定（値はテストの中の値）。
// ElectionTick を 200（100 秒）にして、テストの往復の間に Leader が変わらないようにする。
func reqTestConfig(script ...game.ScriptRequest) Config {
	cfg := testConfig()
	cfg.Raft.ElectionTick = 200
	cfg.Requests = reqConfig(script...)
	return cfg
}

func reqConfig(script ...game.ScriptRequest) game.RequestConfig {
	return game.RequestConfig{
		StageID:            "t",
		DeadlineNormalMs:   60000,
		DeadlineCriticalMs: 90000,
		RetryMs:            25000,
		Script:             script,
		Critical:           []string{"firework:a"},
	}
}

func stallReq(at int64, island uint64) game.ScriptRequest {
	return game.ScriptRequest{At: at, Island: island, Kind: ledger.KindStallReserve, Resource: "stall:main"}
}

func fireworkReq(at int64, island uint64) game.ScriptRequest {
	return game.ScriptRequest{At: at, Island: island, Kind: ledger.KindFireworkReserve, Resource: "firework:a"}
}

func reqStarted(t *testing.T, cfg Config) *Engine {
	t.Helper()
	return startedEngineWith(t, cfg, Options{CheckInvariants: true})
}

// requestLetters は letter.created のうち依頼書のものを返す。
func requestLetters(evs []DomainEvent) []LetterView {
	var out []LetterView
	for _, c := range ofType[LetterCreated](evs) {
		if c.Letter.Kind == "request" {
			out = append(out, c.Letter)
		}
	}
	return out
}

func reqLetter(id, from, to uint64, at SimTime, reqID string, hops int) LetterView {
	return LetterView{
		ID: id, Kind: "request", From: from, To: to, CreatedAt: at, Loc: portLoc(from), Label: "request",
		Request: &RequestView{ReqID: reqID, Hops: hops},
	}
}

func updated(id string, status game.Status, result *RequestResult) RequestUpdated {
	return RequestUpdated{ID: id, Status: string(status), Result: result}
}

var resultOK = &RequestResult{Status: ledger.StatusOK}

// raftLetters は island の港にある、to 宛ての typ の Raft の手紙の ID を返す（ID の昇順）。
func raftLetters(e *Engine, island, to uint64, typ pb.MessageType) []uint64 {
	var out []uint64
	for _, id := range e.tr.Port(island) {
		l, ok := e.tr.Letter(id)
		if ok && l.Kind == ferry.LetterRaft && l.To == to && l.Raft.GetType() == typ {
			out = append(out, uint64(id))
		}
	}
	return out
}

// sail は f1 を to へ出して着くまで進め、出した時刻から着いた時刻までのイベントを返す。
func sail(t *testing.T, e *Engine, to uint64) []DomainEvent {
	t.Helper()
	f, _ := e.tr.Ferry("f1")
	e.RunUntil(SimTime(f.ReadyAt))
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: to})
	deps := ofType[FerryDeparted](e.DrainEvents())
	if len(deps) != 1 {
		t.Fatalf("ferry.departed = %+v", deps)
	}
	e.RunUntil(deps[0].ArriveAt)
	return e.DrainEvents()
}

func load(t *testing.T, e *Engine, ids []uint64) []DomainEvent {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("no letter to load")
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
	return e.DrainEvents()
}

// requestOnly は依頼のイベント（request.*）だけを並べる。
func requestOnly(evs []DomainEvent) []EventData {
	var out []EventData
	for _, ev := range evs {
		switch ev.Data.(type) {
		case RequestCreated, RequestUpdated:
			out = append(out, ev.Data)
		}
	}
	return out
}

func types(evs []DomainEvent) []EventType {
	out := make([]EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

// leaderRoundTrip は E-1 の 1. と 2. を行う（依頼元が Leader の依頼を、D-1 と同じ運航で確定させる）。
// t = 0 と t = 9000 のイベントを返す。
func leaderRoundTrip(t *testing.T) (e *Engine, at0, at9000 []DomainEvent) {
	t.Helper()
	e = reqStarted(t, reqTestConfig(stallReq(0, 1)))
	e.RunUntil(0)
	at0 = e.DrainEvents()
	load(t, e, lids(1, 1))
	evs := sail(t, e, 2)
	var resp []uint64
	for _, c := range ofType[LetterCreated](evs) {
		if c.Letter.From == 2 && c.Letter.Label == "append_resp" {
			resp = append(resp, c.Letter.ID)
		}
	}
	load(t, e, resp)
	evs = sail(t, e, 1)
	return e, at0, at(evs, 9000)
}

func TestRequestLeaderOrigin(t *testing.T) { // E-1
	t.Parallel()
	e, at0, at9000 := leaderRoundTrip(t)

	// 1. t = 0
	wantTypes := []EventType{EvRequestCreated, EvLetterCreated, EvLetterCreated, EvNodeState, EvRequestUpdated}
	if got := types(at0); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("types at 0 = %v, want %v", got, wantTypes)
	}
	for _, ev := range at0 {
		if ev.T != 0 {
			t.Errorf("event %d at %d, want 0", ev.Seq, ev.T)
		}
	}
	wantCreated := RequestCreated{Request: RequestState{
		ID: "t-0001", Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main",
		CreatedAt: 0, Deadline: 60000, Status: "received",
	}}
	if at0[0].Data != wantCreated {
		t.Errorf("request.created = %+v, want %+v", at0[0].Data, wantCreated)
	}
	wantEntries := []EntryView{{Index: 5, Term: 2, Type: "normal", Summary: "stall.reserve t-0001"}}
	for i, to := range []uint64{2, 3} {
		l := at0[1+i].Data.(LetterCreated).Letter
		if l.ID != lid(1, uint64(i+1)) || l.From != 1 || l.To != to || l.Label != "append" || !reflect.DeepEqual(l.Raft.Entries, wantEntries) {
			t.Errorf("letter.created %d = %+v (raft %+v)", i, l, l.Raft)
		}
	}
	if ns := at0[3].Data.(NodeState); ns.ID != 1 || ns.LastIndex != 5 {
		t.Errorf("node.state = %+v, want island 1 with lastIndex 5", ns)
	}
	if want := updated("t-0001", game.StatusProposed, nil); at0[4].Data != want {
		t.Errorf("request.updated = %+v, want %+v", at0[4].Data, want)
	}

	// 2. t = 9000
	iApplied := indexOf(at9000, func(d EventData) bool {
		return d == LedgerApplied{IslandID: 1, Index: 5, ReqID: "t-0001", Result: "ok"}
	})
	ups := ofType[RequestUpdated](at9000)
	if !reflect.DeepEqual(ups, []RequestUpdated{updated("t-0001", game.StatusSucceeded, resultOK)}) {
		t.Errorf("request.updated at 9000 = %+v, want 1 succeeded", ups)
	}
	iUpd := indexOf(at9000, func(d EventData) bool { _, ok := d.(RequestUpdated); return ok })
	if iApplied < 0 || iUpd <= iApplied {
		t.Errorf("ledger.applied %d, request.updated %d in %+v", iApplied, iUpd, datas(at9000))
	}

	// 3. 台帳と再送
	if rec, ok := e.ledgers[0].Lookup("t-0001"); !ok || rec.Result.Status != ledger.StatusOK {
		t.Errorf("Lookup(t-0001) = %+v, %v", rec, ok)
	}
	e.RunUntil(25000)
	evs := e.DrainEvents()
	if got := requestLetters(evs); len(got) != 0 {
		t.Errorf("request letters after succeeded: %+v", got)
	}
	if got := requestOnly(evs); len(got) != 0 {
		t.Errorf("request events after succeeded: %+v", got)
	}
	for _, c := range ofType[LetterCreated](evs) {
		if c.Letter.Raft != nil && len(c.Letter.Raft.Entries) > 0 {
			t.Errorf("entries proposed after succeeded: %+v", c.Letter.Raft.Entries)
		}
	}
}

func TestRequestFollowerOrigin(t *testing.T) { // E-2
	t.Parallel()
	e := reqStarted(t, reqTestConfig(fireworkReq(0, 2)))
	var statuses []string
	var times []SimTime
	record := func(evs []DomainEvent) {
		for _, ev := range evs {
			switch d := ev.Data.(type) {
			case RequestCreated:
				statuses = append(statuses, d.Request.Status)
				times = append(times, ev.T)
			case RequestUpdated:
				statuses = append(statuses, d.Status)
				times = append(times, ev.T)
			}
		}
	}

	// 1. t = 0
	e.RunUntil(0)
	evs := e.DrainEvents()
	record(evs)
	checkEvents(t, evs, []EventData{
		RequestCreated{Request: RequestState{
			ID: "t-0001", Origin: 2, Kind: ledger.KindFireworkReserve, Resource: "firework:a",
			Critical: true, CreatedAt: 0, Deadline: 90000, Status: "received",
		}},
		LetterCreated{Letter: reqLetter(lid(2, 1), 2, 1, 0, "t-0001", 0)},
		updated("t-0001", game.StatusWaiting, nil),
	})

	// 2. 2 で依頼書を積む
	record(sail(t, e, 2))
	evs = load(t, e, lids(2, 1))
	record(evs)
	checkEvents(t, evs, []EventData{
		LetterMoved{LetterID: lid(2, 1), Loc: ferryLoc("f1")},
		updated("t-0001", game.StatusInTransit, nil),
	})

	// 3. 1 へ運ぶ
	evs = sail(t, e, 1)
	record(evs)
	evs = at(evs, 9000)
	iDel := indexOf(evs, func(d EventData) bool { return d == LetterDelivered{LetterID: lid(2, 1), IslandID: 1, Accepted: true} })
	iApp := indexOf(evs, func(d EventData) bool {
		c, ok := d.(LetterCreated)
		return ok && c.Letter.From == 1 && c.Letter.Label == "append"
	})
	iUpd := indexOf(evs, func(d EventData) bool { return d == updated("t-0001", game.StatusProposed, nil) })
	if iDel < 0 || iApp <= iDel || iUpd <= iApp {
		t.Fatalf("delivered %d, append %d, proposed %d in %+v", iDel, iApp, iUpd, datas(evs))
	}

	// 4. MsgApp を 2 へ運び、返事を 1 へ持ち帰る
	load(t, e, raftLetters(e, 1, 2, pb.MsgApp))
	record(sail(t, e, 2))
	load(t, e, raftLetters(e, 2, 1, pb.MsgAppResp))
	evs = sail(t, e, 1)
	record(evs)
	evs = at(evs, 19500)
	applied := ofType[LedgerApplied](evs)
	if want := []LedgerApplied{{IslandID: 1, Index: 5, ReqID: "t-0001", Result: "ok"}}; !reflect.DeepEqual(applied, want) {
		t.Errorf("ledger.applied at 19500 = %+v, want %+v", applied, want)
	}
	if got := ofType[RequestUpdated](evs); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusCommitted, nil)}) {
		t.Errorf("request.updated at 19500 = %+v, want committed", got)
	}

	// 5. Commit を知らせる MsgApp を 2 へ運ぶ
	load(t, e, raftLetters(e, 1, 2, pb.MsgApp))
	evs = sail(t, e, 2)
	record(evs)
	evs = at(evs, 24750)
	applied = ofType[LedgerApplied](evs)
	if want := []LedgerApplied{{IslandID: 2, Index: 5, ReqID: "t-0001", Result: "ok"}}; !reflect.DeepEqual(applied, want) {
		t.Errorf("ledger.applied at 24750 = %+v, want %+v", applied, want)
	}
	if got := ofType[RequestUpdated](evs); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusSucceeded, resultOK)}) {
		t.Errorf("request.updated at 24750 = %+v, want succeeded", got)
	}

	// 6. 再送では何も出ない
	e.RunUntil(25000)
	evs = e.DrainEvents()
	if got := requestOnly(evs); len(got) != 0 {
		t.Errorf("request events at 25000: %+v", got)
	}
	if got := requestLetters(evs); len(got) != 0 {
		t.Errorf("request letters at 25000: %+v", got)
	}

	wantStatuses := []string{"received", "waiting", "in_transit", "proposed", "committed", "succeeded"}
	wantTimes := []SimTime{0, 0, 3750, 9000, 19500, 24750}
	if !reflect.DeepEqual(statuses, wantStatuses) || !reflect.DeepEqual(times, wantTimes) {
		t.Errorf("statuses = %v at %v, want %v at %v", statuses, times, wantStatuses, wantTimes)
	}
}

func TestRequestRetry(t *testing.T) { // E-3
	t.Parallel()
	e := reqStarted(t, reqTestConfig(stallReq(0, 2)))
	e.RunUntil(24999)
	all := e.DrainEvents()
	e.RunUntil(25000)
	evs := e.DrainEvents()
	all = append(all, evs...)
	i := indexOf(evs, func(d EventData) bool { c, ok := d.(LetterCreated); return ok && c.Letter.Kind == "request" })
	if i < 0 || i+1 >= len(evs) {
		t.Fatalf("no request letter at 25000: %+v", datas(evs))
	}
	l := evs[i].Data.(LetterCreated).Letter
	if l.ID == lid(2, 1) || l.CreatedAt != 25000 || l.From != 2 || l.To != 1 || l.Request.Hops != 0 {
		t.Errorf("retried letter = %+v", l)
	}
	if got, want := evs[i+1].Data, (LetterDropped{LetterID: lid(2, 1), Reason: "superseded"}); got != want {
		t.Errorf("after the retried letter: %+v, want %+v", got, want)
	}
	if got := ofType[RequestUpdated](evs); len(got) != 0 {
		t.Errorf("request.updated at 25000 = %+v, want none", got)
	}

	e.RunUntil(120000)
	all = append(all, e.DrainEvents()...)
	var ts []SimTime
	for _, l := range requestLetters(all) {
		ts = append(ts, l.CreatedAt)
	}
	if want := []SimTime{0, 25000, 50000, 75000, 100000}; !reflect.DeepEqual(ts, want) {
		t.Errorf("request letters at %v, want %v", ts, want)
	}
}

func TestRequestRetryNoDoubleSpend(t *testing.T) { // E-4
	t.Parallel()
	e := reqStarted(t, reqTestConfig(stallReq(0, 1)))
	e.RunUntil(24999)
	e.DrainEvents()
	e.RunUntil(25000)
	evs := e.DrainEvents()
	found := false
	for _, c := range ofType[LetterCreated](evs) {
		if c.Letter.Raft == nil {
			continue
		}
		if slices.Contains(c.Letter.Raft.Entries, EntryView{Index: 6, Term: 2, Type: "normal", Summary: "stall.reserve t-0001"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("no MsgApp with index 6 at 25000: %+v", datas(evs))
	}
	if got := ofType[RequestUpdated](evs); len(got) != 0 {
		t.Errorf("request.updated at 25000 = %+v, want none", got)
	}

	apps := raftLetters(e, 1, 2, pb.MsgApp)
	if len(apps) != 2 {
		t.Fatalf("MsgApp to 2 = %v, want 2 letters", apps)
	}
	load(t, e, apps)
	sail(t, e, 2)
	load(t, e, raftLetters(e, 2, 1, pb.MsgAppResp))
	evs = sail(t, e, 1)
	applied := ofType[LedgerApplied](evs)
	want := []LedgerApplied{
		{IslandID: 1, Index: 5, ReqID: "t-0001", Result: "ok"},
		{IslandID: 1, Index: 6, ReqID: "t-0001", Result: "duplicate"},
	}
	if !reflect.DeepEqual(applied, want) {
		t.Errorf("ledger.applied = %+v, want %+v", applied, want)
	}
	if got := ofType[RequestUpdated](evs); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusSucceeded, resultOK)}) {
		t.Errorf("request.updated = %+v, want 1 succeeded", got)
	}
	snap := e.ledgers[0].Snapshot()
	i := slices.IndexFunc(snap.Resources, func(r ledger.ResourceState) bool { return r.ID == "stall:main" })
	if i < 0 || !reflect.DeepEqual(snap.Resources[i].Holders, []string{"t-0001"}) {
		t.Errorf("stall:main = %+v, want holders [t-0001]", snap.Resources)
	}
}

func TestRequestForward(t *testing.T) { // E-5
	t.Parallel()
	e := reqStarted(t, reqTestConfig(stallReq(0, 3)))
	e.RunUntil(0)
	e.DrainEvents()
	id, _, err := e.postRequestForTest(0, 1, 2, ferry.RequestNote{ReqID: "t-0001", Hops: 0})
	if err != nil {
		t.Fatal(err)
	}
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Fatalf("events after PostRequest: %+v", datas(evs))
	}
	load(t, e, []uint64{uint64(id)})
	evs := at(sail(t, e, 2), 3750)
	iDel := indexOf(evs, func(d EventData) bool { return d == LetterDelivered{LetterID: uint64(id), IslandID: 2, Accepted: true} })
	if iDel < 0 || iDel+1 >= len(evs) {
		t.Fatalf("letter.delivered missing: %+v", datas(evs))
	}
	c, ok := evs[iDel+1].Data.(LetterCreated)
	if !ok {
		t.Fatalf("after delivered: %+v, want letter.created", evs[iDel+1].Data)
	}
	fwd := c.Letter
	if want := reqLetter(fwd.ID, 2, 1, 3750, "t-0001", 1); !reflect.DeepEqual(fwd, want) {
		t.Errorf("forwarded letter = %+v, want %+v", fwd, want)
	}

	evs = load(t, e, []uint64{fwd.ID})
	if got := ofType[RequestUpdated](evs); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusInTransit, nil)}) {
		t.Errorf("request.updated after load = %+v, want in_transit", got)
	}
	evs = sail(t, e, 1)
	if got := ofType[RequestUpdated](evs); !reflect.DeepEqual(got, []RequestUpdated{updated("t-0001", game.StatusProposed, nil)}) {
		t.Errorf("request.updated at 1 = %+v, want proposed", got)
	}
}

func TestRequestNoLeader(t *testing.T) { // E-6
	t.Parallel()
	cfg := testConfig()
	cfg.Requests = reqConfig(stallReq(45100, 1))
	e := reqStarted(t, cfg)
	e.RunUntil(45099)
	evs := e.DrainEvents()
	if len(ofType[SteppedDown](evs)) == 0 {
		t.Fatal("island 1 did not step down (precondition)")
	}
	if st := e.nodes[0].Status(); st.Lead != 0 {
		t.Fatalf("island 1 lead = %d, want 0 (precondition)", st.Lead)
	}
	e.RunUntil(45100)
	evs = at(e.DrainEvents(), 45100)
	if got := requestOnly(evs); len(got) != 2 || got[1] != updated("t-0001", game.StatusWaiting, nil) {
		t.Errorf("request events at 45100 = %+v, want created and waiting", got)
	}
	if got := requestLetters(evs); len(got) != 0 {
		t.Errorf("request letters at 45100: %+v", got)
	}
	e.RunUntil(60000)
	if got := requestLetters(e.DrainEvents()); len(got) != 0 {
		t.Errorf("request letters until 60000: %+v", got)
	}
	if ws := e.islands[0].Waits(); len(ws) != 1 || ws[0].Req != 0 || ws[0].Hops != 0 {
		t.Errorf("waits of island 1 = %+v, want request 0 with hops 0", ws)
	}
	for _, isl := range e.islands {
		for _, ev := range isl.Events() {
			if ev.Kind == islandcore.KindRequestRetry {
				t.Errorf("retry timer is scheduled on island %d: %+v", isl.ID(), ev)
			}
		}
	}
}

func TestRequestWaitRetried(t *testing.T) { // E-7
	t.Parallel()
	e := reqStarted(t, reqTestConfig(stallReq(0, 2)))
	e.RunUntil(0)
	e.DrainEvents()
	gen := e.islands[0].RetryGen(0)
	e.islands[2].AddWait(0, 2)
	e.RunUntil(500)
	got := requestLetters(e.DrainEvents())
	if len(got) != 1 {
		t.Fatalf("request letters = %+v, want 1", got)
	}
	if want := reqLetter(got[0].ID, 3, 1, 500, "t-0001", 2); !reflect.DeepEqual(got[0], want) {
		t.Errorf("letter = %+v, want %+v", got[0], want)
	}
	for _, isl := range e.islands {
		if ws := isl.Waits(); len(ws) != 0 {
			t.Errorf("waits of island %d = %+v, want empty", isl.ID(), ws)
		}
	}
	if g := e.islands[0].RetryGen(0); g != gen {
		t.Errorf("gen = %d, want %d (not re-armed by island 3)", g, gen)
	}
}

func TestRequestRejected(t *testing.T) { // E-8
	t.Parallel()
	e := reqStarted(t, reqTestConfig(fireworkReq(0, 1), fireworkReq(100, 1)))
	e.RunUntil(100)
	e.DrainEvents()
	apps := raftLetters(e, 1, 2, pb.MsgApp)
	if len(apps) != 2 {
		t.Fatalf("MsgApp to 2 = %v, want 2", apps)
	}
	load(t, e, apps)
	sail(t, e, 2)
	load(t, e, raftLetters(e, 2, 1, pb.MsgAppResp))
	evs := sail(t, e, 1)
	ups := ofType[RequestUpdated](evs)
	want := []RequestUpdated{
		updated("t-0001", game.StatusSucceeded, resultOK),
		updated("t-0002", game.StatusRejected, &RequestResult{Status: "rejected", Reason: "already_reserved"}),
	}
	if !reflect.DeepEqual(ups, want) {
		t.Errorf("request.updated = %+v, want %+v", ups, want)
	}
	var ts []SimTime
	for _, ev := range evs {
		if ev.Type == EvRequestUpdated {
			ts = append(ts, ev.T)
		}
	}
	if len(ts) != 2 || ts[0] != ts[1] {
		t.Errorf("request.updated at %v, want the same time", ts)
	}
}

func TestRequestAppliedDoesNothing(t *testing.T) { // E-9
	t.Parallel()
	e, _, _ := leaderRoundTrip(t)
	before := snapTransport(e)
	hash := e.TraceHash()
	e.route(0, 2, 0)
	e.afterStep()
	if evs := e.DrainEvents(); len(evs) != 0 {
		t.Errorf("events after route of an applied request: %+v", datas(evs))
	}
	if after := snapTransport(e); !reflect.DeepEqual(before, after) {
		t.Errorf("transport changed: %+v → %+v", before, after)
	}
	if e.TraceHash() != hash {
		t.Error("TraceHash changed")
	}
}

// applyRequests は request.* を model に当てはめる（ID の順）。
func applyRequests(t *testing.T, model []RequestState, evs []DomainEvent) []RequestState {
	t.Helper()
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case RequestCreated:
			model = append(model, d.Request)
		case RequestUpdated:
			i := slices.IndexFunc(model, func(r RequestState) bool { return r.ID == d.ID })
			if i < 0 {
				t.Fatalf("request.updated for unknown %s", d.ID)
			}
			model[i].Status, model[i].Expired, model[i].Result = d.Status, d.Expired, d.Result
		}
	}
	return model
}

func TestRequestSnapshotAndNotable(t *testing.T) { // E-10
	t.Parallel()
	e := newEngine(t, reqTestConfig(stallReq(5100, 1), stallReq(7000, 2)), Options{CheckInvariants: true})
	if got := e.Snapshot().Requests; got == nil || len(got) != 0 {
		t.Errorf("Snapshot().Requests in intro = %#v, want empty", got)
	}
	if at, ok := e.NextNotableAt(); at != 0 || ok {
		t.Errorf("NextNotableAt in intro = %d, %v", at, ok)
	}
	model := []RequestState{}
	check := func(name string, wantAt SimTime, wantOK bool) {
		t.Helper()
		pending, hash := len(e.pending), e.TraceHash()
		s := e.Snapshot()
		at, ok := e.NextNotableAt()
		if e.TraceHash() != hash || len(e.pending) != pending {
			t.Errorf("%s: Snapshot or NextNotableAt changed the events", name)
		}
		model = applyRequests(t, model, e.DrainEvents())
		if !reflect.DeepEqual(s.Requests, model) {
			t.Errorf("%s: Snapshot().Requests = %+v, want %+v", name, s.Requests, model)
		}
		if at != wantAt || ok != wantOK {
			t.Errorf("%s: NextNotableAt = %d, %v; want %d, %v", name, at, ok, wantAt, wantOK)
		}
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	check("start", 5100, true)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	check("depart", 3750, true)
	e.RunUntil(3750)
	check("3750", 5100, true)
	e.RunUntil(5100)
	check("5100", 7000, true)
	if len(model) != 1 {
		t.Errorf("requests at 5100 = %+v, want 1", model)
	}
	e.RunUntil(7000)
	check("7000", 0, false)
	if len(model) != 2 || model[0].Status != "proposed" || model[1].Status != "waiting" {
		t.Errorf("requests at 7000 = %+v", model)
	}
}

func TestRequestInvalidConfig(t *testing.T) { // E-12
	t.Parallel()
	cfg := reqTestConfig(stallReq(0, 1))
	cfg.Requests.RetryMs = 0
	e, err := New(cfg, testSeed, Options{})
	if e != nil || !errorsIsInvalid(err) {
		t.Errorf("New = %v, %v; want game.ErrInvalidConfig", e, err)
	}
}

func TestNoRequests(t *testing.T) { // E-13（既存のテストに加えて、依頼の設定がなければ依頼のイベントが出ない）
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(30000)
	evs := e.DrainEvents()
	if got := requestOnly(evs); len(got) != 0 {
		t.Errorf("request events without requests: %+v", got)
	}
	if got := requestLetters(evs); len(got) != 0 {
		t.Errorf("request letters without requests: %+v", got)
	}
	if len(e.reqs.list) != 0 {
		t.Errorf("requests = %+v", e.reqs.list)
	}
}

// randomRequestRun は E-14 のランダムな操作をラウンドの終わりまで行い、全イベントと TraceHash を返す。
func randomRequestRun(t *testing.T, cfg Config, seed uint64) ([]DomainEvent, [32]byte) {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 1))
	var all []DomainEvent
	mustApply(t, e, Command{Type: CmdGameStart})
	all = append(all, e.DrainEvents()...)
	for e.Phase() != PhaseEnded {
		f, _ := e.tr.Ferry("f1")
		switch r.IntN(5) {
		case 0: // load
			if f.State == ferry.Docked {
				port := e.tr.Port(f.At)
				if len(port) > 0 {
					n := 1 + r.IntN(min(len(port), 5))
					var ids []uint64
					for _, i := range r.Perm(len(port))[:n] {
						ids = append(ids, uint64(port[i]))
					}
					e.Apply(Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
				}
			}
		case 1: // unload
			if len(f.Cargo) > 0 {
				e.Apply(Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[r.IntN(len(f.Cargo))])}})
			}
		case 2: // depart
			if f.State == ferry.Docked {
				to := uint64(2)
				if f.At == 2 {
					to = 1 + 2*uint64(r.IntN(2))
				}
				e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: to})
			}
		case 3: // おまかせ積み込み（配達を増やすため）
			if f.State == ferry.Docked {
				to := uint64(2)
				if f.At == 2 {
					to = 1 + 2*uint64(r.IntN(2))
				}
				e.Apply(Command{Type: CmdFerryAutoload, FerryID: "f1", To: to})
			}
		case 4:
			e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
		}
		all = append(all, e.DrainEvents()...)
	}
	return all, e.TraceHash()
}

// rank は状態の進み具合（committed より前に戻らないことの確認用）。
func rank(s string) int {
	switch s {
	case "committed":
		return 1
	case "succeeded", "rejected":
		return 2
	}
	return 0
}

func TestRequestRandom(t *testing.T) { // E-14
	t.Parallel()
	random := game.RandomConfig{
		RateMilliPerMin: 6000,
		SlotMs:          10000,
		Weights:         []int64{500, 300, 200},
		Kinds: []game.RandomKind{
			{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 1},
			{Kind: ledger.KindFireworkReserve, Resource: "firework:a", Weight: 1},
		},
	}
	base := reqTestConfig()
	base.DurationMs = 600000 // 配達を増やして、確定まで進む依頼を作る
	base.Requests.Random = random
	short := base
	short.Raft.ElectionTick = 40 // Leader の交代も混ぜる
	for _, tc := range []struct {
		name      string
		cfg       Config
		wantFinal bool // succeeded か rejected まで進む依頼がある（このシードと操作で確かめた）
	}{{"electionTick 200", base, true}, {"electionTick 40", short, false}} {
		cfg := tc.cfg
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// シード 4 は、おまかせが経由の手紙も積むようになったあと（#318）に確かめ直したもの。
			a, ha := randomRequestRun(t, cfg, 4)
			b, hb := randomRequestRun(t, cfg, 4)
			if !reflect.DeepEqual(a, b) || ha != hb {
				t.Errorf("same seed gave different runs: %d vs %d events", len(a), len(b))
			}
			if _, hc := randomRequestRun(t, cfg, 5); ha == hc {
				t.Error("seeds 4 and 5 gave the same TraceHash")
			}

			created := ofType[RequestCreated](a)
			if len(created) == 0 {
				t.Fatal("no request was created")
			}
			var last SimTime
			for i, c := range created {
				if c.Request.ID != game.ReqID("t", i+1) {
					t.Errorf("request %d id = %s", i, c.Request.ID)
				}
				if c.Request.CreatedAt < last {
					t.Errorf("createdAt decreases at %s", c.Request.ID)
				}
				last = c.Request.CreatedAt
			}
			// 添字 = ID の連番 - 1（上で確かめた）。
			status := make([]string, len(created))
			final := 0
			for _, ev := range a {
				switch d := ev.Data.(type) {
				case RequestCreated:
					status[slices.IndexFunc(created, func(c RequestCreated) bool { return c.Request.ID == d.Request.ID })] = d.Request.Status
				case RequestUpdated:
					i := slices.IndexFunc(created, func(c RequestCreated) bool { return c.Request.ID == d.ID })
					if i < 0 || status[i] == "" {
						t.Fatalf("request.updated before created: %+v", d)
					}
					prev := status[i]
					if rank(prev) == 2 || rank(d.Status) < rank(prev) {
						t.Errorf("%s: %s after %s", d.ID, d.Status, prev)
					}
					if d.Expired {
						t.Errorf("%s: expired is true", d.ID)
					}
					status[i] = d.Status
					if rank(d.Status) == 2 {
						final++
					}
				}
			}
			t.Logf("%d requests, %d succeeded or rejected", len(created), final)
			if tc.wantFinal && final == 0 {
				t.Error("no request became succeeded or rejected")
			}
		})
	}
}
