package cluster

import (
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/tracker"
)

// Progress は、Leader から見た 1 つのノードの複製の進み具合の複製（エンジニアモード用。DesignDoc §5.6）。
type Progress struct {
	ID           uint64
	Match        uint64
	Next         uint64
	State        tracker.StateType // tracker.StateProbe / StateReplicate / StateSnapshot
	RecentActive bool
}

// Progress は、このノードが Leader のときだけ、構成の各ノード（自分を含む・Learner を除く）の Progress を
// ID の昇順に返す。Leader でなければ nil。Raft の状態とストレージを変えない（読むだけ）。
// Process の直後（未処理の Ready がないとき）に読む。
//
// RawNode.WithProgress は ID の昇順で呼ぶので、map を走査しない（RawNode.Status の Progress の map は使わない）。
func (n *Node) Progress() []Progress {
	if n.rn.BasicStatus().RaftState != raft.StateLeader {
		return nil
	}
	var out []Progress
	n.rn.WithProgress(func(id uint64, typ raft.ProgressType, pr tracker.Progress) {
		if typ != raft.ProgressTypePeer {
			return
		}
		out = append(out, Progress{
			ID:           id,
			Match:        pr.Match,
			Next:         pr.Next,
			State:        pr.State,
			RecentActive: pr.RecentActive,
		})
	})
	return out
}
