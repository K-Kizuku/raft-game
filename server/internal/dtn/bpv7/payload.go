package bpv7

import (
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// EncodeRaft は Raft の手紙（サービス .1）のペイロード: raftpb.Message の protobuf
// （proto.MarshalOptions{Deterministic: true}）。中身は書き換えない（bpv7-profile.md §4）。
func EncodeRaft(m *pb.Message) ([]byte, error) {
	if m == nil {
		return nil, malformed("raft: nil message")
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return nil, malformed("raft: %v", err)
	}
	return b, nil
}

// DecodeRaft は EncodeRaft の逆。読めなければ bpv7_malformed。
func DecodeRaft(b []byte) (*pb.Message, error) {
	m := &pb.Message{}
	if err := proto.Unmarshal(b, m); err != nil {
		return nil, malformed("raft: %v", err)
	}
	return m, nil
}

// RequestPayload は依頼の手紙（サービス .2）のペイロード。
type RequestPayload struct {
	ReqID   string
	Hops    uint64 // 転送された回数
	Command []byte // ledger.Command の JSON の bytes
}

// 依頼のペイロードの CBOR の map のキー（bpv7-profile.md §4）。
const (
	reqKeyReqID   uint64 = 1
	reqKeyHops    uint64 = 2
	reqKeyCommand uint64 = 3
)

// EncodeRequest は依頼のペイロードを CBOR の map {1: reqId（text）, 2: hops（uint）,
// 3: command（bytes）} にする（Core Deterministic Encoding）。
func EncodeRequest(r RequestPayload) ([]byte, error) {
	cmd := r.Command
	if cmd == nil {
		cmd = []byte{}
	}
	b, err := marshal(map[uint64]any{reqKeyReqID: r.ReqID, reqKeyHops: r.Hops, reqKeyCommand: cmd})
	if err != nil {
		return nil, malformed("request: %v", err)
	}
	return b, nil
}

// DecodeRequest は EncodeRequest の逆。CBOR が壊れている・最短の符号化でない・
// キーの不足・未知のキー・型の違いは bpv7_malformed。
func DecodeRequest(b []byte) (RequestPayload, error) {
	if !canonical(b) {
		return RequestPayload{}, malformed("request: not a deterministically encoded CBOR item")
	}
	var m map[uint64]any
	if err := decMode.Unmarshal(b, &m); err != nil {
		return RequestPayload{}, malformed("request: %v", err)
	}
	if len(m) != 3 {
		return RequestPayload{}, malformed("request: %d keys, want 3", len(m))
	}
	reqID, ok1 := m[reqKeyReqID].(string)
	hops, ok2 := m[reqKeyHops].(uint64)
	cmd, ok3 := m[reqKeyCommand].([]byte)
	if !ok1 || !ok2 || !ok3 {
		return RequestPayload{}, malformed("request: keys must be 1 (text), 2 (uint) and 3 (bytes)")
	}
	return RequestPayload{ReqID: reqID, Hops: hops, Command: cmd}, nil
}
