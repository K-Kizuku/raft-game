package cluster

import (
	"errors"
	"fmt"
	"slices"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

// maxOpeningDeliveries は開会式で即時配達する手紙の上限（DesignDoc §5.3 の安全のための上限）。
// ゲームの数値ではないので、ステージ定義ではなくここに置く。
const maxOpeningDeliveries = 1000

var (
	errOpeningTooManyDeliveries = errors.New("too many deliveries")
	errOpeningNoLeader          = errors.New("no leader elected")
)

// OpeningResult は開会式（§5.3）の結果。
type OpeningResult struct {
	Leader    uint64        // 当選した Leader（initialLeader とは限らない。§5.3 の注）
	Term      uint64        // Leader の Term
	Delivered []*pb.Message // 即時配達した手紙（配達した順）。読み取り専用
	Events    []RoleChange  // 開会式の間の RoleChange（処理した順）
}

// Opening は開会式（§5.3）を行う。Bootstrap の構成変更を全ノードで適用し、initialLeader を立候補させ、
// 手紙がなくなるまで FIFO で即時配達してから、Leader が 1 人に決まったことを確かめる。
// 立候補させるだけで、当選は Raft の投票で決まる。時刻を受け取らない（時刻は sim.Engine が決める）。
// エラーのとき、入力の検査で弾いた場合を除き、ノードは途中の状態で残る。
func Opening(nodes []*Node, initialLeader uint64) (OpeningResult, error) {
	return opening(nodes, initialLeader, maxOpeningDeliveries)
}

func opening(nodes []*Node, initialLeader uint64, maxDeliveries int) (OpeningResult, error) {
	if len(nodes) == 0 {
		return OpeningResult{}, errors.New("cluster: opening: no nodes")
	}
	sorted := slices.Clone(nodes)
	for i, n := range sorted {
		if n == nil {
			return OpeningResult{}, fmt.Errorf("cluster: opening: nodes[%d] is nil", i)
		}
	}
	slices.SortFunc(sorted, func(a, b *Node) int {
		switch {
		case a.id < b.id:
			return -1
		case a.id > b.id:
			return 1
		}
		return 0
	})
	byID := make(map[uint64]*Node, len(sorted))
	for i, n := range sorted {
		if i > 0 && sorted[i-1].id == n.id {
			return OpeningResult{}, fmt.Errorf("cluster: opening: duplicate node %d", n.id)
		}
		if n.applied != 0 {
			return OpeningResult{}, fmt.Errorf("cluster: opening: node %d already processed (applied %d)", n.id, n.applied)
		}
		byID[n.id] = n
	}
	leader, ok := byID[initialLeader]
	if !ok {
		return OpeningResult{}, fmt.Errorf("cluster: opening: initial leader %d is not in nodes", initialLeader)
	}

	var res OpeningResult
	settle := func() error {
		var queue []*pb.Message
		for {
			for _, n := range sorted {
				out, err := n.processReady()
				if err != nil {
					return fmt.Errorf("cluster: opening: %w", err)
				}
				if len(out.Applied) > 0 {
					return fmt.Errorf("cluster: opening: node %d applied %d normal entries", n.id, len(out.Applied))
				}
				queue = append(queue, out.Messages...)
				res.Events = append(res.Events, out.Events...)
			}
			if len(queue) == 0 {
				return nil
			}
			m := queue[0]
			queue = queue[1:]
			dst, ok := byID[m.GetTo()]
			if !ok {
				return fmt.Errorf("cluster: opening: message %v from %d to unknown node %d", m.GetType(), m.GetFrom(), m.GetTo())
			}
			if len(res.Delivered) >= maxDeliveries {
				return fmt.Errorf("cluster: opening: more than %d messages: %w", maxDeliveries, errOpeningTooManyDeliveries)
			}
			if err := dst.Deliver(m); err != nil {
				return fmt.Errorf("cluster: opening: deliver %v to %d: %w", m.GetType(), m.GetTo(), err)
			}
			res.Delivered = append(res.Delivered, m)
		}
	}

	// 構成変更を適用する前の Campaign() は raft が無視するので、先に落ち着かせる。
	if err := settle(); err != nil {
		return OpeningResult{}, err
	}
	if err := leader.rn.Campaign(); err != nil {
		return OpeningResult{}, fmt.Errorf("cluster: opening: campaign %d: %w", initialLeader, err)
	}
	if err := settle(); err != nil {
		return OpeningResult{}, err
	}

	var elected *Node
	for _, n := range sorted {
		if n.rn.BasicStatus().RaftState != raft.StateLeader {
			continue
		}
		if elected != nil {
			return OpeningResult{}, fmt.Errorf("cluster: opening: leaders %d and %d: %w", elected.id, n.id, errOpeningNoLeader)
		}
		elected = n
	}
	if elected == nil {
		return OpeningResult{}, fmt.Errorf("cluster: opening: no node is leader: %w", errOpeningNoLeader)
	}
	ls := elected.Status()
	for _, n := range sorted {
		st := n.Status()
		if st.Lead != ls.ID {
			return OpeningResult{}, fmt.Errorf("cluster: opening: node %d lead = %d, want %d: %w", n.id, st.Lead, ls.ID, errOpeningNoLeader)
		}
		if st.Applied != ls.LastIndex {
			return OpeningResult{}, fmt.Errorf("cluster: opening: node %d applied = %d, want %d: %w", n.id, st.Applied, ls.LastIndex, errOpeningNoLeader)
		}
	}
	lastTerm, err := elected.storage.Term(ls.LastIndex)
	if err != nil {
		return OpeningResult{}, fmt.Errorf("cluster: opening: leader %d term of %d: %w", ls.ID, ls.LastIndex, err)
	}
	if lastTerm != ls.Term {
		return OpeningResult{}, fmt.Errorf("cluster: opening: leader %d last entry term = %d, want %d: %w", ls.ID, lastTerm, ls.Term, errOpeningNoLeader)
	}

	res.Leader = ls.ID
	res.Term = ls.Term
	return res, nil
}
