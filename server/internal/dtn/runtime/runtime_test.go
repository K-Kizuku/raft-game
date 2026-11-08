package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

const (
	testSession = "s_test"
	testActor   = dtnproto.ActorRef("island:2")
	testHash    = "sha256:abababababababababababababababababababababababababababababababab"
)

// counterActor は試験用の Actor。advance の horizon.t を足し合わせ、適用した順を記録する。
// 同時に 2 つ以上のメソッドが呼ばれたら panic する（-race でも見つかる）。
type counterActor struct {
	busy    atomic.Int32
	inits   int
	handles int
	applied []int64 // 適用した horizon.t の順
	sum     int64
	horizon dtnproto.Mark

	outputs int   // advance が返す出力の数（0 なら 1）
	fail    error // Handle が返す誤り
	panics  bool
}

func (a *counterActor) enter() func() {
	if !a.busy.CompareAndSwap(0, 1) {
		panic("concurrent call")
	}
	return func() { a.busy.Store(0) }
}

func (a *counterActor) Init(p dtnproto.InitPayload) ([]dtnproto.Output, error) {
	defer a.enter()()
	a.inits++
	return []dtnproto.Output{{T: 0, Class: 3, Seq: 1, Type: "node.state", Data: json.RawMessage(`{"seed":` + strconv.FormatUint(p.Seed, 10) + `}`)}}, nil
}

func (a *counterActor) Handle(kind dtnproto.Kind, payload json.RawMessage) ([]dtnproto.Output, error) {
	defer a.enter()()
	a.handles++
	if a.panics {
		panic("boom")
	}
	if a.fail != nil {
		return nil, a.fail
	}
	switch kind {
	case dtnproto.KindAdvance:
		var p dtnproto.AdvancePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		a.applied = append(a.applied, p.Horizon.T)
		a.sum = a.sum*31 + p.Horizon.T
		a.horizon = p.Horizon
		n := max(a.outputs, 1)
		outs := make([]dtnproto.Output, n)
		for i := range outs {
			outs[i] = dtnproto.Output{T: p.Horizon.T, Class: 3, Seq: uint64(i + 1), Type: "term.changed", Data: json.RawMessage(`{"term":` + strconv.FormatInt(a.sum%1000, 10) + `}`)}
		}
		return outs, nil
	case dtnproto.KindBundleExport:
		return []dtnproto.Output{{Type: "bundle.exported", Data: json.RawMessage(`{}`), Bundle: []byte{1}}}, nil
	}
	return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "unexpected %s", kind)
}

func (a *counterActor) Inspect() (json.RawMessage, error) {
	defer a.enter()()
	return json.Marshal(map[string]int64{"sum": a.sum})
}

func (a *counterActor) Cursor() dtnproto.Cursor {
	return dtnproto.Cursor{Horizon: a.horizon, StateDigest: dtnproto.Digest([]byte(strconv.FormatInt(a.sum, 10)))}
}

// req は要求の電文を作る。
type req struct {
	session string
	actor   dtnproto.ActorRef
	epoch   uint64
	seq     uint64
	kind    dtnproto.Kind
	payload string
	replay  bool
}

func (r req) raw() []byte {
	if r.session == "" {
		r.session = testSession
	}
	if r.actor == "" {
		r.actor = testActor
	}
	if r.epoch == 0 {
		r.epoch = 1
	}
	b, err := json.Marshal(dtnproto.Request{V: 1, SessionID: r.session, Actor: r.actor, Epoch: r.epoch, CoordEpoch: 1,
		EventSeq: r.seq, Kind: r.kind, Payload: json.RawMessage(r.payload), Replay: r.replay})
	if err != nil {
		panic(err)
	}
	return b
}

func initReq(seq uint64) req {
	return req{seq: seq, kind: dtnproto.KindInit, payload: `{"stage":"stage2","stageHash":"` + testHash + `","seed":7,"actor":"island:2"}`}
}

func advanceReq(seq uint64, t int64) req {
	return req{seq: seq, kind: dtnproto.KindAdvance, payload: fmt.Sprintf(`{"horizon":[%d,8]}`, t)}
}

