package host

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Handoff の Host 側（DTN-08 #165。handoff-and-contact.md §3・persistence-and-recovery.md §3.2・§7.5）。
//
//   - transfer.prepare（送り元）: 手紙の確認 → Container に bundle.export → prepared を保存 → CLA で accept →
//     ACK を保存して返す
//   - transfer.accept（受け取り）: Contact と bytes を確かめ、pending で保存して ACK を返す
//   - transfer.activate（受け取り）・transfer.gc（送り元）: journal に入れる種類として、then を Container に送る
//     （Host.journaled と thenPlan）
//   - transfer.abort（両方）・transfer.status（どちらか）
//
// prepare・accept・abort は journal に入れない（冪等は transfers の行。復元の再生にも出てこない）。
// activate・gc は then の kind と payload で journal に入る（eventSeq の冪等。再生は then をそのまま送る）。

// transfers の role の値。
const (
	RoleSource = "source"
	RoleDest   = "dest"
)

// contacts の state の値（DTN-09 が書く）。
const (
	ContactOpen   = "open"
	ContactClosed = "closed"
)

// Peer は CLA（handoff-and-contact.md §4）。送り元の Host が、受け取る Host に transfer.accept の電文 raw を
// 届け、受け取る Host の応答の電文を返す。届かない（接続が切れた・期限を過ぎた）ときは誤りを返す。
// ローカルの試験は同じプロセスの Host.Handle を直接呼ぶ。RPC CLA（Durable Object の fetch）は DTN-09。
type Peer interface {
	Accept(ctx context.Context, raw []byte) ([]byte, error)
}

// PeerFunc は関数の Peer。
type PeerFunc func(ctx context.Context, raw []byte) ([]byte, error)

// Accept は f(ctx, raw)。
func (f PeerFunc) Accept(ctx context.Context, raw []byte) ([]byte, error) { return f(ctx, raw) }

// ContactCheck は Contact の確認（handoff-and-contact.md §2.2 の 1〜5）に渡す値。
type ContactCheck struct {
	SessionID string            // この Host の meta.sessionId
	Self      dtnproto.ActorRef // この Host の Actor
	Epoch     uint64            // この Host の meta.epoch
	Peer      dtnproto.ActorRef // 相手（prepare なら to、accept なら from）
	Contact   dtnproto.Contact
	// Record は contacts の行（contact.open・contact.close で保存したもの。DTN-09）。無ければ nil。
	Record *ContactRow
}

// ContactVerifier は Contact の確認。断るときは contact_closed・contact_mismatch などの *dtnproto.Error を返す。
// 署名・epoch の確認（§2.2 の 1・4）は DTN-09 が入れる。
type ContactVerifier interface {
	Verify(c ContactCheck) *dtnproto.Error
}

// RecordOnlyContacts は試験用の ContactVerifier。署名と epoch を見ず、Contact の中身と記録だけを確かめる:
// sessionId が自分のもの・自分と相手が Contact の島と船の組・記録が閉じていない。
type RecordOnlyContacts struct{}

// Verify は c を確かめる。
func (RecordOnlyContacts) Verify(c ContactCheck) *dtnproto.Error {
	island := dtnproto.IslandRef(c.Contact.Island)
	boat := dtnproto.FerryRef(ferry.FerryID(c.Contact.Ferry))
	switch {
	case c.Contact.SessionID != c.SessionID:
		return dtnproto.Errorf(dtnproto.CodeContactMismatch, "contact %s is for session %q", c.Contact.ContactID, c.Contact.SessionID)
	case !(c.Self == island && c.Peer == boat) && !(c.Self == boat && c.Peer == island):
		return dtnproto.Errorf(dtnproto.CodeContactMismatch, "contact %s is between %s and %s, not %s and %s",
			c.Contact.ContactID, island, boat, c.Self, c.Peer)
	case c.Record != nil && c.Record.State == ContactClosed:
		return dtnproto.Errorf(dtnproto.CodeContactClosed, "contact %s is closed", c.Contact.ContactID)
	}
	return nil
}

