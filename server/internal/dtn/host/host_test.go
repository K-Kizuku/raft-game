package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host/sqlitestore"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

const (
	testSession = "s_host"
	testActor   = dtnproto.ActorRef("island:1")
	testHash    = "sha256:abababababababababababababababababababababababababababababababab"
)

var fixedNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// toyIsland は試験用の決定的な Actor。advance は (今の上限, 新しい上限] の 100 ごとに tick を出し、200 ごとに
// letter.created（Bundle の bytes つき）を出す。だから上限の区切り方によらず同じ出力の列になる（D-9）。
type toyIsland struct {
	calls   atomic.Int32 // Init・Handle を呼んだ回数
	horizon dtnproto.Mark
	seq     uint64
	nextID  uint64
	removed int
}

func (a *toyIsland) Init(dtnproto.InitPayload) ([]dtnproto.Output, error) {
	a.calls.Add(1)
	a.nextID = 1000
	return []dtnproto.Output{a.out(0, "node.state", `{"role":"follower"}`)}, nil
}

func (a *toyIsland) out(t int64, typ, data string) dtnproto.Output {
	a.seq++
	return dtnproto.Output{T: t, Class: 4, Seq: a.seq, Type: typ, Data: json.RawMessage(data)}
}

func (a *toyIsland) created(t int64) dtnproto.Output {
	a.nextID++
	b := []byte("bundle-" + strconv.FormatUint(a.nextID, 10) + "-at-" + strconv.FormatInt(t, 10))
	o := a.out(t, host.OutputLetterCreated, fmt.Sprintf(`{"letterId":%d,"digest":%q,"size":%d}`, a.nextID, dtnproto.Digest(b), len(b)))
	o.Bundle = b
	return o
}

func (a *toyIsland) Handle(kind dtnproto.Kind, payload json.RawMessage) ([]dtnproto.Output, error) {
	a.calls.Add(1)
	var outs []dtnproto.Output
	switch kind {
	case dtnproto.KindAdvance:
		var p dtnproto.AdvancePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		for t := (a.horizon.T/100 + 1) * 100; t <= p.Horizon.T; t += 100 {
			outs = append(outs, a.out(t, "term.changed", fmt.Sprintf(`{"term":%d}`, t/100)))
			if t%200 == 0 {
				outs = append(outs, a.created(t))
			}
		}
		a.horizon = p.Horizon
	case dtnproto.KindDeliver:
		var p dtnproto.LettersPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		for _, l := range p.Letters {
			outs = append(outs, a.out(p.T, host.OutputLetterDelivered, fmt.Sprintf(`{"letterId":%d}`, l.LetterID)))
		}
		outs = append(outs, a.created(p.T))
	case dtnproto.KindPortRemove:
		a.removed++
	case dtnproto.KindBundleExport:
		return nil, nil
	default:
		return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "unexpected %s", kind)
	}
	return outs, nil
}

func (a *toyIsland) Inspect() (json.RawMessage, error) {
	return json.RawMessage(fmt.Sprintf(`{"seq":%d}`, a.seq)), nil
}

func (a *toyIsland) Cursor() dtnproto.Cursor {
	return dtnproto.Cursor{Horizon: a.horizon, StateDigest: dtnproto.Digest(fmt.Appendf(nil, "%d/%d/%d", a.seq, a.nextID, a.removed))}
}

func envelope(seq uint64, kind dtnproto.Kind, payload string) []byte {
	b, err := json.Marshal(dtnproto.Request{V: 1, SessionID: testSession, Actor: testActor, Epoch: 1, CoordEpoch: 1,
		EventSeq: seq, Kind: kind, Payload: json.RawMessage(payload)})
	if err != nil {
		panic(err)
	}
	return b
}

func initReq() []byte {
	return envelope(1, dtnproto.KindInit, fmt.Sprintf(`{"stage":"stage1","stageHash":%q,"seed":7,"actor":%q}`, testHash, testActor))
}

func advanceReq(seq uint64, t int64) []byte {
	return envelope(seq, dtnproto.KindAdvance, fmt.Sprintf(`{"horizon":[%d,0]}`, t))
}