func newRT(t *testing.T, a Actor, opts ...Option) *Runtime {
	t.Helper()
	rt := New(a, opts...)
	t.Cleanup(rt.Close)
	return rt
}

func mustOK(t *testing.T, rt *Runtime, r req) dtnproto.Response {
	t.Helper()
	resp := rt.Handle(r.raw())
	if resp.Status != dtnproto.StatusOK {
		t.Fatalf("%s seq %d: %+v", r.kind, r.seq, resp.Error)
	}
	return resp
}

func wantCode(t *testing.T, rt *Runtime, r req, code dtnproto.Code) dtnproto.Response {
	t.Helper()
	resp := rt.Handle(r.raw())
	if resp.Status != dtnproto.StatusError || resp.Error == nil || resp.Error.Code != code {
		t.Fatalf("%s seq %d: got %s %+v, want %s", r.kind, r.seq, resp.Status, resp.Error, code)
	}
	return resp
}

func TestRuntimeIdempotent(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	first := rt.HandleRaw(advanceReq(2, 100).raw())
	// 同じ eventSeq・同じ digest（epoch 以外を同じにした送り直し。replay も digest に含めない）
	again := rt.HandleRaw(advanceReq(2, 100).raw())
	r := advanceReq(2, 100)
	r.replay = true
	replayed := rt.HandleRaw(r.raw())
	if !bytes.Equal(first, again) || !bytes.Equal(first, replayed) {
		t.Fatalf("responses differ:\n%s\n%s\n%s", first, again, replayed)
	}
	if a.handles != 1 {
		t.Fatalf("Handle called %d times, want 1", a.handles)
	}
	// init の送り直しも同じ応答で、Init は 1 回
	if !bytes.Equal(rt.HandleRaw(initReq(1).raw()), rt.HandleRaw(initReq(1).raw())) {
		t.Fatal("init resend differs")
	}
}

func TestRuntimeInitTwice(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	initResp := rt.HandleRaw(initReq(5).raw())
	// 同じ eventSeq なら保存した応答
	if got := rt.HandleRaw(initReq(5).raw()); !bytes.Equal(got, initResp) {
		t.Fatalf("got %s", got)
	}
	// 違う eventSeq なら invalid
	wantCode(t, rt, initReq(6), dtnproto.CodeInvalid)
	// 同じ eventSeq で中身が違えば conflict
	r := initReq(5)
	r.payload = `{"stage":"stage2","stageHash":"` + testHash + `","seed":8,"actor":"island:2"}`
	wantCode(t, rt, r, dtnproto.CodeConflict)
	// 処理が進んだ後の init も invalid
	mustOK(t, rt, advanceReq(6, 1))
	wantCode(t, rt, initReq(5), dtnproto.CodeInvalid)
	if a.inits != 1 {
		t.Fatalf("Init called %d times", a.inits)
	}
}

func TestRuntimeConflict(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	mustOK(t, rt, advanceReq(2, 100))
	wantCode(t, rt, advanceReq(2, 101), dtnproto.CodeConflict)
	if a.handles != 1 {
		t.Fatalf("Handle called %d times", a.handles)
	}
	// conflict の後も、保存した応答は変わらない
	mustOK(t, rt, advanceReq(2, 100))
}

func TestRuntimeStaleEvent(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	mustOK(t, rt, advanceReq(3, 100))
	wantCode(t, rt, advanceReq(2, 50), dtnproto.CodeStaleEvent)
	wantCode(t, rt, advanceReq(1, 50), dtnproto.CodeStaleEvent)
	if a.handles != 1 {
		t.Fatalf("Handle called %d times", a.handles)
	}
}

func TestRuntimeStaleEpoch(t *testing.T) {
	rt := newRT(t, &counterActor{})
	r := initReq(1)
	r.epoch = 3
	mustOK(t, rt, r)
	for _, e := range []uint64{2, 4} {
		a := advanceReq(2, 1)
		a.epoch = e
		resp := wantCode(t, rt, a, dtnproto.CodeStaleEpoch)
		if resp.Epoch != 3 {
			t.Fatalf("response epoch = %d, want the runtime's 3", resp.Epoch)
		}
	}
	a := advanceReq(2, 1)
	a.epoch = 3
	mustOK(t, rt, a)
}