// transferAck は応答の outputs の transfer.ack。
func transferAckOutput(ack dtnproto.TransferAck) dtnproto.Output {
	data, _ := json.Marshal(ack)
	return dtnproto.Output{Type: dtnproto.OutputTransferAck, Data: data}
}

func transferStatusOutput(st dtnproto.TransferStatus) dtnproto.Output {
	data, _ := json.Marshal(st)
	return dtnproto.Output{Type: dtnproto.OutputTransferStatus, Data: data}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("host: encode: %v", err))
	}
	return b
}

// rowLetters は transfers の行の letter_ids と digests を読む。
func rowLetters(r TransferRow) ([]ferry.LetterID, []string, error) {
	var (
		ids     []ferry.LetterID
		digests []string
	)
	if err := json.Unmarshal(r.LetterIDs, &ids); err != nil {
		return nil, nil, fmt.Errorf("transfers %s: letter_ids: %w", r.TransferID, err)
	}
	if err := json.Unmarshal(r.Digests, &digests); err != nil {
		return nil, nil, fmt.Errorf("transfers %s: digests: %w", r.TransferID, err)
	}
	return ids, digests, nil
}

func rowAck(r TransferRow) (*dtnproto.TransferAck, error) {
	if len(r.Ack) == 0 {
		return nil, nil
	}
	var a dtnproto.TransferAck
	if err := json.Unmarshal(r.Ack, &a); err != nil {
		return nil, fmt.Errorf("transfers %s: ack: %w", r.TransferID, err)
	}
	return &a, nil
}