func deliverReq(seq uint64, t int64, id ferry.LetterID) []byte {
	b := []byte("incoming-" + strconv.FormatUint(uint64(id), 10))
	p, _ := json.Marshal(dtnproto.LettersPayload{T: t, Letters: []dtnproto.Letter{{LetterID: id, CreatedAt: 0, Digest: dtnproto.Digest(b), Bundle: b}}})
	return envelope(seq, dtnproto.KindDeliver, string(p))
}

// scenario は試験の要求の列（init → advance → deliver → advance がまとまる → port.remove → advance）。
func scenario() [][]byte {
	return [][]byte{
		initReq(),
		advanceReq(2, 100),
		deliverReq(3, 150, 7),
		advanceReq(4, 300),
		advanceReq(5, 500), // 4 とまとまる
		envelope(6, dtnproto.KindPortRemove, `{"letterIds":[7]}`),
		advanceReq(7, 600),
	}
}

type env struct {
	t     *testing.T
	path  string
	conn  host.ActorConn
	actor *toyIsland
	cfg   host.Config
	store *sqlitestore.Store
	h     *host.Host
}

func newEnv(t *testing.T, cfg host.Config) *env {
	t.Helper()
	a := &toyIsland{}
	rt := runtime.New(a)
	t.Cleanup(rt.Close)
	cfg.SessionID, cfg.Actor, cfg.Now = testSession, testActor, fixedNow
	e := &env{t: t, path: filepath.Join(t.TempDir(), "actor.sqlite"), conn: host.RuntimeConn{RT: rt}, actor: a, cfg: cfg}
	e.open()
	t.Cleanup(func() { _ = e.h.Close() })
	return e
}

