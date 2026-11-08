package host_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/handoff"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

func wantErr(t *testing.T, resp dtnproto.Response, code dtnproto.Code) {
	t.Helper()
	if resp.Status != dtnproto.StatusError || resp.Error == nil || resp.Error.Code != code {
		t.Fatalf("want %s, got %s %+v", code, resp.Status, resp.Error)
	}
}

func (w *world) bundleRow(ref dtnproto.ActorRef, id ferry.LetterID) (host.BundleRow, bool) {
	for _, b := range w.snapshot(ref).Bundles {
		if b.LetterID == uint64(id) {
			return b, true
		}
	}
	return host.BundleRow{}, false
}

func (w *world) transferRow(ref dtnproto.ActorRef, tid string) (host.TransferRow, bool) {
	for _, r := range w.snapshot(ref).Transfers {
		if r.TransferID == tid {
			return r, true
		}
	}
	return host.TransferRow{}, false
}

func (w *world) containerCalls() int {
	n := 0
	for _, ref := range w.order {
		n += len(w.nodes[ref].conn.kinds)
	}
	return n
}

// prepareOnly は Coordinator の手順 1〜3（Propose と transfer.prepare）だけを行い、ACK を返す。
func (w *world) prepareOnly(from, to handoff.Place, letters []ferry.LetterID, t int64, k dtnproto.Contact) (string, dtnproto.Request, dtnproto.Response) {
	w.t.Helper()
	src, dst := dtnproto.ActorRef(from.Actor()), dtnproto.ActorRef(to.Actor())
	seq := w.next()
	tid := handoff.TransferID(seq)
	req := w.envelope(src, seq, dtnproto.KindTransferPrepare, dtnproto.TransferPreparePayload{TransferID: tid, T: t, To: dst, Contact: k, LetterIDs: letters})
	w.record(centry{kind: "propose", op: &op{name: "manual", src: src, dst: dst, prepare: req, t: t, then: dtnproto.KindCargoAdd, gc: dtnproto.KindPortRemove},
		tr: handoff.Transfer{ID: tid, From: from, To: to, ContactID: k.ContactID, Letters: letters}})
	return tid, req, w.send(src, req)
}

// TestUnloadMixed は、自島と他島の手紙が混ざった下ろす操作が 1 つの transfer になり、unload が手紙の ID の昇順で
// 港と倉庫に分けることを確かめる。参照モデル（sim.Engine の Unload）との突き合わせは、本物の島の Actor（DTN-03）
// と Coordinator（DTN-05）が入ってから結合の試験で行う。
func TestUnloadMixed(t *testing.T) {
	w := newWorld(t, crashSpec{})
	w.play()
	h, o := w.home, w.other
	hC, oA := letterOf(h, 3), letterOf(o, 1)
	var unloadTr handoff.Transfer
	for _, e := range w.journal {
		if e.kind == "propose" && e.op.name == flowUnloadHome {
			unloadTr, _ = w.ledger.Transfer(e.tr.ID)
		}
	}
	if !slices.Equal(unloadTr.Letters, []ferry.LetterID{min(hC, oA), max(hC, oA)}) || unloadTr.Stage != handoff.Collected {
		t.Fatalf("unload transfer = %+v", unloadTr)
	}
	// 島 h の journal の unload の行: then の手紙に bytes が付き、ID の昇順・自島は port・他島は relay
	var row host.JournalRow
	for _, r := range w.snapshot(dtnproto.IslandRef(h)).Journal {
		if r.Kind == dtnproto.KindUnload {
			row = r
		}
	}
	var p dtnproto.UnloadPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil || len(p.Letters) != 2 {
		t.Fatalf("unload row: %v %s", err, row.Payload)
	}
	for i, l := range p.Letters {
		if i > 0 && l.LetterID <= p.Letters[i-1].LetterID {
			t.Errorf("unload letters are not ascending: %d after %d", l.LetterID, p.Letters[i-1].LetterID)
		}
		want := dtnproto.PlaceRelay
		if l.LetterID == hC {
			want = dtnproto.PlacePort
		}
		if l.Place != want || len(l.Bundle) == 0 || dtnproto.Digest(l.Bundle) != l.Digest {
			t.Errorf("unload letter %d: place %s (want %s), %d bytes", l.LetterID, l.Place, want, len(l.Bundle))
		}
	}
	for id, place := range map[ferry.LetterID]string{hC: host.PlacePort, oA: host.PlaceRelay} {
		b, ok := w.bundleRow(dtnproto.IslandRef(h), id)
		if !ok || b.State != host.BundleHeld || b.Place != place || len(b.Bundle) == 0 || b.TransferID != "" {
			t.Errorf("island %d letter %d row = %+v", h, id, b)
		}
	}
	if v := w.islandView(h); !slices.Equal(v.Port, []ferry.LetterID{hC}) || !slices.Equal(v.Relay, []ferry.LetterID{oA}) {
		t.Errorf("island %d = %+v", h, v)
	}
}

