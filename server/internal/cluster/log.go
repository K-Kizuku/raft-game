package cluster

import (
	"bytes"
	"fmt"
	"math"

	pb "go.etcd.io/raft/v3/raftpb"
)

// LogEntry は、ストレージにあるエントリ 1 つの複製（不変条件の検査・エンジニアモード用）。
type LogEntry struct {
	Index uint64
	Term  uint64
	Type  pb.EntryType
	Data  []byte // 複製。書き換えても Node に影響しない
}

// Log は、ストレージにあるエントリを index 1 から LastIndex まで、昇順に複製して返す。
// Raft の状態とストレージを変えない（読むだけ）。Process の直後（未処理の Ready がないとき）に読む。
// コンパクションをしないので、FirstIndex が 1 でなければエラーを返す。エントリがなければ長さ 0（エラーなし）。
func (n *Node) Log() ([]LogEntry, error) {
	first, err := n.storage.FirstIndex()
	if err != nil {
		return nil, fmt.Errorf("cluster: node %d: log: %w", n.id, err)
	}
	last, err := n.storage.LastIndex()
	if err != nil {
		return nil, fmt.Errorf("cluster: node %d: log: %w", n.id, err)
	}
	if first != 1 {
		return nil, fmt.Errorf("cluster: node %d: log: first index %d, want 1", n.id, first)
	}
	if last < first {
		return []LogEntry{}, nil
	}
	ents, err := n.storage.Entries(first, last+1, math.MaxUint64)
	if err != nil {
		return nil, fmt.Errorf("cluster: node %d: log: %w", n.id, err)
	}
	out := make([]LogEntry, len(ents))
	for i, ent := range ents {
		out[i] = LogEntry{
			Index: ent.GetIndex(),
			Term:  ent.GetTerm(),
			Type:  ent.GetType(),
			Data:  bytes.Clone(ent.GetData()),
		}
	}
	return out, nil
}
