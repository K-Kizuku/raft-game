package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/handoff"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host/sqlitestore"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// Handoff の試験の道具立て（DTN-08 #165）。2 つの島（fakeIsland）と 1 隻の船（本物の ferryactor）の Actor Host を、
// 同じプロセスの CLA（host.PeerFunc）でつなぎ、Coordinator の役（台帳 handoff.Ledger・journal・dispatch）を
// この試験が持つ。Coordinator（DTN-05）が入ったら、この役はそちらに置き換わる。
//
// 止める位置は handoff-and-contact.md §3.3 の境界のすべて（Host が止まる: Store を開き直して Host を作り直し、
// Container は動き続ける。Coordinator が止まる: 台帳を journal から作り直し、Open の transfer を transfer.status と
// handoff.Resume で収束させる）。

const repoRoot = "../../../.."

var (
	stagesOnce sync.Once
	stagesList []*stage.Stage
	stagesErr  error
)

func repoStage(t *testing.T, id string) ([]*stage.Stage, *stage.Stage) {
	t.Helper()
	if _, err := os.Stat(repoRoot + "/data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない")
	}
	stagesOnce.Do(func() {
		stagesList, stagesErr = stage.LoadDir(os.DirFS(repoRoot), "data/islands.json", "data/stages")
	})
	if stagesErr != nil {
		t.Fatal(stagesErr)
	}
	for _, st := range stagesList {
		if st.ID == id {
			return stagesList, st
		}
	}
	t.Fatalf("ステージ %s がない", id)
	return nil, nil
}

const hoSession = "s_handoff"

var errCrash = errors.New("crash injected")

// countConn は Container への要求を数える ActorConn。
type countConn struct {
	inner host.ActorConn
	kinds []dtnproto.Kind
}

func (c *countConn) Call(ctx context.Context, raw []byte) ([]byte, error) {
	var env struct {
		Kind dtnproto.Kind `json:"kind"`
	}
	_ = json.Unmarshal(raw, &env)
	c.kinds = append(c.kinds, env.Kind)
	return c.inner.Call(ctx, raw)
}

func (c *countConn) Close() error { return c.inner.Close() }

// hnode は 1 つの Actor Host（と、その後ろの Container）。
type hnode struct {
	ref    dtnproto.ActorRef
	path   string
	conn   *countConn
	store  *sqlitestore.Store
	h      *host.Host
	island *fakeIsland // 船なら nil

	curKind  dtnproto.Kind // いま処理している要求の種類（止める位置の判定に使う）
	armKind  dtnproto.Kind
	armPoint host.Point
	crashes  int
}

// op は Coordinator が 1 つの transfer について覚えていること（journal の intent に入る中身）。
type op struct {
	name     string
	src, dst dtnproto.ActorRef
	prepare  dtnproto.Request
	t        int64
	then     dtnproto.Kind // 受け取り側の then（cargo.add・deliver・unload）
	gc       dtnproto.Kind // 送り元の then（port.remove・relay.remove・cargo.remove）
}

// centry は Coordinator の journal の 1 行（persistence-and-recovery.md §4.2 の「Handoff の台帳の段階」と dispatch）。
type centry struct {
	kind   string // create・propose・commit・dispatch-activate・activated・dispatch-gc・collected
	letter ferry.LetterID
	place  handoff.Place
	tr     handoff.Transfer
	op     *op
	id     string
	ack    handoff.Ack
	req    dtnproto.Request
}

type dispatched struct{ activate, gc *dtnproto.Request }

// crashSpec は 1 つの場面で止める位置。
type crashSpec struct {
	flow   string        // 止める transfer の操作の名前
	who    string        // src・dst・coord
	kind   dtnproto.Kind // Host が止まるとき: 止める要求の種類
	point  host.Point    // Host が止まるとき: 止める位置
	cpoint string        // Coordinator が止まるとき: afterAck・afterCommit・afterActivate・afterGC
}

func (c crashSpec) String() string {
	if c.who == "coord" {
		return fmt.Sprintf("%s/coord/%s", c.flow, c.cpoint)
	}
	return fmt.Sprintf("%s/%s/%s/%s", c.flow, c.who, c.kind, c.point)
}

type world struct {
	t      *testing.T
	stages []*stage.Stage
	st     *stage.Stage
	nodes  map[dtnproto.ActorRef]*hnode
	order  []dtnproto.ActorRef

	home, other, third uint64
	boat               dtnproto.ActorRef
	createdAt          map[ferry.LetterID]int64

	seq        uint64
	coordEpoch uint64
	ledger     *handoff.Ledger
	journal    []centry
	ops        map[string]*op
	disp       map[string]*dispatched

	crash        crashSpec
	flow         string // いま進めている操作
	coordArmed   string
	coordCrashes int
	claCalls     int
	dispatches   int
	resumes      []handoff.Action
	srcBytesSeen int // gc の前に送り元の bytes が残っていた transfer の数
}