// sameSet は a と b が同じ手紙の集合か（どちらも重複なし）。
func sameSet(a, b []ferry.LetterID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// verifyContact は Contacts で Contact を確かめる（Contacts が無ければ internal）。
func (h *Host) verifyContact(tx Tx, peer dtnproto.ActorRef, k dtnproto.Contact) *dtnproto.Error {
	if h.contacts == nil {
		return dtnproto.Errorf(dtnproto.CodeInternal, "no contact verifier is configured")
	}
	rec, ok, err := tx.Contact(k.ContactID)
	if err != nil {
		return dtnproto.Errorf(dtnproto.CodeInternal, "read contact: %v", err)
	}
	c := ContactCheck{SessionID: h.meta.SessionID, Self: h.meta.Actor, Epoch: h.meta.Epoch, Peer: peer, Contact: k}
	if ok {
		c.Record = &rec
	}
	return h.contacts.Verify(c)
}

// --- transfer.prepare（送り元） ---

func (h *Host) transferPrepare(ctx context.Context, req dtnproto.Request) ([]byte, error) {
	pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, h.lim)
	if perr != nil {
		return h.fail(req, perr), nil
	}
	p := pv.(*dtnproto.TransferPreparePayload)

	var (
		row     TransferRow
		have    bool
		last    JournalRow
		hasLast bool
		bundles []dtnproto.TransferBundle // 送る bytes（letterIds の順）
		verr    *dtnproto.Error
	)
	if err := h.store.View(func(tx Tx) (err error) {
		if last, hasLast, err = tx.LastJournal(); err != nil {
			return err
		}
		if row, have, err = tx.Transfer(p.TransferID); err != nil || have {
			return err
		}
		if verr = h.verifyContact(tx, p.To, p.Contact); verr != nil {
			return nil
		}
		// ① 手紙をすべて持っていて（held）、別の transfer の途中でない
		for _, id := range p.LetterIDs {
			b, ok, err := tx.Bundle(uint64(id))
			switch {
			case err != nil:
				return err
			case !ok || b.State == BundleGone:
				verr = dtnproto.Errorf(dtnproto.CodeInvalid, "letter %d is not held by %s", id, h.meta.Actor)
			case b.State != BundleHeld:
				verr = dtnproto.Errorf(dtnproto.CodeInvalid, "letter %d is %s in transfer %s", id, b.State, b.TransferID)
			}
			if verr != nil {
				return nil
			}
		}
		return nil
	}); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read store: %v", err)), nil
	}

	if have {
		// 送り直し: 保存した prepared を使う（Container に bundle.export を送り直さない）
		ids, digests, err := rowLetters(row)
		if err != nil {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "%v", err)), nil
		}
		ack, err := rowAck(row)
		if err != nil {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "%v", err)), nil
		}
		switch {
		case row.Role != RoleSource || row.Peer != string(p.To) || row.ContactID != p.Contact.ContactID || !slices.Equal(ids, p.LetterIDs):
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeConflict, "transfer %s was prepared with another request", p.TransferID)), nil
		case row.State == dtnproto.TransferAborted:
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s is aborted", p.TransferID)), nil
		case ack != nil:
			return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{transferAckOutput(*ack)})), nil
		case row.State != dtnproto.TransferPrepared:
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "transfer %s is %s without an ack", p.TransferID, row.State)), nil
		}
		if err := h.store.View(func(tx Tx) error {
			for i, id := range ids {
				b, ok, err := tx.Bundle(uint64(id))
				if err != nil {
					return err
				}
				if !ok || b.State != BundlePrepared || b.TransferID != p.TransferID || len(b.Bundle) == 0 || b.Digest != digests[i] {
					return fmt.Errorf("letter %d: the prepared bytes are missing", id)
				}
				bundles = append(bundles, dtnproto.TransferBundle{LetterID: id, Bundle: b.Bundle, Digest: b.Digest})
			}
			return nil
		}); err != nil {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "transfer %s: %v", p.TransferID, err)), nil
		}
		return h.sendAccept(ctx, req, p, bundles)
	}

	switch {
	case verr != nil:
		return h.fail(req, verr), nil
	case h.peer == nil:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "no peer (CLA) is configured")), nil
	case hasLast && last.Status == StatusIntent:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "eventSeq %d is pending; resend it first", last.EventSeq)), nil
	case h.lost:
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "actor lost")), nil
	}

	// ② Container に bundle.export（状態を変えない。journal に入れない）
	exp := req
	exp.Kind = dtnproto.KindBundleExport
	exp.Payload = mustJSON(dtnproto.ExportPayload{T: p.T, LetterIDs: p.LetterIDs})
	resp, cerr := h.call(ctx, exp)
	if cerr != nil {
		return h.fail(req, cerr), nil
	}
	if resp.Status != dtnproto.StatusOK {
		if resp.Error.Code == dtnproto.CodeInternal {
			h.markLost(fmt.Errorf("actor returned internal: %s", resp.Error.Message))
		}
		return h.fail(req, resp.Error), nil
	}
	bundles, perr = h.exported(p.LetterIDs, resp.Outputs)
	if perr != nil {
		return h.fail(req, perr), nil
	}
	if err := h.at(PointAfterActorReply); err != nil {
		return nil, err
	}

	// ③ transfers（source・prepared）と bundles（prepared）を 1 つのトランザクションで保存する
	digests := make([]string, len(bundles))
	for i, b := range bundles {
		digests[i] = b.Digest
	}
	if err := h.update(func(tx Tx, m *Meta) error {
		for _, b := range bundles {
			old, _, err := tx.Bundle(uint64(b.LetterID))
			if err != nil {
				return err
			}
			if err := putBundle(tx, m, uint64(b.LetterID), b.Bundle, b.Digest, BundlePrepared, old.Place, req.EventSeq); err != nil {
				return err
			}
			if err := setBundleTransfer(tx, uint64(b.LetterID), p.TransferID); err != nil {
				return err
			}
		}
		if m.StoredBytes > int64(h.lim.MaxStoredBytes) {
			return dtnproto.Errorf(dtnproto.CodeTooLarge, "store: %d bytes exceeds %d", m.StoredBytes, h.lim.MaxStoredBytes)
		}
		return tx.PutTransfer(TransferRow{TransferID: p.TransferID, Role: RoleSource, Peer: string(p.To),
			ContactID: p.Contact.ContactID, LetterIDs: mustJSON(p.LetterIDs), Digests: mustJSON(digests), State: dtnproto.TransferPrepared})
	}); err != nil {
		return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
	}
	if err := h.at(PointAfterPrepare); err != nil {
		return nil, err
	}
	return h.sendAccept(ctx, req, p, bundles)
}

