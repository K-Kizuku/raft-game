package sim

import (
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

func TestLetterLabel(t *testing.T) { // L-1
	t.Parallel()
	raftLetter := func(m *pb.Message) ferry.Letter { return ferry.Letter{Kind: ferry.LetterRaft, Raft: m} }
	msg := func(typ pb.MessageType) *pb.Message { return &pb.Message{Type: typ.Enum()} }
	tests := []struct {
		name string
		l    ferry.Letter
		want string
	}{
		{"request", ferry.Letter{Kind: ferry.LetterRequest, Req: &ferry.RequestNote{ReqID: "r"}}, "request"},
		{"heartbeat", raftLetter(msg(pb.MsgHeartbeat)), "heartbeat"},
		{"heartbeat_resp", raftLetter(msg(pb.MsgHeartbeatResp)), "heartbeat_resp"},
		{"append", raftLetter(&pb.Message{Type: pb.MsgApp.Enum(), Entries: []*pb.Entry{{}}}), "append"},
		{"append_probe", raftLetter(msg(pb.MsgApp)), "append_probe"},
		{"append_resp", raftLetter(msg(pb.MsgAppResp)), "append_resp"},
		{"append_reject", raftLetter(&pb.Message{Type: pb.MsgAppResp.Enum(), Reject: ptr(true)}), "append_reject"},
		{"prevote", raftLetter(msg(pb.MsgPreVote)), "prevote"},
		{"prevote_resp", raftLetter(msg(pb.MsgPreVoteResp)), "prevote_resp"},
		{"vote", raftLetter(msg(pb.MsgVote)), "vote"},
		{"vote_resp", raftLetter(msg(pb.MsgVoteResp)), "vote_resp"},
		{"other", raftLetter(msg(pb.MsgTimeoutNow)), "other"},
	}
	for _, tt := range tests {
		if got := letterLabel(tt.l); got != tt.want {
			t.Errorf("%s: letterLabel = %q, want %q", tt.name, got, tt.want)
		}
	}
}