func TestRuntimeSessionMismatch(t *testing.T) {
	rt := newRT(t, &counterActor{})
	mustOK(t, rt, initReq(1))
	r := advanceReq(2, 1)
	r.session = "s_other"
	wantCode(t, rt, r, dtnproto.CodeSessionMismatch)
}

func TestRuntimeUnknownActor(t *testing.T) {
	rt := newRT(t, &counterActor{})
	mustOK(t, rt, initReq(1))
	r := advanceReq(2, 1)
	r.actor = "island:3"
	wantCode(t, rt, r, dtnproto.CodeUnknownActor)
}

func TestRuntimeNotReadyBeforeInit(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	wantCode(t, rt, advanceReq(1, 1), dtnproto.CodeNotReady)
	wantCode(t, rt, req{seq: 1, kind: dtnproto.KindBundleExport, payload: `{"t":1,"letterIds":[1]}`}, dtnproto.CodeNotReady)
	// inspect は init の前でも答える
	resp := mustOK(t, rt, req{kind: dtnproto.KindInspect, payload: `{}`})
	if len(resp.Outputs) != 1 || string(resp.Outputs[0].Data) != `{"ready":false}` || resp.Cursor != nil {
		t.Fatalf("inspect before init: %+v", resp)
	}
	if a.handles != 0 {
		t.Fatal("actor was called")
	}
	mustOK(t, rt, initReq(2))
	mustOK(t, rt, advanceReq(3, 1))
}

func TestRuntimeTooLarge(t *testing.T) {
	a := &counterActor{outputs: dtnproto.DefaultLimits.MaxOutputs + 1}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	wantCode(t, rt, advanceReq(2, 1), dtnproto.CodeTooLarge)
	// Actor は呼ばれたので、同じ eventSeq の送り直しは同じ応答（呼び直さない）
	wantCode(t, rt, advanceReq(2, 1), dtnproto.CodeTooLarge)
	if a.handles != 1 {
		t.Fatalf("Handle called %d times", a.handles)
	}
	a.outputs = dtnproto.DefaultLimits.MaxOutputs
	resp := mustOK(t, rt, advanceReq(3, 2))
	if len(resp.Outputs) != dtnproto.DefaultLimits.MaxOutputs {
		t.Fatalf("got %d outputs", len(resp.Outputs))
	}
	// 電文の上限
	lim := dtnproto.DefaultLimits
	lim.MaxMessageBytes = 200
	rt2 := newRT(t, &counterActor{}, WithLimits(lim))
	big := initReq(1)
	big.session = "s_" + string(bytes.Repeat([]byte("x"), 100))
	wantCode(t, rt2, big, dtnproto.CodeTooLarge)
}

func TestRuntimeExportNotJournaled(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	mustOK(t, rt, advanceReq(5, 100))
	// bundle.export と inspect は eventSeq を進めない（古い・同じ・新しい番号のどれでも処理する）
	for _, seq := range []uint64{0, 5, 9} {
		resp := mustOK(t, rt, req{seq: seq, kind: dtnproto.KindBundleExport, payload: `{"t":100,"letterIds":[1]}`})
		if len(resp.Outputs) != 1 || resp.Outputs[0].Type != "bundle.exported" {
			t.Fatalf("export: %+v", resp)
		}
		resp = mustOK(t, rt, req{seq: seq, kind: dtnproto.KindInspect, payload: `{}`})
		if len(resp.Outputs) != 1 || resp.Outputs[0].Type != InspectOutputType || string(resp.Outputs[0].Data) != `{"sum":100}` {
			t.Fatalf("inspect: %+v", resp)
		}
	}
	// eventSeq 6 は、9 の export の後でもまだ新しい
	mustOK(t, rt, advanceReq(6, 200))
	// 5 の送り直しは stale（最後は 6）
	wantCode(t, rt, advanceReq(5, 100), dtnproto.CodeStaleEvent)
}