// exported は bundle.export の応答の outputs（letterIds の順の bundle.exported）から、送る bytes を作る。
func (h *Host) exported(ids []ferry.LetterID, outs []dtnproto.Output) ([]dtnproto.TransferBundle, *dtnproto.Error) {
	if len(outs) != len(ids) {
		return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: %d outputs for %d letters", len(outs), len(ids))
	}
	bs := make([]dtnproto.TransferBundle, len(ids))
	for i, o := range outs {
		var d letterData
		if o.Type != OutputBundleExported || json.Unmarshal(o.Data, &d) != nil || ferry.LetterID(d.LetterID) != ids[i] {
			return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: outputs[%d] is not bundle.exported for letter %d", i, ids[i])
		}
		switch {
		case len(o.Bundle) == 0:
			return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: outputs[%d] without bundle", i)
		case len(o.Bundle) > h.lim.MaxBundleBytes:
			return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: outputs[%d]: bundle of %d bytes exceeds %d", i, len(o.Bundle), h.lim.MaxBundleBytes)
		case dtnproto.Digest(o.Bundle) != d.Digest:
			return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: outputs[%d]: data.digest does not match the bundle", i)
		case d.Size != nil && *d.Size != int64(len(o.Bundle)):
			return nil, dtnproto.Errorf(dtnproto.CodeInvalid, "bundle.export: outputs[%d]: data.size %d is not %d", i, *d.Size, len(o.Bundle))
		}
		bs[i] = dtnproto.TransferBundle{LetterID: ids[i], Bundle: o.Bundle, Digest: d.Digest}
	}
	return bs, nil
}

func setBundleTransfer(tx Tx, id uint64, transferID string) error {
	r, ok, err := tx.Bundle(id)
	if err != nil || !ok {
		return err
	}
	r.TransferID = transferID
	return tx.PutBundle(r)
}

// sendAccept は ④ CLA で受け取る Host に transfer.accept を送り、⑤ ACK を transfers.ack に保存して返す。
func (h *Host) sendAccept(ctx context.Context, req dtnproto.Request, p *dtnproto.TransferPreparePayload, bundles []dtnproto.TransferBundle) ([]byte, error) {
	if h.peer == nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "no peer (CLA) is configured")), nil
	}
	epoch := p.Contact.FerryEpoch
	if _, isIsland := p.To.IslandID(); isIsland {
		epoch = p.Contact.IslandEpoch
	}
	acc := dtnproto.Request{V: dtnproto.Version, SessionID: req.SessionID, Actor: p.To, Epoch: epoch, CoordEpoch: req.CoordEpoch,
		EventSeq: req.EventSeq, Kind: dtnproto.KindTransferAccept,
		Payload: mustJSON(dtnproto.TransferAcceptPayload{TransferID: p.TransferID, From: h.meta.Actor, Contact: p.Contact, Bundles: bundles})}
	raw := mustJSON(acc)
	cctx, cancel := context.WithTimeout(ctx, h.timeout)
	b, err := h.peer.Accept(cctx, raw)
	cancel()
	if err != nil {
		// 届かなかった（受け取る Host が止まった・保存の後に応答が失われた）。Session Front は同じ eventSeq で
		// prepare を送り直し、ここで保存済みの prepared の bytes で accept を送り直す
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "peer %s did not answer: %v", p.To, err)), nil
	}
	var resp dtnproto.Response
	if err := json.Unmarshal(b, &resp); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "peer %s returned a malformed response", p.To)), nil
	}
	if resp.Status != dtnproto.StatusOK {
		if resp.Error == nil {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeNotReady, "peer %s returned an error without a code", p.To)), nil
		}
		return h.fail(req, resp.Error), nil
	}
	var ack *dtnproto.TransferAck
	for _, o := range resp.Outputs {
		if o.Type == dtnproto.OutputTransferAck {
			var a dtnproto.TransferAck
			if json.Unmarshal(o.Data, &a) == nil {
				ack = &a
			}
		}
	}
	want := make([]string, len(bundles))
	for i, x := range bundles {
		want[i] = x.Digest
	}
	if ack == nil || ack.TransferID != p.TransferID || !slices.Equal(ack.Digests, want) {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeConflict, "peer %s acknowledged other bytes for %s", p.To, p.TransferID)), nil
	}
	if err := h.at(PointAfterPeerReply); err != nil {
		return nil, err
	}
	if err := h.update(func(tx Tx, m *Meta) error {
		r, ok, err := tx.Transfer(p.TransferID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("transfer %s disappeared", p.TransferID)
		}
		if r.State == dtnproto.TransferAborted {
			// 取り消した後に届いた ACK は捨てる
			return dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s is aborted", p.TransferID)
		}
		r.Ack = mustJSON(ack)
		return tx.PutTransfer(r)
	}); err != nil {
		return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
	}
	if err := h.at(PointAfterCommit); err != nil {
		return nil, err
	}
	return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{transferAckOutput(*ack)})), nil
}