func newWorld(t *testing.T, crash crashSpec) *world {
	t.Helper()
	stages, st := repoStage(t, "stage1")
	home := st.Ferries[0].Home
	n := uint64(len(st.Islands))
	w := &world{t: t, stages: stages, st: st, nodes: map[dtnproto.ActorRef]*hnode{}, home: home,
		other: home%n + 1, third: (home+1)%n + 1, boat: dtnproto.FerryRef(ferry.FerryID(st.Ferries[0].ID)),
		createdAt: map[ferry.LetterID]int64{}, coordEpoch: 1, ledger: handoff.NewLedger(), ops: map[string]*op{},
		disp: map[string]*dispatched{}, crash: crash}
	codec := bpv7.LetterCodec{LifetimeMs: uint64(st.DurationMs + 60_000)}
	h, o, x := w.home, w.other, w.third
	scripts := map[uint64][]ferry.Letter{
		h: {raftLetter(h, 1, o, 100, 1), raftLetter(h, 2, o, 200, 1), raftLetter(h, 3, x, 300, 1)},
		o: {raftLetter(o, 1, x, 400, 2), raftLetter(o, 2, h, 500, 2)},
	}
	dir := t.TempDir()
	for _, id := range []uint64{h, o} {
		isl := newFakeIsland(id, codec, scripts[id])
		w.addNode(dtnproto.IslandRef(id), runtime.New(isl), isl, dir)
	}
	w.addNode(w.boat, runtime.New(ferryactor.New(stages)), nil, dir)
	return w
}

func (w *world) addNode(ref dtnproto.ActorRef, rt *runtime.Runtime, isl *fakeIsland, dir string) {
	w.t.Cleanup(rt.Close)
	n := &hnode{ref: ref, path: filepath.Join(dir, fmt.Sprintf("actor-%d.sqlite", len(w.order))),
		conn: &countConn{inner: host.RuntimeConn{RT: rt}}, island: isl}
	w.nodes[ref] = n
	w.order = append(w.order, ref)
	w.open(n)
	w.t.Cleanup(func() { _ = n.h.Close() })
}

func (w *world) config(n *hnode) host.Config {
	return host.Config{
		SessionID: hoSession, Actor: n.ref, Now: fixedNow, Contacts: host.RecordOnlyContacts{},
		Peer: host.PeerFunc(w.cla),
		Hooks: host.Hooks{At: func(p host.Point) error {
			if n.armPoint != "" && p == n.armPoint && n.curKind == n.armKind {
				n.armPoint = ""
				n.crashes++
				return errCrash
			}
			return nil
		}},
	}
}

// open は Store を開き直して Host を作り直す（Host が止まったのと同じ。Container は動き続ける）。
func (w *world) open(n *hnode) {
	w.t.Helper()
	if n.h != nil {
		_ = n.h.Close()
	}
	s, err := sqlitestore.Open(n.path)
	if err != nil {
		w.t.Fatal(err)
	}
	h, err := host.New(s, n.conn, w.config(n))
	if err != nil {
		w.t.Fatal(err)
	}
	n.store, n.h = s, h
}