// TestDeliverOnce は、同じ手紙が宛先で 2 回適用されないこと（Host の eventSeq の冪等・transfer の状態・島の配達済みの
// 集合の三重）と、同じ内容で別の letterId の Raft の再送は両方届くことを確かめる（D-4）。
func TestDeliverOnce(t *testing.T) {
	w := newWorld(t, crashSpec{})
	w.initAll()
	h, o := w.home, w.other
	hA, hB := letterOf(h, 1), letterOf(h, 2)
	cargo := handoff.FerryCargo(w.st.Ferries[0].ID)
	w.handoff(flowLoadHome, handoff.IslandPort(h), cargo, []ferry.LetterID{hA, hB}, nil, 1000, w.contact(h))
	w.sail(1500, o, 2500)
	k := w.contact(o)
	tid := w.handoff(flowDeliverOth, cargo, handoff.IslandPort(o), []ferry.LetterID{hA, hB},
		[]handoff.Place{handoff.Delivered, handoff.Delivered}, 2500, k)
	isl := dtnproto.IslandRef(o)

	// 同じ内容（h → o の term 1 の heartbeat）で別の letterId の 2 通は、両方届く
	if v := w.islandView(o); !slices.Equal(v.Delivered, []ferry.LetterID{hA, hB}) {
		t.Fatalf("delivered = %v", v.Delivered)
	}
	codec := bpv7.LetterCodec{LifetimeMs: uint64(w.st.DurationMs + 60_000)}
	ra, _ := w.bundleRow(isl, hA)
	rb, _ := w.bundleRow(isl, hB)
	la, _, _, errA := codec.Decode(ra.Bundle)
	lb, _, _, errB := codec.Decode(rb.Bundle)
	if errA != nil || errB != nil || la.Raft.String() != lb.Raft.String() {
		t.Fatalf("the two letters must carry the same raft message: %v %v %v %v", errA, errB, la.Raft, lb.Raft)
	}

	// 1. 同じ eventSeq の activate の送り直しは、保存した応答を返す（Container に送らない）
	act := *w.disp[tid].activate
	before := w.containerCalls()
	first := w.mustOK(isl, act)
	again := w.mustOK(isl, act)
	if w.containerCalls() != before || !jsonEqual(t, first, again) {
		t.Fatalf("resent activate called the container (%d → %d) or changed the response", before, w.containerCalls())
	}
	// 2. 同じ transfer を別の eventSeq でもう一度 activate すると invalid（transfer は activated）
	act2 := act
	act2.EventSeq = w.next()
	wantErr(t, w.send(isl, act2), dtnproto.CodeInvalid)

	// 3. 同じ手紙の bytes を別の transfer でもう一度届けても、島が 2 回目の deliver を invalid で断る
	seq := w.next()
	tid2 := handoff.TransferID(seq)
	acc := w.envelope(isl, seq, dtnproto.KindTransferAccept, dtnproto.TransferAcceptPayload{TransferID: tid2, From: w.boat, Contact: k,
		Bundles: []dtnproto.TransferBundle{{LetterID: hA, Bundle: ra.Bundle, Digest: ra.Digest}}})
	w.mustOK(isl, acc)
	then := dtnproto.ThenLettersPayload{T: 2600, Letters: []dtnproto.ThenLetter{{LetterID: hA, CreatedAt: w.createdAt[hA], Digest: ra.Digest}}}
	act3 := w.envelope(isl, w.next(), dtnproto.KindTransferActivate,
		dtnproto.TransferThenPayload{TransferID: tid2, Then: dtnproto.Then{Kind: dtnproto.KindDeliver, Payload: mustMarshal(t, then)}})
	wantErr(t, w.send(isl, act3), dtnproto.CodeInvalid)
	if v := w.islandView(o); !slices.Equal(v.Delivered, []ferry.LetterID{hA, hB}) {
		t.Fatalf("delivered after the duplicate = %v", v.Delivered)
	}
	if r, _ := w.transferRow(isl, tid2); r.State != dtnproto.TransferPending {
		t.Fatalf("a refused activate must leave the transfer pending: %+v", r)
	}
}