// --- transfer.accept（受け取り） ---

func (h *Host) transferAccept(req dtnproto.Request) ([]byte, error) {
	pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, h.lim)
	if perr != nil {
		return h.fail(req, perr), nil
	}
	p := pv.(*dtnproto.TransferAcceptPayload)
	ids := make([]ferry.LetterID, len(p.Bundles))
	digests := make([]string, len(p.Bundles))
	var total int64
	for i, b := range p.Bundles {
		ids[i], digests[i] = b.LetterID, b.Digest
		total += int64(len(b.Bundle))
	}
	ack := dtnproto.TransferAck{TransferID: p.TransferID, Digests: digests}

	var (
		row  TransferRow
		have bool
		verr *dtnproto.Error
	)
	if err := h.store.View(func(tx Tx) (err error) {
		if row, have, err = tx.Transfer(p.TransferID); err != nil || have {
			return err
		}
		if verr = h.verifyContact(tx, p.From, p.Contact); verr != nil {
			return nil
		}
		for _, id := range ids {
			b, ok, err := tx.Bundle(uint64(id))
			if err != nil {
				return err
			}
			if ok && b.State != BundleGone {
				verr = dtnproto.Errorf(dtnproto.CodeInvalid, "letter %d is already %s on %s", id, b.State, h.meta.Actor)
				return nil
			}
		}
		return nil
	}); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read store: %v", err)), nil
	}

	if have {
		// 同じ transferId: 同じ中身なら同じ ACK、違えば conflict
		rids, rdigests, err := rowLetters(row)
		if err != nil {
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "%v", err)), nil
		}
		switch {
		case row.State == dtnproto.TransferAborted:
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s is aborted", p.TransferID)), nil
		case row.Role != RoleDest || row.Peer != string(p.From) || row.ContactID != p.Contact.ContactID ||
			!slices.Equal(rids, ids) || !slices.Equal(rdigests, digests):
			return h.fail(req, dtnproto.Errorf(dtnproto.CodeConflict, "transfer %s was accepted with other bytes", p.TransferID)), nil
		}
		return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{transferAckOutput(ack)})), nil
	}
	if verr != nil {
		return h.fail(req, verr), nil
	}

	// pending で保存する（activate までは Container に渡さない）。置き場所は仮に、船なら cargo・島なら port
	// （activate で then に合わせる）
	place := PlacePort
	if _, isFerry := h.meta.Actor.FerryID(); isFerry {
		place = PlaceCargo
	}
	if err := h.update(func(tx Tx, m *Meta) error {
		if m.StoredBytes+total > int64(h.lim.MaxStoredBytes) {
			return dtnproto.Errorf(dtnproto.CodeTooLarge, "store: %d+%d bytes exceeds %d", m.StoredBytes, total, h.lim.MaxStoredBytes)
		}
		for _, b := range p.Bundles {
			if err := putBundle(tx, m, uint64(b.LetterID), b.Bundle, b.Digest, BundlePending, place, req.EventSeq); err != nil {
				return err
			}
			if err := setBundleTransfer(tx, uint64(b.LetterID), p.TransferID); err != nil {
				return err
			}
		}
		return tx.PutTransfer(TransferRow{TransferID: p.TransferID, Role: RoleDest, Peer: string(p.From),
			ContactID: p.Contact.ContactID, LetterIDs: mustJSON(ids), Digests: mustJSON(digests),
			State: dtnproto.TransferPending, Ack: mustJSON(ack)})
	}); err != nil {
		return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
	}
	if err := h.at(PointAfterCommit); err != nil {
		return nil, err
	}
	return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{transferAckOutput(ack)})), nil
}

