package sim

import (
	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// letterLabel は手紙のゲーム上の区分（プロトコル §5.2 の label）を返す。
// エントリのない MsgApp（Commit の通知・探り）を append_probe とするのは設計書にない判断。
func letterLabel(l ferry.Letter) string {
	if l.Kind == ferry.LetterRequest {
		return "request"
	}
	m := l.Raft
	switch m.GetType() {
	case pb.MsgHeartbeat:
		return "heartbeat"
	case pb.MsgHeartbeatResp:
		return "heartbeat_resp"
	case pb.MsgApp:
		if len(m.GetEntries()) > 0 {
			return "append"
		}
		return "append_probe"
	case pb.MsgAppResp:
		if m.GetReject() {
			return "append_reject"
		}
		return "append_resp"
	case pb.MsgPreVote:
		return "prevote"
	case pb.MsgPreVoteResp:
		return "prevote_resp"
	case pb.MsgVote:
		return "vote"
	case pb.MsgVoteResp:
		return "vote_resp"
	}
	return "other"
}
