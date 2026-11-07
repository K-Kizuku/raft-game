// Package loadmodel は、スパイク（T-007a / #134）の「負荷の模型」。
//
// nodes 個の raft.RawNode を 1 つの goroutine で動かし、ゲーム内 500ms ごとに全ノードを Tick し、
// 手紙をすぐに届ける（フェリーの遅れは模さない＝本物より手紙が多い側に倒す）。
// 測定だけに使う。ゲームのエンジン（server/internal/sim）の代わりではない。
//
// Ready の処理の順序は CLAUDE.md のとおり「Append → SetHardState → 送信 → 適用 → Advance」。
// Raft のメッセージは書き換えず、proto.Clone で複製してから届ける。
package loadmodel

import (
	"fmt"
	"io"
	"log"
	"strconv"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// TickMs はゲーム内の 1 Tick の長さ（ミリ秒）。DesignDoc §3.2 の RaftTick。
const TickMs = 500

// Phase は Ready の処理の段階。記録用の差し込み口（OnPhase）に渡す。
type Phase int

// Ready の処理の段階。値の順に処理する。
const (
	PhaseAppend Phase = iota + 1
	PhaseHardState
	PhaseSend
	PhaseApply
	PhaseAdvance
)

func (p Phase) String() string {
	switch p {
	case PhaseAppend:
		return "append"
	case PhaseHardState:
		return "hardstate"
	case PhaseSend:
		return "send"
	case PhaseApply:
		return "apply"
	case PhaseAdvance:
		return "advance"
	}
	return "unknown"
}

// Cluster は負荷の模型のクラスタ。1 つの goroutine からだけ使う。
type Cluster struct {
	ids      []uint64
	nodes    map[uint64]*raft.RawNode
	storages map[uint64]*raft.MemoryStorage
	applied  map[uint64]int // ノードごとの、適用した（データのある）通常のエントリの数

	proposalsPerSec int
	carryMs         int64 // Tick に満たないゲーム内の時間
	proposalAcc     int64 // 提案の数の端数（proposalsPerSec × ミリ秒）
	seq             uint64

	leader        uint64
	leaderChanges int
	proposed      int

	// OnPhase は、Ready を 1 つ処理するたびに段階ごとに呼ばれる（テスト用。nil なら呼ばない）。
	OnPhase func(node uint64, p Phase)
}

// NewCluster は nodes 個のノードのクラスタを作る（ID は 1〜nodes）。
func NewCluster(nodes, proposalsPerSec int) (*Cluster, error) {
	if nodes < 1 {
		return nil, fmt.Errorf("loadmodel: nodes must be >= 1, got %d", nodes)
	}
	if proposalsPerSec < 0 {
		return nil, fmt.Errorf("loadmodel: proposalsPerSec must be >= 0, got %d", proposalsPerSec)
	}
	c := &Cluster{
		nodes:           make(map[uint64]*raft.RawNode, nodes),
		storages:        make(map[uint64]*raft.MemoryStorage, nodes),
		applied:         make(map[uint64]int, nodes),
		proposalsPerSec: proposalsPerSec,
	}
	for i := 1; i <= nodes; i++ {
		c.ids = append(c.ids, uint64(i))
	}
	logger := &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	for _, id := range c.ids {
		st := raft.NewMemoryStorage()
		// 構成変更のエントリを使わず、最初から全員を Voter にする（etcd-raft のテストと同じやり方）。
		if err := st.ApplySnapshot(&pb.Snapshot{
			Metadata: &pb.SnapshotMetadata{ConfState: &pb.ConfState{Voters: append([]uint64(nil), c.ids...)}},
		}); err != nil {
			return nil, fmt.Errorf("loadmodel: node %d: apply snapshot: %w", id, err)
		}
		rn, err := raft.NewRawNode(&raft.Config{
			ID:              id,
			ElectionTick:    40, // DesignDoc §3.2・ADR-0007
			HeartbeatTick:   2,
			Storage:         st,
			MaxSizePerMsg:   1 << 20,
			MaxInflightMsgs: 256,
			CheckQuorum:     true,
			PreVote:         true,
			Logger:          logger,
		})
		if err != nil {
			return nil, fmt.Errorf("loadmodel: node %d: new raw node: %w", id, err)
		}
		c.nodes[id] = rn
		c.storages[id] = st
	}
	return c, nil
}

// Advance はゲーム内の時間を ms ミリ秒進める。TickMs ごとに全ノードを Tick し、Leader に提案を出し、
// Ready がなくなるまで処理する。
func (c *Cluster) Advance(ms int64) error {
	c.carryMs += ms
	for c.carryMs >= TickMs {
		c.carryMs -= TickMs
		if err := c.tick(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cluster) tick() error {
	for _, id := range c.ids {
		c.nodes[id].Tick()
	}
	if err := c.pump(); err != nil {
		return err
	}
	c.proposalAcc += int64(c.proposalsPerSec) * TickMs
	for c.proposalAcc >= 1000 {
		c.proposalAcc -= 1000
		if c.leader == 0 {
			continue // Leader がいない間の提案は出さない（ゲームでも Leader がいないと受け付けない）
		}
		c.seq++
		data := strconv.AppendUint([]byte("cmd-"), c.seq, 10)
		if err := c.nodes[c.leader].Propose(data); err != nil {
			continue // Leader が替わった直後などは捨てる（数えない）
		}
		c.proposed++
	}
	return c.pump()
}

// pump は、どのノードにも Ready と届けていない手紙がなくなるまで処理する。
func (c *Cluster) pump() error {
	for {
		var outbox []*pb.Message
		for _, id := range c.ids {
			msgs, err := c.processReady(id)
			if err != nil {
				return err
			}
			outbox = append(outbox, msgs...)
		}
		for _, m := range outbox {
			rn, ok := c.nodes[m.GetTo()]
			if !ok {
				continue
			}
			// Step のエラー（古い Term の手紙など）は、ネットワークでの欠落と同じ扱いにする。
			_ = rn.Step(m)
		}
		c.observeLeader()
		if len(outbox) == 0 && !c.anyReady() {
			return nil
		}
	}
}

func (c *Cluster) anyReady() bool {
	for _, id := range c.ids {
		if c.nodes[id].HasReady() {
			return true
		}
	}
	return false
}

func (c *Cluster) phase(id uint64, p Phase) {
	if c.OnPhase != nil {
		c.OnPhase(id, p)
	}
}

// processReady は 1 つのノードの Ready がなくなるまで処理し、送る手紙を返す。
// 順序は「Append → SetHardState → 送信 → 適用 → Advance」（CLAUDE.md）。
func (c *Cluster) processReady(id uint64) ([]*pb.Message, error) {
	rn, st := c.nodes[id], c.storages[id]
	var out []*pb.Message
	for rn.HasReady() {
		rd := rn.Ready()
		if rd.Snapshot != nil && !raft.IsEmptySnap(rd.Snapshot) {
			return nil, fmt.Errorf("loadmodel: node %d: unexpected snapshot", id)
		}

		// 1. Append
		c.phase(id, PhaseAppend)
		if err := st.Append(rd.Entries); err != nil {
			return nil, fmt.Errorf("loadmodel: node %d: append: %w", id, err)
		}

		// 2. SetHardState
		if rd.HardState != nil && !raft.IsEmptyHardState(rd.HardState) {
			c.phase(id, PhaseHardState)
			if err := st.SetHardState(rd.HardState); err != nil {
				return nil, fmt.Errorf("loadmodel: node %d: set hard state: %w", id, err)
			}
		}

		// 3. 送信（Raft のバッファを共有しないよう複製する）
		c.phase(id, PhaseSend)
		for _, m := range rd.Messages {
			out = append(out, proto.Clone(m).(*pb.Message))
		}

		// 4. 適用（通常のエントリを数えるだけ。構成変更は使わない）
		c.phase(id, PhaseApply)
		for _, ent := range rd.CommittedEntries {
			if ent.GetType() == pb.EntryNormal && len(ent.GetData()) > 0 {
				c.applied[id]++
			}
		}

		// 5. Advance
		rn.Advance(rd)
		c.phase(id, PhaseAdvance)
	}
	return out, nil
}

func (c *Cluster) observeLeader() {
	var leader uint64
	for _, id := range c.ids {
		if c.nodes[id].BasicStatus().RaftState == raft.StateLeader {
			leader = id
			break
		}
	}
	if leader != 0 && leader != c.leader {
		c.leaderChanges++
	}
	c.leader = leader
}

// Leader は今の Leader の ID を返す（いなければ 0）。
func (c *Cluster) Leader() uint64 { return c.leader }

// LeaderChanges は、Leader が決まった・替わった回数を返す。
func (c *Cluster) LeaderChanges() int { return c.leaderChanges }

// Proposed は、Leader が受け付けた提案の数を返す。
func (c *Cluster) Proposed() int { return c.proposed }

// Committed は、ノードごとに適用した（データのある）通常のエントリの数の最大を返す。
func (c *Cluster) Committed() int {
	best := 0
	for _, id := range c.ids {
		best = max(best, c.applied[id])
	}
	return best
}