// --- transfer.activate（受け取り）・transfer.gc（送り元） ---

// activatePlan は transfer.activate の thenPlan。then の手紙に pending の bytes を付けて Container に送り、
// 確定で transfers を activated にする（bundles は then の効果で held・then の place になる）。
func (h *Host) activatePlan(req dtnproto.Request) *thenPlan {
	var p dtnproto.TransferThenPayload
	_ = json.Unmarshal(req.Payload, &p) // Validate で確かめ済み
	return &thenPlan{
		build: func(tx Tx) (dtnproto.Kind, json.RawMessage, *dtnproto.Error) {
			row, rids, perr := h.transferFor(tx, p.TransferID, RoleDest, dtnproto.TransferPending)
			if perr != nil {
				return "", nil, perr
			}
			tv, perr := dtnproto.DecodeThenPayload(p.Then.Kind, p.Then.Payload, req.Actor, h.lim)
			if perr != nil {
				return "", nil, perr
			}
			type thenLetter struct {
				dtnproto.ThenLetter
				place string
			}
			var (
				t       int64
				letters []thenLetter
			)
			switch v := tv.(type) {
			case *dtnproto.ThenLettersPayload:
				t = v.T
				for _, l := range v.Letters {
					letters = append(letters, thenLetter{ThenLetter: l})
				}
			case *dtnproto.ThenUnloadPayload:
				t = v.T
				for _, l := range v.Letters {
					letters = append(letters, thenLetter{ThenLetter: l.ThenLetter, place: l.Place})
				}
			}
			ids := make([]ferry.LetterID, len(letters))
			for i, l := range letters {
				ids[i] = l.LetterID
			}
			if !sameSet(ids, rids) {
				return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "then: letters %v are not the letters %v of transfer %s", ids, rids, row.TransferID)
			}
			full := make([]dtnproto.Letter, len(letters))
			for i, l := range letters {
				b, ok, err := tx.Bundle(uint64(l.LetterID))
				switch {
				case err != nil:
					return "", nil, dtnproto.Errorf(dtnproto.CodeInternal, "read bundle: %v", err)
				case !ok || b.State != BundlePending || b.TransferID != row.TransferID || len(b.Bundle) == 0:
					// 受け取り側で bytes が失われ、メタデータだけ残った。成功にしない
					return "", nil, dtnproto.Errorf(dtnproto.CodeInternal, "letter %d: the pending bytes of %s are missing", l.LetterID, row.TransferID)
				case b.Digest != l.Digest:
					return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "letter %d: then digest %s is not the accepted %s", l.LetterID, l.Digest, b.Digest)
				}
				full[i] = dtnproto.Letter{LetterID: l.LetterID, CreatedAt: l.CreatedAt, Digest: b.Digest, Bundle: b.Bundle}
			}
			var payload json.RawMessage
			if p.Then.Kind == dtnproto.KindUnload {
				ul := make([]dtnproto.UnloadLetter, len(full))
				for i := range full {
					ul[i] = dtnproto.UnloadLetter{Letter: full[i], Place: letters[i].place}
				}
				payload = mustJSON(dtnproto.UnloadPayload{T: t, Letters: ul})
			} else {
				payload = mustJSON(dtnproto.LettersPayload{T: t, Letters: full})
			}
			if _, perr := dtnproto.DecodePayload(p.Then.Kind, payload, req.Actor, h.lim); perr != nil {
				return "", nil, perr
			}
			return p.Then.Kind, payload, nil
		},
		effects: func(tx Tx, _ *Meta) error { return h.finishTransfer(tx, p.TransferID, dtnproto.TransferActivated) },
	}
}

