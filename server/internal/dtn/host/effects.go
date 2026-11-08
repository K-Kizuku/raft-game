package host

import (
	"encoding/json"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// 出力の type のうち、Host が bundles に効果を持つもの（actor-protocol.md §4）。
const (
	OutputLetterCreated   = "letter.created"
	OutputLetterDropped   = "letter.dropped"
	OutputLetterDelivered = "letter.delivered"
	OutputBundleExported  = "bundle.exported"
)

// letterData は出力の data のうち Host が読むところ。
type letterData struct {
	LetterID uint64 `json:"letterId"`
	Digest   string `json:"digest"`
	Size     *int64 `json:"size"`
}

// applyEffects は、ok の応答を確定するときの bundles の効果（persistence-and-recovery.md §7.2 の表）。
// 要求の効果を先に、出力の効果を出力の順に行う。Actor の出力の Bundle が不正なら invalid。
func (h *Host) applyEffects(tx Tx, m *Meta, req dtnproto.Request, resp dtnproto.Response) error {
	seq := req.EventSeq
	switch req.Kind {
	case dtnproto.KindCargoAdd, dtnproto.KindDeliver:
		pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, h.lim)
		if perr != nil {
			return perr
		}
		state, place := BundleHeld, PlaceCargo
		if req.Kind == dtnproto.KindDeliver {
			state, place = BundleGone, PlaceDelivered
		}
		for _, l := range pv.(*dtnproto.LettersPayload).Letters {
			if err := putBundle(tx, m, uint64(l.LetterID), l.Bundle, l.Digest, state, place, seq); err != nil {
				return err
			}
		}
	case dtnproto.KindUnload:
		pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, h.lim)
		if perr != nil {
			return perr
		}
		for _, l := range pv.(*dtnproto.UnloadPayload).Letters {
			if err := putBundle(tx, m, uint64(l.LetterID), l.Bundle, l.Digest, BundleHeld, l.Place, seq); err != nil {
				return err
			}
		}
	case dtnproto.KindPortRemove, dtnproto.KindRelayRemove, dtnproto.KindCargoRemove:
		// 所有権が移った（transfer.gc の then）。collected の後なので bytes を消し、行は tombstone として残す
		pv, perr := dtnproto.DecodePayload(req.Kind, req.Payload, req.Actor, h.lim)
		if perr != nil {
			return perr
		}
		for _, id := range pv.(*dtnproto.LetterIDsPayload).LetterIDs {
			if err := markBundle(tx, m, uint64(id), BundleGone, PlaceMoved, seq, true); err != nil {
				return err
			}
		}
	}

	for i, o := range resp.Outputs {
		switch o.Type {
		case OutputLetterCreated:
			var d letterData
			if err := json.Unmarshal(o.Data, &d); err != nil || d.LetterID == 0 {
				return dtnproto.Errorf(dtnproto.CodeInvalid, "outputs[%d]: letter.created needs data.letterId", i)
			}
			switch {
			case len(o.Bundle) == 0:
				return dtnproto.Errorf(dtnproto.CodeInvalid, "outputs[%d]: letter.created without bundle", i)
			case len(o.Bundle) > h.lim.MaxBundleBytes:
				return dtnproto.Errorf(dtnproto.CodeInvalid, "outputs[%d]: bundle of %d bytes exceeds %d", i, len(o.Bundle), h.lim.MaxBundleBytes)
			case dtnproto.Digest(o.Bundle) != d.Digest:
				return dtnproto.Errorf(dtnproto.CodeInvalid, "outputs[%d]: data.digest does not match the bundle", i)
			case d.Size != nil && *d.Size != int64(len(o.Bundle)):
				return dtnproto.Errorf(dtnproto.CodeInvalid, "outputs[%d]: data.size %d is not %d", i, *d.Size, len(o.Bundle))
			}
			if err := putBundle(tx, m, d.LetterID, o.Bundle, d.Digest, BundleHeld, PlacePort, seq); err != nil {
				return err
			}
		case OutputLetterDropped, OutputLetterDelivered:
			var d letterData
			if err := json.Unmarshal(o.Data, &d); err != nil || d.LetterID == 0 {
				continue // 手紙の ID の無い出力は表示用。bundles には効かない
			}
			place := PlaceDropped
			if o.Type == OutputLetterDelivered {
				place = PlaceDelivered
			}
			if err := markBundle(tx, m, d.LetterID, BundleGone, place, seq, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// putBundle は bundles の行を書く（同じ手紙の行は新しい bytes で上書きする）。保存の合計を差分で数える。
func putBundle(tx Tx, m *Meta, id uint64, b []byte, digest, state, place string, seq uint64) error {
	old, ok, err := tx.Bundle(id)
	if err != nil {
		return err
	}
	if ok {
		m.StoredBytes -= int64(len(old.Bundle))
	}
	m.StoredBytes += int64(len(b))
	return tx.PutBundle(BundleRow{LetterID: id, Bundle: append([]byte(nil), b...), Digest: digest, Size: int64(len(b)),
		State: state, Place: place, UpdatedSeq: seq})
}

// markBundle は、行があれば state・place を変える（drop なら bytes を NULL にする）。行が無ければ何もしない。
func markBundle(tx Tx, m *Meta, id uint64, state, place string, seq uint64, drop bool) error {
	r, ok, err := tx.Bundle(id)
	if err != nil || !ok {
		return err
	}
	if drop {
		m.StoredBytes -= int64(len(r.Bundle))
		r.Bundle = nil
	}
	r.State, r.Place, r.UpdatedSeq = state, place, seq
	return tx.PutBundle(r)
}