// open は Store を開き直して Host を作り直す（Host のプロセスが止まったのと同じ。Container は動き続ける）。
func (e *env) open() {
	e.t.Helper()
	if e.h != nil {
		_ = e.h.Close()
	}
	s, err := sqlitestore.Open(e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	h, err := host.New(s, e.conn, e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.store, e.h = s, h
}

func (e *env) do(raw []byte) dtnproto.Response {
	e.t.Helper()
	b, err := e.h.Handle(context.Background(), raw)
	if err != nil {
		e.t.Fatalf("Handle: %v", err)
	}
	var resp dtnproto.Response
	if err := json.Unmarshal(b, &resp); err != nil {
		e.t.Fatalf("response: %v: %s", err, b)
	}
	return resp
}

func (e *env) snapshot() host.Snapshot {
	e.t.Helper()
	var snap host.Snapshot
	if err := e.store.View(func(tx host.Tx) (err error) { snap, err = tx.Snapshot(); return err }); err != nil {
		e.t.Fatal(err)
	}
	return snap
}

// checkAccounting は meta の journalRows・storedBytes が行から数え直した値と同じかを確かめる。
func checkAccounting(t *testing.T, snap host.Snapshot) {
	t.Helper()
	var stored int64
	for _, r := range snap.Journal {
		stored += int64(len(r.Payload) + len(r.Response))
	}
	for _, r := range snap.Bundles {
		stored += int64(len(r.Bundle))
	}
	if got, want := snap.Meta[host.MetaJournalRows], strconv.Itoa(len(snap.Journal)); got != want {
		t.Errorf("journalRows = %s, want %s", got, want)
	}
	if got, want := snap.Meta[host.MetaStoredBytes], strconv.FormatInt(stored, 10); got != want {
		t.Errorf("storedBytes = %s, want %s", got, want)
	}
}

func wantOK(t *testing.T, resp dtnproto.Response) {
	t.Helper()
	if resp.Status != dtnproto.StatusOK {
		t.Fatalf("status %s: %+v", resp.Status, resp.Error)
	}
}

func wantCode(t *testing.T, resp dtnproto.Response, code dtnproto.Code) {
	t.Helper()
	if resp.Error == nil || resp.Error.Code != code {
		t.Fatalf("want %s, got %s %+v", code, resp.Status, resp.Error)
	}
}

// TestCrashAndResend は、止める位置 3 つ × 要求の列のどこで止めても、開き直して同じ eventSeq で送り直すと、
// 止めなかったときと同じ保存（確定した応答・Bundle・meta）に収束し、Actor は要求ごとに 1 回だけ適用することを
// 確かめる（AC-3）。
func TestCrashAndResend(t *testing.T) {
	reqs := scenario()
	base := newEnv(t, host.Config{})
	var baseResp [][]byte
	for _, r := range reqs {
		b, err := base.h.Handle(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		baseResp = append(baseResp, b)
	}
	want := base.snapshot()
	checkAccounting(t, want)
	if len(want.Journal) != 6 { // 4 と 5 がまとまる
		t.Fatalf("journal rows = %d, want 6", len(want.Journal))
	}
	wantCalls := base.actor.calls.Load()

	for _, p := range host.Points {
		for k := range reqs {
			t.Run(fmt.Sprintf("%s/step%d", p, k), func(t *testing.T) {
				var crash atomic.Bool
				e := newEnv(t, host.Config{Hooks: host.Hooks{At: func(at host.Point) error {
					if at == p && crash.CompareAndSwap(true, false) {
						return errors.New("crash")
					}
					return nil
				}}})
				for i, r := range reqs {
					if i == k {
						crash.Store(true)
						if _, err := e.h.Handle(context.Background(), r); !errors.Is(err, host.ErrStopped) {
							t.Fatalf("want stop at %s, got %v", p, err)
						}
						e.open()
					}
					b, err := e.h.Handle(context.Background(), r)
					if err != nil {
						t.Fatal(err)
					}
					// 確定した後に止まった送り直しは保存した応答（まとめた行ならつなげた outputs）を返す
					if i != k || p != host.PointAfterCommit {
						if string(b) != string(baseResp[i]) {
							t.Fatalf("step %d response differs:\n got %s\nwant %s", i, b, baseResp[i])
						}
					}
				}
				got := e.snapshot()
				checkAccounting(t, got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("snapshot differs:\n got %+v\nwant %+v", got, want)
				}
				if c := e.actor.calls.Load(); c != wantCalls {
					t.Fatalf("actor applied %d requests, want %d", c, wantCalls)
				}
			})
		}
	}
}

// TestMergedAdvanceReplay は、まとめた advance の行の resp_digest が、まとめた上限への 1 回の再生
// （新しい Container に init と journal の行を送る）の応答の digest と一致することを確かめる（AC-4）。
func TestMergedAdvanceReplay(t *testing.T) {
	e := newEnv(t, host.Config{})
	for _, r := range scenario() {
		wantOK(t, e.do(r))
	}
	snap := e.snapshot()

	// 新しい Container に journal を event_seq の順に再生する（DTN-10 の §4.1 の手順 5 の模擬）
	rt := runtime.New(&toyIsland{})
	defer rt.Close()
	merged := false
	for _, row := range snap.Journal {
		req := dtnproto.Request{V: 1, SessionID: testSession, Actor: testActor, Epoch: 2, CoordEpoch: 1,
			EventSeq: row.EventSeq, Kind: row.Kind, Payload: row.Payload, Replay: true}
		raw, _ := json.Marshal(req)
		resp := rt.Handle(raw)
		wantOK(t, resp)
		d, err := dtnproto.ResponseDigest(resp)
		if err != nil {
			t.Fatal(err)
		}
		if d != row.RespDigest {
			t.Fatalf("eventSeq %d: replay digest %s != stored %s", row.EventSeq, d, row.RespDigest)
		}
		var stored dtnproto.Response
		if err := json.Unmarshal(row.Response, &stored); err != nil {
			t.Fatal(err)
		}
		if row.EventSeq == 5 {
			merged = true
			if len(stored.Outputs) != len(resp.Outputs) || len(stored.Outputs) < 4 {
				t.Fatalf("merged row: %d outputs, replay %d", len(stored.Outputs), len(resp.Outputs))
			}
		}
		for _, o := range stored.Outputs {
			if o.Bundle != nil {
				t.Fatalf("eventSeq %d: stored response has bundle bytes", row.EventSeq)
			}
		}
	}
	if !merged {
		t.Fatal("no merged row 5")
	}
}

// TestConcurrentSameEventSeq は、同じ eventSeq の要求が同時に 2 つ届いても、Actor に 1 回だけ送り、
// 2 つ目は 1 つ目の確定の後に冪等の確認をして同じ応答を返すことを確かめる。
func TestConcurrentSameEventSeq(t *testing.T) {
	e := newEnv(t, host.Config{})
	wantOK(t, e.do(initReq()))
	before := e.actor.calls.Load()
	var wg sync.WaitGroup
	out := make([][]byte, 2)
	for i := range out {
		wg.Go(func() {
			b, err := e.h.Handle(context.Background(), advanceReq(2, 400))
			if err != nil {
				t.Error(err)
			}
			out[i] = b
		})
	}
	wg.Wait()
	if string(out[0]) != string(out[1]) {
		t.Fatalf("responses differ:\n%s\n%s", out[0], out[1])
	}
	if got := e.actor.calls.Load() - before; got != 1 {
		t.Fatalf("actor called %d times", got)
	}
}

// failingStore は Update を失敗させる Store（SQLITE_FULL の模擬）。
type failingStore struct {
	host.Store
	n  atomic.Int32
	at int32 // 何回目の Update を失敗させるか
}

func (s *failingStore) Update(f func(host.Tx) error) error {
	if s.n.Add(1) == s.at {
		// トランザクションの中で書いてから失敗させ、ロールバックされることを確かめる
		err := s.Store.Update(func(tx host.Tx) error {
			if err := f(tx); err != nil {
				return err
			}
			return errors.New("database or disk is full (SQLITE_FULL)")
		})
		return err
	}
	return s.Store.Update(f)
}

// TestStoreFullRollsBack は、applied の確定に失敗したら internal を返し（成功の応答を返さない）、行は intent の
// まま・Bundle も書かれず、同じ eventSeq の送り直しで確定することを確かめる。
func TestStoreFullRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actor.sqlite")
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Update の回数: 1 = New の meta、2 = init の intent、3 = init の applied、4 = advance の intent、5 = advance の applied
	fs := &failingStore{Store: s, at: 5}
	rt := runtime.New(&toyIsland{})
	defer rt.Close()
	h, err := host.New(fs, host.RuntimeConn{RT: rt}, host.Config{SessionID: testSession, Actor: testActor})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	do := func(raw []byte) dtnproto.Response {
		b, err := h.Handle(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		var resp dtnproto.Response
		_ = json.Unmarshal(b, &resp)
		return resp
	}
	wantOK(t, do(initReq()))
	wantCode(t, do(advanceReq(2, 200)), dtnproto.CodeInternal)
	var snap host.Snapshot
	_ = s.View(func(tx host.Tx) (err error) { snap, err = tx.Snapshot(); return err })
	if n := len(snap.Journal); n != 2 || snap.Journal[1].Status != host.StatusIntent || len(snap.Bundles) != 0 {
		t.Fatalf("after failed commit: %+v", snap)
	}
	checkAccounting(t, snap)
	resp := do(advanceReq(2, 200))
	wantOK(t, resp)
	if len(resp.Outputs) != 3 || resp.Outputs[2].Bundle != nil { // tick 100・tick 200・letter.created
		t.Fatalf("outputs: %+v", resp.Outputs)
	}
	_ = s.View(func(tx host.Tx) (err error) { snap, err = tx.Snapshot(); return err })
	if snap.Journal[1].Status != host.StatusApplied || len(snap.Bundles) != 1 {
		t.Fatalf("after resend: %+v", snap)
	}
	checkAccounting(t, snap)
}

// TestStoredBytesAtCommit は、intent は上限に収まるが確定で上限を超えると too_large で確定しないことを確かめる。
func TestStoredBytesAtCommit(t *testing.T) {
	lim := dtnproto.DefaultLimits
	lim.MaxStoredBytes = 700
	e := newEnv(t, host.Config{Limits: lim})
	wantOK(t, e.do(initReq()))
	snap := e.snapshot()
	used, _ := strconv.Atoi(snap.Meta[host.MetaStoredBytes])
	if used > 500 {
		t.Fatalf("init used %d bytes; adjust the test", used)
	}
	// advance の payload は小さいが、応答（tick と letter.created を 4 回ずつ）と Bundle で上限を超える
	wantCode(t, e.do(advanceReq(2, 800)), dtnproto.CodeTooLarge)
	snap = e.snapshot()
	if snap.Journal[1].Status != host.StatusIntent || len(snap.Bundles) != 0 {
		t.Fatalf("committed over the limit: %+v", snap)
	}
	checkAccounting(t, snap)
}

// TestActorLostAndRestart は、Container が応答しなければ not_ready と OnActorLost（1 回）、Restart で epoch を
// 1 増やして保存することを確かめる。
func TestActorLostAndRestart(t *testing.T) {
	var lost atomic.Int32
	e := newEnv(t, host.Config{ActorTimeout: 30 * time.Millisecond, Hooks: host.Hooks{OnActorLost: func(error) { lost.Add(1) }}})
	wantOK(t, e.do(initReq()))
	_ = e.h.Close()
	// 応答しない Container につなぎ直す
	s, err := sqlitestore.Open(e.path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := host.New(s, hangConn{}, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.store, e.h = s, h
	wantCode(t, e.do(advanceReq(2, 100)), dtnproto.CodeNotReady)
	wantCode(t, e.do(advanceReq(2, 100)), dtnproto.CodeNotReady)
	wantCode(t, e.do(envelope(0, dtnproto.KindInspect, `{}`)), dtnproto.CodeNotReady)
	if lost.Load() != 1 {
		t.Fatalf("OnActorLost called %d times", lost.Load())
	}
	if e.h.Container() != host.ContainerLost {
		t.Fatalf("container = %s", e.h.Container())
	}
	rt := runtime.New(&toyIsland{})
	defer rt.Close()
	epoch, err := e.h.Restart(host.RuntimeConn{RT: rt})
	if err != nil || epoch != 2 {
		t.Fatalf("Restart = %d, %v", epoch, err)
	}
	e.open()
	if m := e.h.Meta(); m.Epoch != 2 {
		t.Fatalf("epoch after reopen = %d", m.Epoch)
	}
}

type hangConn struct{}

func (hangConn) Call(ctx context.Context, _ []byte) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangConn) Close() error { return nil }

// TestShutdownInterrupted は、Container を止めた後・保存を消す前に止まっても、もう一度 shutdown すれば
// 消えることを確かめる（D-8）。
func TestShutdownInterrupted(t *testing.T) {
	var stop atomic.Bool
	stop.Store(true)
	e := newEnv(t, host.Config{Hooks: host.Hooks{At: func(p host.Point) error {
		if p == host.PointAfterActorStop && stop.CompareAndSwap(true, false) {
			return errors.New("crash")
		}
		return nil
	}}})
	wantOK(t, e.do(initReq()))
	if _, err := e.h.Handle(context.Background(), envelope(2, dtnproto.KindShutdown, `{}`)); !errors.Is(err, host.ErrStopped) {
		t.Fatalf("want stop, got %v", err)
	}
	e.open()
	if len(e.snapshot().Journal) != 1 {
		t.Fatal("store was deleted before the stop point")
	}
	wantOK(t, e.do(envelope(2, dtnproto.KindShutdown, `{}`)))
	wantOK(t, e.do(envelope(3, dtnproto.KindShutdown, `{}`)))
	if err := e.store.View(func(host.Tx) error { return nil }); !errors.Is(err, host.ErrDestroyed) {
		t.Fatalf("store after shutdown: %v", err)
	}
}

// TestNewChecksMeta は、別のセッション・別の Actor の保存を開くと断ることを確かめる。
func TestNewChecksMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actor.sqlite")
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := host.New(s, hangConn{}, host.Config{SessionID: testSession, Actor: testActor})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Close()
	for _, c := range []struct {
		cfg  host.Config
		code dtnproto.Code
	}{
		{host.Config{SessionID: "s_other", Actor: testActor}, dtnproto.CodeSessionMismatch},
		{host.Config{SessionID: testSession, Actor: "island:2"}, dtnproto.CodeUnknownActor},
	} {
		s, err := sqlitestore.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = host.New(s, hangConn{}, c.cfg)
		if pe, ok := errors.AsType[*dtnproto.Error](err); !ok || pe.Code != c.code {
			t.Errorf("%+v: got %v, want %s", c.cfg, err, c.code)
		}
		_ = s.Close()
	}
}