func TestRuntimeActorErrors(t *testing.T) {
	a := &counterActor{fail: dtnproto.Errorf(dtnproto.CodeContactClosed, "closed")}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	wantCode(t, rt, advanceReq(2, 1), dtnproto.CodeContactClosed)
	a.fail = errors.New("plain")
	wantCode(t, rt, advanceReq(3, 1), dtnproto.CodeInternal)
	a.fail = nil
	a.panics = true
	wantCode(t, rt, advanceReq(4, 1), dtnproto.CodeInternal)
	a.panics = false
	mustOK(t, rt, advanceReq(5, 1))
}

func TestRuntimeRejectsHostOnlyKinds(t *testing.T) {
	rt := newRT(t, &counterActor{})
	mustOK(t, rt, initReq(1))
	wantCode(t, rt, req{seq: 2, kind: dtnproto.KindTransferAbort, payload: `{"transferId":"x1"}`}, dtnproto.CodeInvalid)
	wantCode(t, rt, req{kind: dtnproto.KindHostStatus, payload: `{}`}, dtnproto.CodeInvalid)
}

func TestRuntimeShutdown(t *testing.T) {
	rt := newRT(t, &counterActor{})
	mustOK(t, rt, initReq(1))
	first := rt.HandleRaw(req{seq: 2, kind: dtnproto.KindShutdown, payload: `{}`}.raw())
	select {
	case <-rt.Stopped():
	default:
		t.Fatal("not stopped")
	}
	if got := rt.HandleRaw(req{seq: 2, kind: dtnproto.KindShutdown, payload: `{}`}.raw()); !bytes.Equal(got, first) {
		t.Fatal("shutdown resend differs")
	}
	wantCode(t, rt, advanceReq(3, 1), dtnproto.CodeNotReady)
	wantCode(t, rt, req{kind: dtnproto.KindInspect, payload: `{}`}, dtnproto.CodeNotReady)
}

func TestRuntimeValidationError(t *testing.T) {
	rt := newRT(t, &counterActor{})
	resp := rt.Handle([]byte(`{"v":2}`))
	if resp.Error == nil || resp.Error.Code != dtnproto.CodeUnsupportedVersion {
		t.Fatalf("%+v", resp)
	}
	resp = rt.Handle([]byte(`not json`))
	if resp.Error == nil || resp.Error.Code != dtnproto.CodeInvalid {
		t.Fatalf("%+v", resp)
	}
}

func TestRuntimeSerializes(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	mustOK(t, rt, initReq(1))
	const n = 100
	resps := make([]dtnproto.Response, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%10 == 0 { // journal に入れない種類も混ぜる
				_ = rt.Handle(req{seq: uint64(i + 2), kind: dtnproto.KindInspect, payload: `{}`}.raw())
			}
			resps[i] = rt.Handle(advanceReq(uint64(i+2), int64(i+2)).raw())
		}()
	}
	wg.Wait()
	// 届いた順に 1 つずつ適用した結果: 適用した eventSeq（= horizon.t）は増える一方で、
	// ok の応答はちょうど適用したもの、ほかは「それより新しい番号が先に届いた」stale_event
	if !slices.IsSorted(a.applied) || len(slices.Compact(slices.Clone(a.applied))) != len(a.applied) {
		t.Fatalf("applied out of order: %v", a.applied)
	}
	var ok []int64
	for i, r := range resps {
		switch {
		case r.Status == dtnproto.StatusOK:
			ok = append(ok, int64(i+2))
		case r.Error == nil || r.Error.Code != dtnproto.CodeStaleEvent:
			t.Fatalf("seq %d: %+v", i+2, r.Error)
		}
	}
	if !slices.Equal(ok, a.applied) || a.handles != len(ok) {
		t.Fatalf("ok %v, applied %v, handles %d", ok, a.applied, a.handles)
	}
	if len(ok) == 0 || ok[len(ok)-1] != n+1 {
		t.Fatalf("the newest request must be applied: %v", ok)
	}
}
