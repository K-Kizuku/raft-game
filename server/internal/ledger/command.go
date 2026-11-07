package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version はコマンドの形式の版（§7.1 の "v"）。
const Version = 1

// コマンドの種類（§7.1 の "kind"）。
const (
	KindFireworkReserve = "firework.reserve" // 花火の予約（P0）
	KindStallReserve    = "stall.reserve"    // 屋台の出店枠（P0）
)

// Command は Raft のエントリの Data に入れるコマンド。フィールドの順序が JSON のキーの順序になる。
type Command struct {
	V        int    `json:"v"`
	ReqID    string `json:"reqId"`
	Origin   uint64 `json:"origin"` // 依頼元の島（= ノード）ID
	Kind     string `json:"kind"`
	Resource string `json:"resource"` // 資源の ID（例: "firework:a"）
	Amount   int64  `json:"amount"`   // budget.spend（P1）用。予約では使わない（0）
}

// ErrInvalidCommand は、コマンドを Encode・Decode できないときのエラー。
var ErrInvalidCommand = errors.New("ledger: invalid command")

// Encode はコマンドを JSON にする。キーの順序は Command のフィールドの順で固定し、
// amount は 0 でも出し、末尾に改行を付けない。V・ReqID・Origin・Kind が不正なときは
// ErrInvalidCommand を包んで返す。Kind が既知の種類かどうかは見ない（Apply で決める）。
func Encode(c Command) ([]byte, error) {
	switch {
	case c.V != Version:
		return nil, fmt.Errorf("%w: v must be %d, got %d", ErrInvalidCommand, Version, c.V)
	case c.ReqID == "":
		return nil, fmt.Errorf("%w: reqId is empty", ErrInvalidCommand)
	case c.Origin == 0:
		return nil, fmt.Errorf("%w: origin is 0", ErrInvalidCommand)
	case c.Kind == "":
		return nil, fmt.Errorf("%w: kind is empty", ErrInvalidCommand)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	return b, nil
}

// Decode は JSON のコマンドを 1 つ読む。JSON として不正・オブジェクトでない（null を含む）・
// 未知のキーがある・型が合わない・1 つ目の値のあとに空白以外が続くときは ErrInvalidCommand を
// 包んで返す。値の中身の検査はしない（Apply で行う）。
func Decode(data []byte) (Command, error) {
	// null は json.Decoder ではエラーにならないので、先に 1 つの値として取り出して確かめる。
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Command{}, fmt.Errorf("%w: not a JSON object", ErrInvalidCommand)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Command
	if err := dec.Decode(&c); err != nil {
		return Command{}, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Command{}, fmt.Errorf("%w: trailing data after the object", ErrInvalidCommand)
	}
	return c, nil
}
