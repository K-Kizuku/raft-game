package bpv7

import (
	"bytes"
	"encoding/hex"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

func TestRaftRoundTrip(t *testing.T) {
	msgs := []*pb.Message{
		{},
		heartbeatMsg,
		appMsg,
		{Type: pb.MsgAppResp.Enum(), To: u64(3), From: u64(1), Term: u64(2), Index: u64(4), Reject: proto.Bool(true), RejectHint: u64(3), LogTerm: u64(1)},
		{Type: pb.MsgVote.Enum(), To: u64(2), From: u64(1), Term: u64(9), LogTerm: u64(8), Index: u64(40)},
		{Type: pb.MsgVoteResp.Enum(), To: u64(1), From: u64(2), Term: u64(9), Reject: proto.Bool(false)},
		{Type: pb.MsgHeartbeatResp.Enum(), To: u64(1), From: u64(2), Term: u64(9), Context: []byte("ctx")},
		{Type: pb.MsgApp.Enum(), To: u64(2), From: u64(1), Term: u64(1), Entries: []*pb.Entry{
			{Type: pb.EntryNormal.Enum(), Term: u64(1), Index: u64(1)},
			{Type: pb.EntryConfChangeV2.Enum(), Term: u64(1), Index: u64(2), Data: []byte{0, 1, 2}},
			{},
		}},
		{Type: pb.MsgApp.Enum(), To: u64(0), From: u64(0), Term: u64(0)}, // 0 を明示した欄も残る
	}
	for i, m := range msgs {
		b, err := EncodeRaft(m)
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		got, err := DecodeRaft(b)
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		if !proto.Equal(got, m) {
			t.Fatalf("%d: round trip\n got %v\nwant %v", i, got, m)
		}
		again, _ := EncodeRaft(got)
		if !bytes.Equal(again, b) {
			t.Fatalf("%d: not deterministic: %x vs %x", i, again, b)
		}
	}
	// 手で組み立てた protobuf（testdata/README.md）と一致する。
	if got := hex.EncodeToString(mustRaft(t, heartbeatMsg)); got != "08081002180120034005" {
		t.Fatalf("heartbeat protobuf = %s", got)
	}
	if _, err := EncodeRaft(nil); codeOf(err) != CodeMalformed {
		t.Fatalf("EncodeRaft(nil) = %v", err)
	}
	if _, err := DecodeRaft([]byte{0x08}); codeOf(err) != CodeMalformed {
		t.Fatalf("DecodeRaft(truncated) = %v", err)
	}
}

func TestRequestRoundTrip(t *testing.T) {
	for _, r := range []RequestPayload{
		{ReqID: "r-1", Hops: 1, Command: testCommand},
		{ReqID: "", Hops: 0, Command: []byte{}},
		{ReqID: "依頼-あ", Hops: 1 << 40, Command: []byte("{}")},
	} {
		b, err := EncodeRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeRequest(b)
		if err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
		if got.ReqID != r.ReqID || got.Hops != r.Hops || !bytes.Equal(got.Command, r.Command) {
			t.Fatalf("round trip: got %+v, want %+v", got, r)
		}
	}
	// nil の command は空の bstr になる。
	b, _ := EncodeRequest(RequestPayload{ReqID: "a"})
	if got := hex.EncodeToString(b); got != "a3016161020003"+"40" {
		t.Fatalf("EncodeRequest(nil command) = %s", got)
	}
}

func TestDecodeRequestErrors(t *testing.T) {
	tests := map[string]string{
		"empty":            "",
		"not a map":        "83636161610040",
		"missing key 3":    "a201636161610200",
		"unknown key 4":    "a40163616161020003400400",
		"key 4 instead":    "a301636161610200" + "0440",
		"text key":         "a3616101020003406131" + "00",
		"reqId not text":   "a301410102000340",
		"hops negative":    "a30163616161022003" + "40",
		"hops not uint":    "a3016361616102f50340",
		"command not bstr": "a3016361616102000360",
		"duplicate key":    "a301636161610200010340",
		"unsorted keys":    "a30200016361616103" + "40",
		"not shortest":     "a301636161610218000340",
		"trailing bytes":   "a30163616161020003" + "4000",
		"indefinite map":   "bf0163616161020003" + "40ff",
		"invalid utf-8":    "a30161ff020003" + "40",
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := hex.DecodeString(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRequest(b); codeOf(err) != CodeMalformed {
				t.Fatalf("DecodeRequest(%s) = %v, want bpv7_malformed", h, err)
			}
		})
	}
}