// TestAbort は、committed の前の取り消しで元に戻り（送り元は held、受け取りの pending は消える）、取り消した後の
// ACK・prepare・accept を断り、committed の後の取り消しを断ることを確かめる。
func TestAbort(t *testing.T) {
	w := newWorld(t, crashSpec{})
	w.initAll()
	h := w.home
	hA, hB := letterOf(h, 1), letterOf(h, 2)
	port, cargo := handoff.IslandPort(h), handoff.FerryCargo(w.st.Ferries[0].ID)
	isl := dtnproto.IslandRef(h)
	k := w.contact(h)

	tid, prep, resp := w.prepareOnly(port, cargo, []ferry.LetterID{hA, hB}, 1000, k)
	if resp.Status != dtnproto.StatusOK {
		t.Fatalf("prepare: %+v", resp.Error)
	}
	var ack dtnproto.TransferAck
	_ = json.Unmarshal(resp.Outputs[0].Data, &ack)
	if b, _ := w.bundleRow(w.boat, hA); b.State != host.BundlePending {
		t.Fatalf("receiver row = %+v", b)
	}
	// Coordinator が取り消す（committed の前）
	if err := w.ledger.Abort(tid); err != nil {
		t.Fatal(err)
	}
	abort := func(ref dtnproto.ActorRef, id string) dtnproto.Response {
		return w.send(ref, w.envelope(ref, w.next(), dtnproto.KindTransferAbort, dtnproto.TransferIDPayload{TransferID: id}))
	}
	for range 2 { // 2 回目は何もしないで成功
		for _, ref := range []dtnproto.ActorRef{isl, w.boat} {
			if r := abort(ref, tid); r.Status != dtnproto.StatusOK {
				t.Fatalf("abort %s: %+v", ref, r.Error)
			}
		}
	}
	for _, id := range []ferry.LetterID{hA, hB} {
		if b, _ := w.bundleRow(isl, id); b.State != host.BundleHeld || b.TransferID != "" || len(b.Bundle) == 0 {
			t.Errorf("source row after abort = %+v", b)
		}
		if b, ok := w.bundleRow(w.boat, id); ok {
			t.Errorf("receiver row after abort = %+v", b)
		}
	}
	for _, ref := range []dtnproto.ActorRef{isl, w.boat} {
		if st, _ := w.status(ref, tid); st != handoff.HostAborted {
			t.Errorf("%s status = %s", ref, st)
		}
		checkAccounting(t, w.snapshot(ref))
	}
	// 取り消した後に届いた ACK は捨てる
	if err := w.ledger.Persisted(tid, handoff.Ack{TransferID: ack.TransferID, Digests: ack.Digests}); !errors.Is(err, handoff.ErrAborted) {
		t.Fatalf("late ack: %v", err)
	}
	// 取り消した transfer の prepare・accept の送り直しは断る
	wantErr(t, w.send(isl, prep), dtnproto.CodeInvalid)
	// 受け取り側に行の無い transfer の取り消しは墓石を残し、遅れて届いた accept で pending を作らない
	ghost := handoff.TransferID(w.next())
	if r := abort(w.boat, ghost); r.Status != dtnproto.StatusOK {
		t.Fatalf("abort unknown: %+v", r.Error)
	}
	b, _ := w.bundleRow(isl, hA)
	late := w.envelope(w.boat, 1, dtnproto.KindTransferAccept, dtnproto.TransferAcceptPayload{TransferID: ghost, From: isl, Contact: k,
		Bundles: []dtnproto.TransferBundle{{LetterID: hA, Bundle: b.Bundle, Digest: b.Digest}}})
	wantErr(t, w.send(w.boat, late), dtnproto.CodeInvalid)
	if _, ok := w.bundleRow(w.boat, hA); ok {
		t.Fatal("a late accept created a pending row")
	}

	// 取り消した後、同じ手紙で別の transfer を最後まで進められる
	tid2 := w.handoff(flowLoadHome, port, cargo, []ferry.LetterID{hA, hB}, nil, 1100, k)
	if w.ledger.Owner(hA) != cargo || !slices.Equal(w.cargo(), []ferry.LetterID{hA, hB}) {
		t.Fatalf("owner %q cargo %v", w.ledger.Owner(hA), w.cargo())
	}
	// committed の後は取り消さない（台帳も Host も断る）
	if err := w.ledger.Abort(tid2); !errors.Is(err, handoff.ErrTransition) {
		t.Fatalf("ledger abort after commit: %v", err)
	}
	wantErr(t, abort(w.boat, tid2), dtnproto.CodeInvalid)
	wantErr(t, abort(isl, tid2), dtnproto.CodeInvalid)
}

