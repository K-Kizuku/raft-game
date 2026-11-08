package bpv7

import (
	"math"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// LetterCodec は、ゲームの手紙（ferry.Letter）と Bundle の bytes を変換する。
// DTN-02 の dtnproto.LetterCodec を、dtn/protocol を import せずに満たす
// （引数と戻り値は ferry と組み込みの型だけ）。
type LetterCodec struct {
	LifetimeMs uint64 // Primary Block の Lifetime。ステージの durationMs + 60,000（bpv7-profile.md §3）
}

// Encode は手紙を Bundle にする。
//   - Raft の手紙: ipn:<From>.1 → ipn:<To>.1、ペイロードは EncodeRaft(l.Raft)。command は空でなければならない
//   - 依頼の手紙: ipn:<From>.2 → ipn:<To>.2、ペイロードは EncodeRequest{l.Req.ReqID, l.Req.Hops, command}
//
// 作成時刻の連番は l.ID（letterId）の島の中の通し番号で、l.ID の島は l.From と同じでなければならない。
// l.CreatedAt・l.Location は Bundle に入れない。ageMs は Bundle Age（0 以上）。
func (c LetterCodec) Encode(l ferry.Letter, command []byte, ageMs int64) ([]byte, error) {
	if ageMs < 0 {
		return nil, errorf(CodeAge, "negative age %d", ageMs)
	}
	node, seq, err := SplitLetterID(uint64(l.ID))
	if err != nil {
		return nil, err
	}
	if node != l.From {
		return nil, errorf(CodeEID, "letter %d was not created by island %d", l.ID, l.From)
	}
	var service uint64
	var payload []byte
	switch l.Kind {
	case ferry.LetterRaft:
		if l.Raft == nil || l.Req != nil || len(command) != 0 {
			return nil, malformed("raft letter %d must carry only a raft message", l.ID)
		}
		service = ServiceRaft
		if payload, err = EncodeRaft(l.Raft); err != nil {
			return nil, err
		}
	case ferry.LetterRequest:
		if l.Req == nil || l.Raft != nil || l.Req.Hops < 0 {
			return nil, malformed("request letter %d must carry a request note with hops >= 0", l.ID)
		}
		service = ServiceRequest
		payload, err = EncodeRequest(RequestPayload{ReqID: l.Req.ReqID, Hops: uint64(l.Req.Hops), Command: command})
		if err != nil {
			return nil, err
		}
	default:
		return nil, malformed("letter %d: unknown kind %d", l.ID, l.Kind)
	}
	return Encode(Bundle{
		Dest:       IPN(l.To, service),
		Source:     IPN(l.From, service),
		ReportTo:   DTNNone,
		Seq:        seq,
		LifetimeMs: c.LifetimeMs,
		AgeMs:      uint64(ageMs),
		Payload:    payload,
	})
}

// Decode は Bundle を手紙に戻す。手紙は ID・Kind・From・To・Raft か Req だけを埋める
// （CreatedAt・Location は 0）。command は依頼の手紙の ledger.Command の JSON（Raft なら nil）、
// ageMs は Bundle Age。
func (c LetterCodec) Decode(b []byte) (l ferry.Letter, command []byte, ageMs int64, err error) {
	bd, err := Decode(b)
	if err != nil {
		return ferry.Letter{}, nil, 0, err
	}
	if bd.AgeMs > math.MaxInt64 {
		return ferry.Letter{}, nil, 0, errorf(CodeAge, "age %d does not fit in int64", bd.AgeMs)
	}
	id, err := bd.LetterID()
	if err != nil {
		return ferry.Letter{}, nil, 0, err
	}
	l = ferry.Letter{ID: ferry.LetterID(id), From: bd.Source.Node, To: bd.Dest.Node}
	switch bd.Source.Service {
	case ServiceRaft:
		l.Kind = ferry.LetterRaft
		if l.Raft, err = DecodeRaft(bd.Payload); err != nil {
			return ferry.Letter{}, nil, 0, err
		}
	case ServiceRequest:
		r, err := DecodeRequest(bd.Payload)
		if err != nil {
			return ferry.Letter{}, nil, 0, err
		}
		if r.Hops > math.MaxInt {
			return ferry.Letter{}, nil, 0, malformed("request hops %d too large", r.Hops)
		}
		l.Kind = ferry.LetterRequest
		l.Req = &ferry.RequestNote{ReqID: r.ReqID, Hops: int(r.Hops)}
		command = r.Command
	}
	return l, command, int64(bd.AgeMs), nil
}

// WithAge は Bundle の Bundle Age を ageMs（0 以上）に変え、CRC を計算し直した bytes を返す。
func (c LetterCodec) WithAge(b []byte, ageMs int64) ([]byte, error) {
	if ageMs < 0 {
		return nil, errorf(CodeAge, "negative age %d", ageMs)
	}
	return SetAge(b, uint64(ageMs))
}