// gcPlan は transfer.gc の thenPlan。then（port.remove・relay.remove・cargo.remove）をそのまま Container に送り、
// 確定で transfers を collected にする（bundles は then の効果で gone・moved になり、bytes を消す）。
func (h *Host) gcPlan(req dtnproto.Request) *thenPlan {
	var p dtnproto.TransferThenPayload
	_ = json.Unmarshal(req.Payload, &p) // Validate で確かめ済み
	return &thenPlan{
		build: func(tx Tx) (dtnproto.Kind, json.RawMessage, *dtnproto.Error) {
			row, rids, perr := h.transferFor(tx, p.TransferID, RoleSource, dtnproto.TransferPrepared)
			if perr != nil {
				return "", nil, perr
			}
			if len(row.Ack) == 0 {
				return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s has no ack yet", row.TransferID)
			}
			var lp dtnproto.LetterIDsPayload
			if err := json.Unmarshal(p.Then.Payload, &lp); err != nil {
				return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "then: %v", err)
			}
			if !sameSet(lp.LetterIDs, rids) {
				return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "then: letters %v are not the letters %v of transfer %s", lp.LetterIDs, rids, row.TransferID)
			}
			want := map[dtnproto.Kind]string{dtnproto.KindPortRemove: PlacePort, dtnproto.KindRelayRemove: PlaceRelay, dtnproto.KindCargoRemove: PlaceCargo}[p.Then.Kind]
			for _, id := range lp.LetterIDs {
				b, ok, err := tx.Bundle(uint64(id))
				switch {
				case err != nil:
					return "", nil, dtnproto.Errorf(dtnproto.CodeInternal, "read bundle: %v", err)
				case !ok || b.State != BundlePrepared || b.TransferID != row.TransferID:
					return "", nil, dtnproto.Errorf(dtnproto.CodeInternal, "letter %d: the prepared row of %s is missing", id, row.TransferID)
				case b.Place != want:
					return "", nil, dtnproto.Errorf(dtnproto.CodeInvalid, "then %s: letter %d is in %s", p.Then.Kind, id, b.Place)
				}
			}
			return p.Then.Kind, p.Then.Payload, nil
		},
		effects: func(tx Tx, _ *Meta) error { return h.finishTransfer(tx, p.TransferID, dtnproto.TransferCollected) },
	}
}

// transferFor は transfer id の行を読み、role と state が want であることを確かめる。
func (h *Host) transferFor(tx Tx, id, role, state string) (TransferRow, []ferry.LetterID, *dtnproto.Error) {
	row, ok, err := tx.Transfer(id)
	switch {
	case err != nil:
		return row, nil, dtnproto.Errorf(dtnproto.CodeInternal, "read transfer: %v", err)
	case !ok:
		return row, nil, dtnproto.Errorf(dtnproto.CodeInvalid, "unknown transfer %s", id)
	case row.Role != role:
		return row, nil, dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s: %s is the %s, not the %s", id, h.meta.Actor, row.Role, role)
	case row.State != state:
		// 別の eventSeq で同じ transfer をもう一度 activate・gc しようとした（宛先で 2 回適用しない。D-4）など
		return row, nil, dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s is %s, not %s", id, row.State, state)
	}
	ids, _, err := rowLetters(row)
	if err != nil {
		return row, nil, dtnproto.Errorf(dtnproto.CodeInternal, "%v", err)
	}
	return row, ids, nil
}