// cla は同じプロセスの CLA。受け取る Host が止まったら開き直し、送り元には「届かなかった」を返す。
func (w *world) cla(ctx context.Context, raw []byte) ([]byte, error) {
	w.claCalls++
	var env struct {
		Actor dtnproto.ActorRef `json:"actor"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	n, ok := w.nodes[env.Actor]
	if !ok {
		return nil, fmt.Errorf("no host %s", env.Actor)
	}
	n.curKind = dtnproto.KindTransferAccept
	b, err := n.h.Handle(ctx, raw)
	if errors.Is(err, host.ErrStopped) {
		w.open(n)
		return nil, errors.New("peer stopped")
	}
	return b, err
}

func (w *world) next() uint64 { w.seq++; return w.seq }

func (w *world) envelope(ref dtnproto.ActorRef, seq uint64, kind dtnproto.Kind, payload any) dtnproto.Request {
	b, err := json.Marshal(payload)
	if err != nil {
		w.t.Fatal(err)
	}
	return dtnproto.Request{V: 1, SessionID: hoSession, Actor: ref, Epoch: 1, CoordEpoch: w.coordEpoch, EventSeq: seq, Kind: kind, Payload: b}
}

// send は Session Front の役。Host が止まったら開き直して、not_ready なら、同じ eventSeq で送り直す。
func (w *world) send(ref dtnproto.ActorRef, req dtnproto.Request) dtnproto.Response {
	w.t.Helper()
	w.dispatches++
	req.CoordEpoch = w.coordEpoch
	raw, err := json.Marshal(req)
	if err != nil {
		w.t.Fatal(err)
	}
	for range 8 {
		n := w.nodes[ref]
		n.curKind = req.Kind
		b, err := n.h.Handle(context.Background(), raw)
		if errors.Is(err, host.ErrStopped) {
			w.open(n)
			continue
		}
		if err != nil {
			w.t.Fatalf("%s %s: %v", ref, req.Kind, err)
		}
		var resp dtnproto.Response
		if err := json.Unmarshal(b, &resp); err != nil {
			w.t.Fatalf("%s %s: response %s", ref, req.Kind, b)
		}
		if resp.Status == dtnproto.StatusError && resp.Error.Code == dtnproto.CodeNotReady {
			continue
		}
		return resp
	}
	w.t.Fatalf("%s %s: no answer after resends", ref, req.Kind)
	return dtnproto.Response{}
}

func (w *world) mustOK(ref dtnproto.ActorRef, req dtnproto.Request) dtnproto.Response {
	w.t.Helper()
	resp := w.send(ref, req)
	if resp.Status != dtnproto.StatusOK {
		w.t.Fatalf("%s %s (eventSeq %d): %+v", ref, req.Kind, req.EventSeq, resp.Error)
	}
	return resp
}

func (w *world) record(e centry) {
	w.journal = append(w.journal, e)
	w.apply(e)
}

// apply は journal の 1 行を台帳と dispatch に反映する（記録するときと、Coordinator の復元の再生で同じ処理）。
func (w *world) apply(e centry) {
	w.t.Helper()
	var err error
	switch e.kind {
	case "create":
		err = w.ledger.Create(e.letter, e.place)
	case "propose":
		w.ops[e.tr.ID] = e.op
		err = w.ledger.Propose(e.tr)
	case "commit":
		if tr, _ := w.ledger.Transfer(e.id); tr.Stage == handoff.Proposed {
			err = w.ledger.Persisted(e.id, e.ack)
		}
		if err == nil {
			err = w.ledger.Commit(e.id)
		}
	case "dispatch-activate":
		r := e.req
		w.dispatchOf(e.id).activate = &r
	case "activated":
		err = w.ledger.Activated(e.id)
	case "dispatch-gc":
		r := e.req
		w.dispatchOf(e.id).gc = &r
	case "collected":
		err = w.ledger.Collected(e.id)
	default:
		err = fmt.Errorf("unknown journal entry %q", e.kind)
	}
	if err != nil {
		w.t.Fatalf("journal %s: %v", e.kind, err)
	}
}

func (w *world) dispatchOf(id string) *dispatched {
	d := w.disp[id]
	if d == nil {
		d = &dispatched{}
		w.disp[id] = d
	}
	return d
}

// coordCrash は、Coordinator がこの位置で止まる場面なら true（1 回だけ）。
func (w *world) coordCrash(point string) bool {
	if w.coordArmed == point {
		w.coordArmed = ""
		w.coordCrashes++
		return true
	}
	return false
}

// restartCoordinator は Coordinator を作り直す: coordEpoch を 1 増やし、台帳と dispatch を journal の再生で作り直し、
// Open の transfer を両方の Host の transfer.status と handoff.Resume で収束させる（persistence-and-recovery.md §4.2）。
func (w *world) restartCoordinator() {
	w.t.Helper()
	w.coordEpoch++
	w.ledger, w.ops, w.disp = handoff.NewLedger(), map[string]*op{}, map[string]*dispatched{}
	for _, e := range w.journal {
		w.apply(e)
	}
	for _, tr := range w.ledger.Open() {
		o := w.ops[tr.ID]
		src, _ := w.status(o.src, tr.ID)
		dst, dstAck := w.status(o.dst, tr.ID)
		a := handoff.Resume(tr.Stage, src, dst)
		w.resumes = append(w.resumes, a)
		switch a {
		case handoff.ActionCommit:
			if dstAck == nil {
				w.t.Fatalf("%s: resume commit without the receiver's ack", tr.ID)
			}
			w.record(centry{kind: "commit", id: tr.ID, ack: handoff.Ack{TransferID: dstAck.TransferID, Digests: dstAck.Digests}})
		case handoff.ActionResendPrepare:
			if tr.Stage != handoff.Proposed {
				w.t.Fatalf("%s: resend prepare at %s", tr.ID, tr.Stage)
			}
		case handoff.ActionActivate:
			if tr.Stage != handoff.Committed {
				w.t.Fatalf("%s: activate at %s", tr.ID, tr.Stage)
			}
		case handoff.ActionGC:
			if tr.Stage != handoff.Activated {
				w.t.Fatalf("%s: gc at %s", tr.ID, tr.Stage)
			}
		default:
			w.t.Fatalf("%s: resume %s at %s (src %s, dst %s): %s", tr.ID, a, tr.Stage, src, dst, handoff.Explain(tr.Stage, src, dst))
		}
	}
}

func (w *world) status(ref dtnproto.ActorRef, tid string) (handoff.HostTransferState, *dtnproto.TransferAck) {
	w.t.Helper()
	resp := w.mustOK(ref, w.envelope(ref, 0, dtnproto.KindTransferStatus, dtnproto.TransferIDPayload{TransferID: tid}))
	var st dtnproto.TransferStatus
	if len(resp.Outputs) != 1 || resp.Outputs[0].Type != dtnproto.OutputTransferStatus || json.Unmarshal(resp.Outputs[0].Data, &st) != nil {
		w.t.Fatalf("transfer.status: %+v", resp.Outputs)
	}
	return handoff.HostTransferState(st.State), st.Ack
}

func (w *world) contact(island uint64) dtnproto.Contact {
	return dtnproto.Contact{ContactID: fmt.Sprintf("k%d", w.next()), SessionID: hoSession, Island: island,
		Ferry: w.st.Ferries[0].ID, IslandEpoch: 1, FerryEpoch: 1, OpenedAt: 0, Sig: "dGVzdA=="}
}

// handoff は 1 つの操作（handoff-and-contact.md §3.2 の表の 1 行）を Handoff で行う。final は手紙ごとの Commit の
// 後の持ち主（nil なら全部 to）。
func (w *world) handoff(name string, from, to handoff.Place, letters []ferry.LetterID, final []handoff.Place, t int64, k dtnproto.Contact) string {
	w.t.Helper()
	w.flow = name
	src, dst := dtnproto.ActorRef(from.Actor()), dtnproto.ActorRef(to.Actor())
	if w.crash.flow == name {
		switch w.crash.who {
		case "src":
			w.nodes[src].armKind, w.nodes[src].armPoint = w.crash.kind, w.crash.point
		case "dst":
			w.nodes[dst].armKind, w.nodes[dst].armPoint = w.crash.kind, w.crash.point
		case "coord":
			w.coordArmed = w.crash.cpoint
		}
	}
	o := &op{name: name, src: src, dst: dst, t: t}
	switch {
	case from.IsIsland() && from.Slot() == "port":
		o.then, o.gc = dtnproto.KindCargoAdd, dtnproto.KindPortRemove
	case from.IsIsland():
		o.then, o.gc = dtnproto.KindCargoAdd, dtnproto.KindRelayRemove
	case final != nil && final[0] == handoff.Delivered:
		o.then, o.gc = dtnproto.KindDeliver, dtnproto.KindCargoRemove
	default:
		o.then, o.gc = dtnproto.KindUnload, dtnproto.KindCargoRemove
	}
	seq := w.next()
	tid := handoff.TransferID(seq)
	o.prepare = w.envelope(src, seq, dtnproto.KindTransferPrepare,
		dtnproto.TransferPreparePayload{TransferID: tid, T: t, To: dst, Contact: k, LetterIDs: letters})
	w.record(centry{kind: "propose", op: o, tr: handoff.Transfer{ID: tid, From: from, To: to, ContactID: k.ContactID, Letters: letters, Final: final}})
	w.drive(tid)
	if w.crash.flow == name {
		crashed := w.coordCrashes
		for _, n := range w.nodes {
			crashed += n.crashes
		}
		if crashed != 1 {
			w.t.Fatalf("%s: %d crashes happened, want 1", w.crash, crashed)
		}
	}
	return tid
}

// drive は transfer tid を、台帳の段階から Collected まで進める（Coordinator の手順。handoff-and-contact.md §3.4）。
func (w *world) drive(tid string) {
	w.t.Helper()
	for range 20 {
		tr, _ := w.ledger.Transfer(tid)
		o := w.ops[tid]
		switch tr.Stage {
		case handoff.Proposed:
			resp := w.mustOK(o.src, o.prepare)
			var ack dtnproto.TransferAck
			if len(resp.Outputs) != 1 || resp.Outputs[0].Type != dtnproto.OutputTransferAck || json.Unmarshal(resp.Outputs[0].Data, &ack) != nil {
				w.t.Fatalf("%s: prepare outputs %+v", tid, resp.Outputs)
			}
			if w.coordCrash("afterAck") {
				w.restartCoordinator()
				continue
			}
			w.record(centry{kind: "commit", id: tid, ack: handoff.Ack{TransferID: ack.TransferID, Digests: ack.Digests}})
			if w.coordCrash("afterCommit") {
				w.restartCoordinator()
				continue
			}
		case handoff.Committed:
			d := w.dispatchOf(tid)
			if d.activate == nil {
				w.record(centry{kind: "dispatch-activate", id: tid, req: w.envelope(o.dst, w.next(), dtnproto.KindTransferActivate,
					dtnproto.TransferThenPayload{TransferID: tid, Then: w.activateThen(tr, o)})})
			}
			w.mustOK(o.dst, *w.dispatchOf(tid).activate)
			if w.coordCrash("afterActivate") {
				w.restartCoordinator()
				continue
			}
			w.record(centry{kind: "activated", id: tid})
		case handoff.Activated:
			d := w.dispatchOf(tid)
			if d.gc == nil {
				// gc を送る前（送り直しでない）なら、送り元の写しが残っている
				w.checkSourceBytes(tr, o)
				w.record(centry{kind: "dispatch-gc", id: tid, req: w.envelope(o.src, w.next(), dtnproto.KindTransferGC,
					dtnproto.TransferThenPayload{TransferID: tid, Then: dtnproto.Then{Kind: o.gc,
						Payload: mustMarshal(w.t, dtnproto.LetterIDsPayload{LetterIDs: tr.Letters})}})})
			}
			w.mustOK(o.src, *w.dispatchOf(tid).gc)
			if w.coordCrash("afterGC") {
				w.restartCoordinator()
				continue
			}
			w.record(centry{kind: "collected", id: tid})
		case handoff.Collected:
			return
		default:
			w.t.Fatalf("%s: stage %s", tid, tr.Stage)
		}
	}
	w.t.Fatalf("%s did not finish", tid)
}

// activateThen は受け取り側の then（bytes を持たない。digest は ACK の値）を作る。
func (w *world) activateThen(tr handoff.Transfer, o *op) dtnproto.Then {
	letters := make([]dtnproto.ThenLetter, len(tr.Letters))
	for i, id := range tr.Letters {
		letters[i] = dtnproto.ThenLetter{LetterID: id, CreatedAt: w.createdAt[id], Digest: tr.Digests[i]}
	}
	var payload any = dtnproto.ThenLettersPayload{T: o.t, Letters: letters}
	if o.then == dtnproto.KindUnload {
		ul := make([]dtnproto.ThenUnloadLetter, len(letters))
		for i := range letters {
			place := dtnproto.PlaceRelay
			if tr.FinalOf(i).Slot() == "port" {
				place = dtnproto.PlacePort
			}
			ul[i] = dtnproto.ThenUnloadLetter{ThenLetter: letters[i], Place: place}
		}
		slices.SortFunc(ul, func(a, b dtnproto.ThenUnloadLetter) int { return compareID(a.LetterID, b.LetterID) })
		payload = dtnproto.ThenUnloadPayload{T: o.t, Letters: ul}
	}
	return dtnproto.Then{Kind: o.then, Payload: mustMarshal(w.t, payload)}
}

func compareID(a, b ferry.LetterID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// checkSourceBytes は、gc の前に送り元の Host に prepared の bytes が残っていることを確かめる（D-5）。
func (w *world) checkSourceBytes(tr handoff.Transfer, o *op) {
	w.t.Helper()
	snap := w.snapshot(o.src)
	for _, id := range tr.Letters {
		i := slices.IndexFunc(snap.Bundles, func(b host.BundleRow) bool { return b.LetterID == uint64(id) })
		if i < 0 || snap.Bundles[i].State != host.BundlePrepared || len(snap.Bundles[i].Bundle) == 0 || snap.Bundles[i].TransferID != tr.ID {
			w.t.Fatalf("%s: the source copy of letter %d is gone before gc: %+v", tr.ID, id, snap.Bundles)
		}
	}
	w.srcBytesSeen++
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (w *world) snapshot(ref dtnproto.ActorRef) host.Snapshot {
	w.t.Helper()
	var snap host.Snapshot
	if err := w.nodes[ref].store.View(func(tx host.Tx) (err error) { snap, err = tx.Snapshot(); return err }); err != nil {
		w.t.Fatal(err)
	}
	return snap
}

func (w *world) inspect(ref dtnproto.ActorRef) json.RawMessage {
	w.t.Helper()
	resp := w.mustOK(ref, w.envelope(ref, 0, dtnproto.KindInspect, struct{}{}))
	return resp.Outputs[0].Data
}

func (w *world) islandView(id uint64) islandView {
	w.t.Helper()
	var v islandView
	if err := json.Unmarshal(w.inspect(dtnproto.IslandRef(id)), &v); err != nil {
		w.t.Fatal(err)
	}
	return v
}

func (w *world) cargo() []ferry.LetterID {
	w.t.Helper()
	var v struct {
		Cargo []struct {
			LetterID ferry.LetterID `json:"letterId"`
		} `json:"cargo"`
	}
	if err := json.Unmarshal(w.inspect(w.boat), &v); err != nil {
		w.t.Fatal(err)
	}
	ids := []ferry.LetterID{}
	for _, c := range v.Cargo {
		ids = append(ids, c.LetterID)
	}
	return ids
}

func letterOf(island, n uint64) ferry.LetterID { return ferry.LetterID(island*1_000_000_000 + n) }

// initAll は 3 つの Actor を init し、島を進めて台本の手紙を作る（台帳に Create する）。
func (w *world) initAll() {
	w.t.Helper()
	for _, ref := range w.order {
		w.mustOK(ref, w.envelope(ref, w.next(), dtnproto.KindInit,
			dtnproto.InitPayload{Stage: w.st.ID, StageHash: w.st.Hash, Seed: 1, Actor: ref}))
	}
	for _, id := range []uint64{w.home, w.other} {
		ref := dtnproto.IslandRef(id)
		resp := w.mustOK(ref, w.envelope(ref, w.next(), dtnproto.KindAdvance, dtnproto.AdvancePayload{Horizon: dtnproto.Mark{T: 1000}}))
		for _, o := range resp.Outputs {
			if o.Type != host.OutputLetterCreated {
				continue
			}
			var d struct {
				LetterID ferry.LetterID `json:"letterId"`
			}
			if err := json.Unmarshal(o.Data, &d); err != nil {
				w.t.Fatal(err)
			}
			w.createdAt[d.LetterID] = o.T
			w.record(centry{kind: "create", letter: d.LetterID, place: handoff.IslandPort(id)})
		}
	}
	if len(w.createdAt) != 5 {
		w.t.Fatalf("created %d letters, want 5", len(w.createdAt))
	}
}

func (w *world) sail(t int64, to uint64, arriveAt int64) {
	w.t.Helper()
	w.mustOK(w.boat, w.envelope(w.boat, w.next(), dtnproto.KindDepart, dtnproto.DepartPayload{T: t, To: to, ArriveAt: arriveAt}))
	w.mustOK(w.boat, w.envelope(w.boat, w.next(), dtnproto.KindArrive, dtnproto.ArrivePayload{T: arriveAt, Island: to}))
}

// 場面の操作の名前。
const (
	flowLoadHome    = "load-home"     // 島 h の港 → 船（hA・hB・hC）
	flowDeliverOth  = "deliver-other" // 船 → 島 o に配達（hA・hB）
	flowLoadOther   = "load-other"    // 島 o の港 → 船（oA・oB）
	flowDeliverHome = "deliver-home"  // 船 → 島 h に配達（oB）
	flowUnloadHome  = "unload-home"   // 船 → 島 h に下ろす（hC は港・oA は倉庫。1 つの transfer）
)

var flows = []string{flowLoadHome, flowDeliverOth, flowLoadOther, flowDeliverHome, flowUnloadHome}

// play は場面全体（5 つの Handoff と航海）を進める。
func (w *world) play() {
	w.t.Helper()
	h, o := w.home, w.other
	hA, hB, hC := letterOf(h, 1), letterOf(h, 2), letterOf(h, 3)
	oA, oB := letterOf(o, 1), letterOf(o, 2)
	cargo := handoff.FerryCargo(w.st.Ferries[0].ID)
	dl := []handoff.Place{handoff.Delivered, handoff.Delivered}

	w.initAll()
	k := w.contact(h)
	w.handoff(flowLoadHome, handoff.IslandPort(h), cargo, []ferry.LetterID{hA, hB, hC}, nil, 1000, k)
	w.sail(1500, o, 2500)
	k = w.contact(o)
	w.handoff(flowDeliverOth, cargo, handoff.IslandPort(o), []ferry.LetterID{hA, hB}, dl, 2500, k)
	w.handoff(flowLoadOther, handoff.IslandPort(o), cargo, []ferry.LetterID{oA, oB}, nil, 4000, k)
	w.sail(4500, h, 5500)
	k = w.contact(h)
	w.handoff(flowDeliverHome, cargo, handoff.IslandPort(h), []ferry.LetterID{oB}, dl[:1], 5500, k)
	// 自島の手紙（hC）と他島の手紙（oA）が混ざった下ろす操作も 1 つの transfer（手紙の ID の昇順）
	down := []ferry.LetterID{hC, oA}
	slices.SortFunc(down, compareID)
	final := make([]handoff.Place, len(down))
	for i, id := range down {
		final[i] = handoff.IslandRelay(h)
		if id == hC {
			final[i] = handoff.IslandPort(h)
		}
	}
	w.handoff(flowUnloadHome, cargo, handoff.IslandPort(h), down, final, 6000, k)
}

// checkFinal は場面の最後の状態（持ち主が 1 つ・宛先の適用が 1 回・物理的な写しが持ち主の Host だけ）を確かめる。
func (w *world) checkFinal() {
	w.t.Helper()
	h, o := w.home, w.other
	hA, hB, hC := letterOf(h, 1), letterOf(h, 2), letterOf(h, 3)
	oA, oB := letterOf(o, 1), letterOf(o, 2)
	wantOwner := map[ferry.LetterID]handoff.Place{
		hA: handoff.Delivered, hB: handoff.Delivered, oB: handoff.Delivered,
		hC: handoff.IslandPort(h), oA: handoff.IslandRelay(h),
	}
	for _, id := range []ferry.LetterID{hA, hB, hC, oA, oB} {
		if got := w.ledger.Owner(id); got != wantOwner[id] {
			w.t.Errorf("owner of %d = %q, want %q", id, got, wantOwner[id])
		}
		if hd := w.ledger.Holder(id); hd != "" {
			w.t.Errorf("letter %d is still held by %s", id, hd)
		}
		// 物理的な写し（held・prepared・pending の行）は、持ち主の Host に 1 つだけ
		var holders []string
		for _, ref := range w.order {
			for _, b := range w.snapshot(ref).Bundles {
				if b.LetterID == uint64(id) && b.State != host.BundleGone {
					holders = append(holders, fmt.Sprintf("%s:%s/%s", ref, b.State, b.Place))
				}
			}
		}
		want := 0
		if wantOwner[id] != handoff.Delivered {
			want = 1
		}
		if len(holders) != want {
			w.t.Errorf("letter %d has copies %v, want %d", id, holders, want)
		}
	}
	if open := w.ledger.Open(); len(open) != 0 {
		w.t.Errorf("open transfers: %+v", open)
	}
	if got, want := w.islandView(h), (islandView{Port: []ferry.LetterID{hC}, Relay: []ferry.LetterID{oA}, Delivered: []ferry.LetterID{oB}}); !reflect.DeepEqual(got, want) {
		w.t.Errorf("island %d = %+v, want %+v", h, got, want)
	}
	if got, want := w.islandView(o), (islandView{Port: []ferry.LetterID{}, Relay: []ferry.LetterID{}, Delivered: []ferry.LetterID{hA, hB}}); !reflect.DeepEqual(got, want) {
		w.t.Errorf("island %d = %+v, want %+v", o, got, want)
	}
	if got := w.cargo(); len(got) != 0 {
		w.t.Errorf("cargo = %v, want empty", got)
	}
	if w.srcBytesSeen < len(flows) {
		w.t.Errorf("source bytes checked for %d transfers, want >= %d", w.srcBytesSeen, len(flows))
	}
}

// comparable はスナップショットから、止めたかどうかで変わってよいもの（meta.maxCoordEpoch。Coordinator の作り直しで
// 増える）を除く。
func comparable(s host.Snapshot) host.Snapshot {
	m := map[string]string{}
	for k, v := range s.Meta {
		if k != host.MetaMaxCoordEpoch {
			m[k] = v
		}
	}
	s.Meta = m
	return s
}

// crashSpecs は handoff-and-contact.md §3.3 の境界のすべて（と、その間の Host の止まる位置）。
func crashSpecs(flow string) []crashSpec {
	return []crashSpec{
		// 1. transfer.prepare の保存の前（bundle.export の応答の後）
		{flow: flow, who: "src", kind: dtnproto.KindTransferPrepare, point: host.PointAfterActorReply},
		// 2. 送り元の保存の後、accept の前
		{flow: flow, who: "src", kind: dtnproto.KindTransferPrepare, point: host.PointAfterPrepare},
		// 3. 受け取り側の保存の後、ACK が届く前（受け取り側が止まる・送り元が ACK を保存する前に止まる・
		//    ACK を保存した後に応答が失われる）
		{flow: flow, who: "dst", kind: dtnproto.KindTransferAccept, point: host.PointAfterCommit},
		{flow: flow, who: "src", kind: dtnproto.KindTransferPrepare, point: host.PointAfterPeerReply},
		{flow: flow, who: "src", kind: dtnproto.KindTransferPrepare, point: host.PointAfterCommit},
		// 4. ACK の後、CAS の前（Coordinator が止まる）
		{flow: flow, who: "coord", cpoint: "afterAck"},
		// 5. CAS の後、activate の前（Coordinator が止まる・activate の途中で受け取り側が止まる）
		{flow: flow, who: "coord", cpoint: "afterCommit"},
		{flow: flow, who: "dst", kind: dtnproto.KindTransferActivate, point: host.PointAfterIntent},
		{flow: flow, who: "dst", kind: dtnproto.KindTransferActivate, point: host.PointAfterActorReply},
		{flow: flow, who: "dst", kind: dtnproto.KindTransferActivate, point: host.PointAfterCommit},
		// 6. activate の後、gc の前（Coordinator が止まる・gc の途中で送り元が止まる）
		{flow: flow, who: "coord", cpoint: "afterActivate"},
		{flow: flow, who: "src", kind: dtnproto.KindTransferGC, point: host.PointAfterIntent},
		{flow: flow, who: "src", kind: dtnproto.KindTransferGC, point: host.PointAfterActorReply},
		{flow: flow, who: "src", kind: dtnproto.KindTransferGC, point: host.PointAfterCommit},
		{flow: flow, who: "coord", cpoint: "afterGC"},
	}
}

// TestHandoffCrashMatrix は、5 つの Handoff（積む・配達・積む・配達・自島と他島が混ざった下ろす）のそれぞれで、
// handoff-and-contact.md §3.3 の境界のどこで止めても、送り直し・Resume の後に、持ち主が 1 つに収束し、宛先の
// 適用が 1 回だけで、送り元の bytes が collected まで残り、すべての Host の保存が止めなかったときと同じになる
// ことを確かめる（AC-2。D-3・D-4・D-5）。
func TestHandoffCrashMatrix(t *testing.T) {
	base := newWorld(t, crashSpec{})
	base.play()
	base.checkFinal()
	want := map[dtnproto.ActorRef]host.Snapshot{}
	for _, ref := range base.order {
		want[ref] = comparable(base.snapshot(ref))
		checkAccounting(t, want[ref])
	}
	for _, flow := range flows {
		for _, c := range crashSpecs(flow) {
			t.Run(c.String(), func(t *testing.T) {
				w := newWorld(t, c)
				w.play()
				w.checkFinal()
				for _, ref := range w.order {
					got := comparable(w.snapshot(ref))
					if !reflect.DeepEqual(got, want[ref]) {
						t.Errorf("%s: the store differs from the run without crashes\n got: %+v\nwant: %+v", ref, got, want[ref])
					}
				}
				if c.who == "coord" && len(w.resumes) == 0 {
					t.Errorf("the coordinator restarted without resuming anything")
				}
			})
		}
	}
}

// TestHandoffRoundTrips は、積む操作 1 回あたりの往復の数と、ローカルでの実時間を測る（open-questions.md の Q-AGE）。
func TestHandoffRoundTrips(t *testing.T) {
	w := newWorld(t, crashSpec{})
	w.initAll()
	for _, ref := range w.order {
		w.nodes[ref].conn.kinds = nil
	}
	w.dispatches, w.claCalls = 0, 0
	h := w.home
	k := w.contact(h)
	start := time.Now()
	w.handoff(flowLoadHome, handoff.IslandPort(h), handoff.FerryCargo(w.st.Ferries[0].ID),
		[]ferry.LetterID{letterOf(h, 1), letterOf(h, 2), letterOf(h, 3)}, nil, 1000, k)
	elapsed := time.Since(start)
	var calls []dtnproto.Kind
	for _, ref := range w.order {
		calls = append(calls, w.nodes[ref].conn.kinds...)
	}
	slices.Sort(calls)
	wantCalls := []dtnproto.Kind{dtnproto.KindBundleExport, dtnproto.KindCargoAdd, dtnproto.KindPortRemove}
	if w.dispatches != 3 || w.claCalls != 1 || !slices.Equal(calls, wantCalls) {
		t.Fatalf("dispatches %d (want 3), CLA %d (want 1), container calls %v (want %v)", w.dispatches, w.claCalls, calls, wantCalls)
	}
	t.Logf("Q-AGE: 1 回の積む操作（3 通）= Coordinator の dispatch 3・Container への要求 3（bundle.export 1 を含む）・CLA 1、実時間 %v（SQLite のファイル・同じプロセス）", elapsed)
}
