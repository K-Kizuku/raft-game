package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// LetterCodec は手紙と Bundle の bytes の変換（architecture.md §3・bpv7-profile.md）。
// 引数と戻り値は ferry と組み込みの型だけにし、DTN-06 の bpv7 が import なしで満たせるようにする。
//
//   - Encode: 手紙 l（ID・種類・送り元と宛先・作成時刻・Raft のメッセージか依頼書）と、依頼の手紙の台帳の
//     コマンドの bytes command（Raft の手紙なら nil）と Bundle Age ageMs から Bundle の bytes を作る。
//     l.Location は符号化しない
//   - Decode: Encode の逆。Location は空で返す
//   - WithAge: Bundle Age だけを ageMs に変えた bytes を返す（bundle.export が送り出すときに使う）
type LetterCodec interface {
	Encode(l ferry.Letter, command []byte, ageMs int64) ([]byte, error)
	Decode(b []byte) (l ferry.Letter, command []byte, ageMs int64, err error)
	WithAge(b []byte, ageMs int64) ([]byte, error)
}

// FakeCodec は試験用の LetterCodec。決定的な JSON に、Raft のメッセージを
// proto.MarshalOptions{Deterministic: true} の bytes（base64）で入れる。DTN-06 の bpv7 が入るまで、
// DTN-03・04 はこれを使う。BPv7 とは互換でない。
type FakeCodec struct{}

var _ LetterCodec = FakeCodec{}

// fakeBundle は FakeCodec の bytes の形。フィールドの順は固定（encoding/json は構造体の順に書く）。
type fakeBundle struct {
	Fake      int            `json:"fake"` // 常に 1（BPv7 の bytes との取り違えを見つけるため）
	ID        ferry.LetterID `json:"id"`
	Kind      uint8          `json:"kind"`
	From      uint64         `json:"from"`
	To        uint64         `json:"to"`
	CreatedAt int64          `json:"createdAt"`
	AgeMs     int64          `json:"ageMs"`
	Raft      []byte         `json:"raft"` // Raft の手紙だけ。依頼書は null
	ReqID     string         `json:"reqId,omitempty"`
	Hops      int            `json:"hops,omitempty"`
	Command   []byte         `json:"command,omitempty"`
}

var errFakeBundle = errors.New("fake codec: malformed bundle")

// Encode は l を FakeCodec の bytes にする。
func (FakeCodec) Encode(l ferry.Letter, command []byte, ageMs int64) ([]byte, error) {
	if ageMs < 0 {
		return nil, fmt.Errorf("fake codec: negative age %d", ageMs)
	}
	fb := fakeBundle{Fake: 1, ID: l.ID, Kind: uint8(l.Kind), From: l.From, To: l.To, CreatedAt: l.CreatedAt, AgeMs: ageMs, Command: command}
	switch l.Kind {
	case ferry.LetterRaft:
		if l.Raft == nil {
			return nil, errors.New("fake codec: raft letter without message")
		}
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(l.Raft)
		if err != nil {
			return nil, fmt.Errorf("fake codec: %w", err)
		}
		if b == nil {
			b = []byte{} // 中身が空のメッセージも "raft": "" と書く（依頼書の null と区別する）
		}
		fb.Raft = b
	case ferry.LetterRequest:
		if l.Req == nil || l.Req.ReqID == "" {
			return nil, errors.New("fake codec: request letter without note")
		}
		fb.ReqID, fb.Hops = l.Req.ReqID, l.Req.Hops
	default:
		return nil, fmt.Errorf("fake codec: unknown letter kind %d", l.Kind)
	}
	return json.Marshal(fb)
}

// Decode は FakeCodec の bytes を手紙に戻す。
func (FakeCodec) Decode(b []byte) (ferry.Letter, []byte, int64, error) {
	fb, err := decodeFake(b)
	if err != nil {
		return ferry.Letter{}, nil, 0, err
	}
	l := ferry.Letter{ID: fb.ID, Kind: ferry.LetterKind(fb.Kind), From: fb.From, To: fb.To, CreatedAt: fb.CreatedAt}
	switch l.Kind {
	case ferry.LetterRaft:
		m := new(pb.Message)
		if err := proto.Unmarshal(fb.Raft, m); err != nil {
			return ferry.Letter{}, nil, 0, fmt.Errorf("fake codec: %w", err)
		}
		l.Raft = m
	case ferry.LetterRequest:
		l.Req = &ferry.RequestNote{ReqID: fb.ReqID, Hops: fb.Hops}
	}
	return l, fb.Command, fb.AgeMs, nil
}

// WithAge は Bundle Age だけを変えた bytes を返す。
func (FakeCodec) WithAge(b []byte, ageMs int64) ([]byte, error) {
	if ageMs < 0 {
		return nil, fmt.Errorf("fake codec: negative age %d", ageMs)
	}
	fb, err := decodeFake(b)
	if err != nil {
		return nil, err
	}
	fb.AgeMs = ageMs
	return json.Marshal(fb)
}

func decodeFake(b []byte) (fakeBundle, error) {
	var fb fakeBundle
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fb); err != nil {
		return fakeBundle{}, fmt.Errorf("%w: %v", errFakeBundle, err)
	}
	if fb.Fake != 1 || fb.AgeMs < 0 {
		return fakeBundle{}, errFakeBundle
	}
	switch ferry.LetterKind(fb.Kind) {
	case ferry.LetterRaft:
		if fb.Raft == nil || fb.ReqID != "" {
			return fakeBundle{}, errFakeBundle
		}
	case ferry.LetterRequest:
		if fb.ReqID == "" || fb.Raft != nil {
			return fakeBundle{}, errFakeBundle
		}
	default:
		return fakeBundle{}, errFakeBundle
	}
	return fb, nil
}
