package protocol

import (
	"encoding/json"
	"strconv"
)

// Version はエンベロープの版（v）。
const Version = 1

// Request は要求のエンベロープ（actor-protocol.md §2.1）。
type Request struct {
	V          int             `json:"v"`
	SessionID  string          `json:"sessionId"`
	Actor      ActorRef        `json:"actor"`
	Epoch      uint64          `json:"epoch"`
	CoordEpoch uint64          `json:"coordEpoch"`
	EventSeq   uint64          `json:"eventSeq"`
	Kind       Kind            `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Replay     bool            `json:"replay,omitempty"` // 省略可。true は復元の再生
}

// Status は応答の status。
type Status string

// 応答の status の値。
const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// Response は応答のエンベロープ（actor-protocol.md §2.2）。
// outputs は空でも [] で、cursor・error は無ければ null で書き出す。
type Response struct {
	V        int      `json:"v"`
	Actor    ActorRef `json:"actor"`
	Epoch    uint64   `json:"epoch"`
	EventSeq uint64   `json:"eventSeq"`
	Status   Status   `json:"status"`
	Outputs  []Output `json:"outputs"`
	Cursor   *Cursor  `json:"cursor"`
	Error    *Error   `json:"error"`
}

// MarshalJSON は outputs が nil でも [] を書き出す。
func (r Response) MarshalJSON() ([]byte, error) {
	type plain Response
	if r.Outputs == nil {
		r.Outputs = []Output{}
	}
	return json.Marshal(plain(r))
}

// Output は Actor の出力 1 つ（actor-protocol.md §4）。(T, Class, Seq) は島の中の順序。
// Bundle は Actor → Host の応答にだけ入れる（base64・パディングあり。Host は他への応答から取り除く）。
type Output struct {
	T      int64           `json:"t"`
	Class  uint8           `json:"class"`
	Seq    uint64          `json:"seq"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	Bundle []byte          `json:"bundle,omitempty"`
}

// Mark は時刻の印 (t, c)（simulation-time.md §2.1）。JSON では [t, c]。辞書式の順で比べる。
type Mark struct {
	T int64
	C uint8
}

// MaxMarkClass は Mark の C の上限（8 = その時刻のすべての種類の後、コマンドの位置）。
const MaxMarkClass = 8

// Less は m < o（辞書式）。
func (m Mark) Less(o Mark) bool {
	return m.T < o.T || (m.T == o.T && m.C < o.C)
}

// MarshalJSON は [t, c] を書き出す。
func (m Mark) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 24)
	b = append(b, '[')
	b = strconv.AppendInt(b, m.T, 10)
	b = append(b, ',')
	b = strconv.AppendUint(b, uint64(m.C), 10)
	b = append(b, ']')
	return b, nil
}

// UnmarshalJSON は [t, c]（整数 2 つ、c は 0〜255）だけを受ける。
func (m *Mark) UnmarshalJSON(b []byte) error {
	jv, err := parseJSON(b)
	if err != nil {
		return err
	}
	if jv.kind != jArray || len(jv.arr) != 2 || jv.arr[0].kind != jNumber || jv.arr[1].kind != jNumber {
		return Errorf(CodeInvalid, "mark must be [t, c]")
	}
	c := jv.arr[1].num
	if c < 0 || c > 255 {
		return Errorf(CodeInvalid, "mark class out of range")
	}
	m.T, m.C = jv.arr[0].num, uint8(c)
	return nil
}

// Cursor は Actor の進み具合と状態の要約（actor-protocol.md §2.2）。
type Cursor struct {
	Horizon     Mark   `json:"horizon"`
	StateDigest string `json:"stateDigest"`
}