func (h *Host) finishTransfer(tx Tx, id, state string) error {
	row, ok, err := tx.Transfer(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("transfer %s disappeared", id)
	}
	row.State = state
	return tx.PutTransfer(row)
}

// --- transfer.abort・transfer.status ---

// transferAbort は committed の前の transfer を取り消す。送り元は prepared → held、受け取りは pending の行を消す。
// 行が無ければ aborted の墓石の行（role・peer・contact は空）を作る（遅れて届いた accept で pending を作らない）。
// activated・collected は断る（invalid）。2 回目は何もしないで成功する。
func (h *Host) transferAbort(req dtnproto.Request) ([]byte, error) {
	var p dtnproto.TransferIDPayload
	_ = json.Unmarshal(req.Payload, &p) // Validate で確かめ済み
	var refuse *dtnproto.Error
	if err := h.update(func(tx Tx, m *Meta) error {
		row, ok, err := tx.Transfer(p.TransferID)
		if err != nil {
			return err
		}
		if !ok {
			return tx.PutTransfer(TransferRow{TransferID: p.TransferID, LetterIDs: json.RawMessage("[]"),
				Digests: json.RawMessage("[]"), State: dtnproto.TransferAborted})
		}
		switch row.State {
		case dtnproto.TransferAborted:
			return nil
		case dtnproto.TransferActivated, dtnproto.TransferCollected:
			refuse = dtnproto.Errorf(dtnproto.CodeInvalid, "transfer %s is %s; it cannot be aborted after the commit", p.TransferID, row.State)
			return nil
		}
		ids, _, err := rowLetters(row)
		if err != nil {
			return err
		}
		for _, id := range ids {
			b, ok, err := tx.Bundle(uint64(id))
			if err != nil {
				return err
			}
			if !ok || b.TransferID != p.TransferID {
				continue
			}
			switch b.State {
			case BundlePrepared:
				b.State, b.TransferID, b.UpdatedSeq = BundleHeld, "", req.EventSeq
				if err := tx.PutBundle(b); err != nil {
					return err
				}
			case BundlePending:
				m.StoredBytes -= int64(len(b.Bundle))
				if err := tx.DeleteBundle(uint64(id)); err != nil {
					return err
				}
			}
		}
		row.State = dtnproto.TransferAborted
		return tx.PutTransfer(row)
	}); err != nil {
		return h.fail(req, asProtoError(err, dtnproto.CodeInternal)), nil
	}
	if refuse != nil {
		return h.fail(req, refuse), nil
	}
	if err := h.at(PointAfterCommit); err != nil {
		return nil, err
	}
	return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{
		transferStatusOutput(dtnproto.TransferStatus{TransferID: p.TransferID, State: dtnproto.TransferAborted})})), nil
}

// transferStatus は {transferId, state, ack?} を返す（journal に入れない。状態を変えない）。
func (h *Host) transferStatus(req dtnproto.Request) []byte {
	var p dtnproto.TransferIDPayload
	_ = json.Unmarshal(req.Payload, &p) // Validate で確かめ済み
	st := dtnproto.TransferStatus{TransferID: p.TransferID, State: dtnproto.TransferNone}
	if err := h.store.View(func(tx Tx) error {
		row, ok, err := tx.Transfer(p.TransferID)
		if err != nil || !ok {
			return err
		}
		st.State = row.State
		st.Ack, err = rowAck(row)
		return err
	}); err != nil {
		return h.fail(req, dtnproto.Errorf(dtnproto.CodeInternal, "read transfer: %v", err))
	}
	return encode(okResponse(req, h.meta.Epoch, []dtnproto.Output{transferStatusOutput(st)}))
}