// TestTransferIdempotency は、transfer.* の冪等と、同じ transferId の中身の違いを conflict にすることを確かめる（AC-3）。
// あわせて、送り元の Host の確認（別の transfer の途中の手紙・Contact）と、受け取り側の bytes が失われたときを見る。
func TestTransferIdempotency(t *testing.T) {
	w := newWorld(t, crashSpec{})
	w.initAll()
	h, o := w.home, w.other
	hA, hB, hC := letterOf(h, 1), letterOf(h, 2), letterOf(h, 3)
	port, cargo := handoff.IslandPort(h), handoff.FerryCargo(w.st.Ferries[0].ID)
	isl := dtnproto.IslandRef(h)
	k := w.contact(h)

	tid, prep, resp := w.prepareOnly(port, cargo, []ferry.LetterID{hA, hB}, 1000, k)
	if resp.Status != dtnproto.StatusOK {
		t.Fatalf("prepare: %+v", resp.Error)
	}
	// prepare の送り直しは同じ ACK（Container にも CLA にも送らない）
	calls, cla := w.containerCalls(), w.claCalls
	if again := w.mustOK(isl, prep); !jsonEqual(t, resp, again) || w.containerCalls() != calls || w.claCalls != cla {
		t.Fatalf("resent prepare: %+v (container %d → %d, CLA %d → %d)", again, calls, w.containerCalls(), cla, w.claCalls)
	}
	// 同じ transferId で手紙が違う prepare は conflict
	other := prep
	other.Payload = mustMarshal(t, dtnproto.TransferPreparePayload{TransferID: tid, T: 1000, To: w.boat, Contact: k, LetterIDs: []ferry.LetterID{hA}})
	wantErr(t, w.send(isl, other), dtnproto.CodeConflict)
	// 別の transfer の途中の手紙を含む prepare は断る（1 通が 2 つの transfer に同時に入らない）
	_, _, r2 := w.prepareOnly(port, cargo, []ferry.LetterID{hB, hC}, 1000, k)
	wantErr(t, r2, dtnproto.CodeInvalid)

	// accept の送り直し: 同じ中身なら同じ ACK、digest が違えば conflict
	pa, _ := w.bundleRow(w.boat, hA)
	pb, _ := w.bundleRow(w.boat, hB)
	accept := func(bundles ...dtnproto.TransferBundle) dtnproto.Response {
		return w.send(w.boat, w.envelope(w.boat, 1, dtnproto.KindTransferAccept,
			dtnproto.TransferAcceptPayload{TransferID: tid, From: isl, Contact: k, Bundles: bundles}))
	}
	same := accept(dtnproto.TransferBundle{LetterID: hA, Bundle: pa.Bundle, Digest: pa.Digest}, dtnproto.TransferBundle{LetterID: hB, Bundle: pb.Bundle, Digest: pb.Digest})
	if same.Status != dtnproto.StatusOK || !jsonEqual(t, same.Outputs, resp.Outputs) {
		t.Fatalf("resent accept: %+v", same)
	}
	changed := slices.Clone(pb.Bundle)
	changed[len(changed)-1] ^= 0xff
	wantErr(t, accept(dtnproto.TransferBundle{LetterID: hA, Bundle: pa.Bundle, Digest: pa.Digest},
		dtnproto.TransferBundle{LetterID: hB, Bundle: changed, Digest: dtnproto.Digest(changed)}), dtnproto.CodeConflict)

	// activate の then の digest が ACK と違えば invalid
	var ack dtnproto.TransferAck
	_ = json.Unmarshal(resp.Outputs[0].Data, &ack)
	activateFor := func(id string, seq uint64, digests ...string) dtnproto.Request {
		ls := make([]dtnproto.ThenLetter, 2)
		for i, id := range []ferry.LetterID{hA, hB} {
			ls[i] = dtnproto.ThenLetter{LetterID: id, CreatedAt: w.createdAt[id], Digest: digests[i]}
		}
		return w.envelope(w.boat, seq, dtnproto.KindTransferActivate, dtnproto.TransferThenPayload{TransferID: id,
			Then: dtnproto.Then{Kind: dtnproto.KindCargoAdd, Payload: mustMarshal(t, dtnproto.ThenLettersPayload{T: 1000, Letters: ls})}})
	}
	activate := func(seq uint64, digests ...string) dtnproto.Request { return activateFor(tid, seq, digests...) }
	wantErr(t, w.send(w.boat, activate(w.next(), ack.Digests[1], ack.Digests[0])), dtnproto.CodeInvalid)
	// activate の手紙が transfer の手紙と違えば invalid
	wrong := activate(w.next(), ack.Digests...)
	wrong.Payload = mustMarshal(t, dtnproto.TransferThenPayload{TransferID: tid, Then: dtnproto.Then{Kind: dtnproto.KindCargoAdd,
		Payload: mustMarshal(t, dtnproto.ThenLettersPayload{T: 1000, Letters: []dtnproto.ThenLetter{{LetterID: hA, CreatedAt: 100, Digest: ack.Digests[0]}}})}})
	wantErr(t, w.send(w.boat, wrong), dtnproto.CodeInvalid)
	// 未知の transfer の activate・gc は invalid
	wantErr(t, w.send(w.boat, activateFor("x99999", w.next(), ack.Digests...)), dtnproto.CodeInvalid)
	gc := w.envelope(isl, w.next(), dtnproto.KindTransferGC, dtnproto.TransferThenPayload{TransferID: "x99999",
		Then: dtnproto.Then{Kind: dtnproto.KindPortRemove, Payload: mustMarshal(t, dtnproto.LetterIDsPayload{LetterIDs: []ferry.LetterID{hA}})}})
	wantErr(t, w.send(isl, gc), dtnproto.CodeInvalid)
	// gc の then の置き場所が違えば invalid（港から積んだ手紙に relay.remove）
	gc2 := w.envelope(isl, w.next(), dtnproto.KindTransferGC, dtnproto.TransferThenPayload{TransferID: tid,
		Then: dtnproto.Then{Kind: dtnproto.KindRelayRemove, Payload: mustMarshal(t, dtnproto.LetterIDsPayload{LetterIDs: []ferry.LetterID{hA, hB}})}})
	wantErr(t, w.send(isl, gc2), dtnproto.CodeInvalid)

	// 受け取り側で bytes が失われ、メタデータだけ残った: activate は internal（成功にしない・Container に送らない）
	n := w.nodes[w.boat]
	if err := n.store.Update(func(tx host.Tx) error {
		r, _, err := tx.Bundle(uint64(hA))
		if err != nil {
			return err
		}
		lost := r
		lost.Bundle = nil
		if err := tx.PutBundle(lost); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls = w.containerCalls()
	wantErr(t, w.send(w.boat, activate(w.next(), ack.Digests...)), dtnproto.CodeInternal)
	if w.containerCalls() != calls {
		t.Fatal("activate without bytes reached the container")
	}
	if r, _ := w.transferRow(w.boat, tid); r.State != dtnproto.TransferPending {
		t.Fatalf("transfer after the failed activate = %+v", r)
	}

	// Contact の確認: 別の島・別のセッション・閉じた Contact
	k2 := k
	k2.Island = o
	_, _, r := w.prepareOnly(port, cargo, []ferry.LetterID{hC}, 1000, k2)
	wantErr(t, r, dtnproto.CodeContactMismatch)
	k3 := w.contact(h)
	k3.SessionID = "s_other"
	_, _, r = w.prepareOnly(port, cargo, []ferry.LetterID{hC}, 1000, k3)
	wantErr(t, r, dtnproto.CodeContactMismatch)
	k4 := w.contact(h)
	if err := w.nodes[isl].store.Update(func(tx host.Tx) error {
		return tx.PutContact(host.ContactRow{ContactID: k4.ContactID, Body: mustMarshal(t, k4), State: host.ContactClosed})
	}); err != nil {
		t.Fatal(err)
	}
	_, _, r = w.prepareOnly(port, cargo, []ferry.LetterID{hC}, 1000, k4)
	wantErr(t, r, dtnproto.CodeContactClosed)
}

// TestTransferNotConfigured は、Contact の確認（ContactVerifier）か CLA（Peer）が無い Host は、転送を始めないことを
// 確かめる（DTN-09 が入るまでの既定）。
func TestTransferNotConfigured(t *testing.T) {
	e := newEnv(t, host.Config{})
	b := e.do(initReq())
	wantOK(t, b)
	k := dtnproto.Contact{ContactID: "k1", SessionID: testSession, Island: 1, Ferry: "f1", IslandEpoch: 1, FerryEpoch: 1, Sig: "dGVzdA=="}
	p, _ := json.Marshal(dtnproto.TransferPreparePayload{TransferID: "x2", T: 0, To: "ferry:f1", Contact: k, LetterIDs: []ferry.LetterID{1001}})
	wantCode(t, e.do(envelope(2, dtnproto.KindTransferPrepare, string(p))), dtnproto.CodeInternal)
	// contact.* は DTN-09
	cp, _ := json.Marshal(dtnproto.ContactPayload{Contact: k})
	wantCode(t, e.do(envelope(2, dtnproto.KindContactOpen, string(cp))), dtnproto.CodeInternal)
	// status は確認なしで答える（行が無ければ none）
	resp := e.do(envelope(0, dtnproto.KindTransferStatus, `{"transferId":"x2"}`))
	wantOK(t, resp)
	var st dtnproto.TransferStatus
	if err := json.Unmarshal(resp.Outputs[0].Data, &st); err != nil || st.State != dtnproto.TransferNone || st.Ack != nil {
		t.Fatalf("status = %+v %v", st, err)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return string(x) == string(y)
}
