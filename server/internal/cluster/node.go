package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// NodeConfig は 1 ノードを作るための設定。数値はすべて呼び出し側（将来はステージ定義、§5.2）から渡し、
// このパッケージに既定値を持たない。
type NodeConfig struct {
	ID              uint64
	Peers           []uint64 // 全ノードの ID（自分を含む）。Bootstrap に使う
	ElectionTick    int
	HeartbeatTick   int
	MaxSizePerMsg   uint64
	MaxInflightMsgs int
	RandIntn        func(n int) int // 必須（ADR-0005）。nil はエラー
	Logger          raft.Logger     // nil なら出力を捨てる Logger を使う
}

// Applied は、このノードで適用した通常のエントリ 1 つ。
type Applied struct {
	Index uint64
	Term  uint64
	Data  []byte // エントリの Data の複製。T-103/T-104 で台帳の結果（Result）に置き換える
}

// Node は 1 つの RawNode とその Storage を持つ。1 つの goroutine からだけ使う（ADR-0003）。
type Node struct {
	id      uint64
	rn      *raft.RawNode
	storage *raft.MemoryStorage
	applied uint64 // 最後に適用したエントリの index（Bootstrap の構成変更と空のエントリを含む）
}

// errUnexpectedSnapshot は、コンパクションをしないので来ないはずのスナップショットが Ready に入っていたことを表す。
var errUnexpectedSnapshot = errors.New("cluster: unexpected snapshot in Ready")

// NewNode は cfg から Node を作り、Peers で Bootstrap する。
func NewNode(cfg NodeConfig) (*Node, error) {
	if cfg.RandIntn == nil {
		return nil, fmt.Errorf("cluster: node %d: RandIntn is required", cfg.ID)
	}
	if len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("cluster: node %d: Peers is empty", cfg.ID)
	}
	sorted := slices.Clone(cfg.Peers)
	slices.Sort(sorted)
	// ID 0 は raft.None なので、Bootstrap の構成変更が無視されて panic する。先に弾く。
	if sorted[0] == raft.None {
		return nil, fmt.Errorf("cluster: node %d: Peers contains 0", cfg.ID)
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return nil, fmt.Errorf("cluster: node %d: duplicate peer %d", cfg.ID, sorted[i])
		}
	}
	if _, found := slices.BinarySearch(sorted, cfg.ID); !found {
		return nil, fmt.Errorf("cluster: node %d: Peers does not contain the node itself", cfg.ID)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	}

	storage := raft.NewMemoryStorage()
	rn, err := newRawNode(&raft.Config{
		ID:                        cfg.ID,
		ElectionTick:              cfg.ElectionTick,
		HeartbeatTick:             cfg.HeartbeatTick,
		Storage:                   storage,
		MaxSizePerMsg:             cfg.MaxSizePerMsg,
		MaxInflightMsgs:           cfg.MaxInflightMsgs,
		CheckQuorum:               true,  // ADR-0007
		PreVote:                   true,  // ADR-0007
		DisableProposalForwarding: true,  // ADR-0008
		AsyncStorageWrites:        false, // §5.2
		RandIntn:                  cfg.RandIntn,
		Logger:                    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("cluster: node %d: new raw node: %w", cfg.ID, err)
	}

	peers := make([]raft.Peer, len(sorted))
	for i, id := range sorted {
		peers[i] = raft.Peer{ID: id}
	}
	if err := bootstrap(rn, peers); err != nil {
		return nil, fmt.Errorf("cluster: node %d: bootstrap: %w", cfg.ID, err)
	}

	return &Node{id: cfg.ID, rn: rn, storage: storage}, nil
}

// newRawNode は raft.NewRawNode を呼ぶ。v3.7 の NewRawNode は Config の検査
// （ID が 0・ElectionTick <= HeartbeatTick・MaxInflightMsgs <= 0 など）に失敗すると
// エラーを返さず panic するので、その panic をエラーに変える。
func newRawNode(c *raft.Config) (rn *raft.RawNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			rn, err = nil, fmt.Errorf("invalid raft config: %v", r)
		}
	}()
	return raft.NewRawNode(c)
}

// bootstrap は rn.Bootstrap を呼ぶ。Bootstrap の中の構成変更の適用は、入力によっては
// （たとえば Voter が残らないとき）エラーを返さず panic するので、newRawNode と同じく
// その panic をエラーに変える。
func bootstrap(rn *raft.RawNode, peers []raft.Peer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bootstrap panicked: %v", r)
		}
	}()
	return rn.Bootstrap(peers)
}

// ID はこのノードの ID を返す。
func (n *Node) ID() uint64 { return n.id }

// Deliver は、このノード宛ての手紙を Raft に渡す（§5.5）。
// Step のエラー（raft.ErrStepLocalMsg・raft.ErrStepPeerNotFound など）は包まずに返す。
func (n *Node) Deliver(m *pb.Message) error {
	if m.GetTo() != n.id {
		return fmt.Errorf("cluster: node %d: message addressed to %d", n.id, m.GetTo())
	}
	return n.rn.Step(m)
}

// processReady は Ready がなくなるまで処理し、送る手紙と適用した通常のエントリを返す（§5.4）。
// 順序は「追記 → HardState の保存 → 送信 → 適用 → Advance」で固定。
// Ready を扱ってよいのはこの関数だけ。エラーのときは nil, nil, err を返す。
func (n *Node) processReady() (out []*pb.Message, applied []Applied, err error) {
	for n.rn.HasReady() {
		rd := n.rn.Ready()

		if rd.Snapshot != nil && !raft.IsEmptySnap(rd.Snapshot) {
			return nil, nil, errUnexpectedSnapshot
		}

		// 1. 追記
		if err := n.storage.Append(rd.Entries); err != nil {
			return nil, nil, fmt.Errorf("cluster: node %d: append: %w", n.id, err)
		}

		// 2. HardState の保存
		if rd.HardState != nil && !raft.IsEmptyHardState(rd.HardState) {
			if err := n.storage.SetHardState(rd.HardState); err != nil {
				return nil, nil, fmt.Errorf("cluster: node %d: set hard state: %w", n.id, err)
			}
		}

		// 3. 送信（Raft のバッファを共有しないよう複製する）
		for _, m := range rd.Messages {
			out = append(out, proto.Clone(m).(*pb.Message))
		}

		// 4. 適用
		for _, ent := range rd.CommittedEntries {
			if ent.GetIndex() != n.applied+1 {
				return nil, nil, fmt.Errorf("cluster: node %d: apply index %d, want %d", n.id, ent.GetIndex(), n.applied+1)
			}
			switch ent.GetType() {
			case pb.EntryNormal:
				if len(ent.GetData()) > 0 {
					applied = append(applied, Applied{
						Index: ent.GetIndex(),
						Term:  ent.GetTerm(),
						Data:  bytes.Clone(ent.GetData()),
					})
				}
			case pb.EntryConfChange:
				var cc pb.ConfChange
				if err := proto.Unmarshal(ent.GetData(), &cc); err != nil {
					return nil, nil, fmt.Errorf("cluster: node %d: decode conf change at %d: %w", n.id, ent.GetIndex(), err)
				}
				n.rn.ApplyConfChange(&cc)
			case pb.EntryConfChangeV2:
				var cc pb.ConfChangeV2
				if err := proto.Unmarshal(ent.GetData(), &cc); err != nil {
					return nil, nil, fmt.Errorf("cluster: node %d: decode conf change v2 at %d: %w", n.id, ent.GetIndex(), err)
				}
				n.rn.ApplyConfChange(&cc)
			default:
				return nil, nil, fmt.Errorf("cluster: node %d: unknown entry type %v at %d", n.id, ent.GetType(), ent.GetIndex())
			}
			n.applied = ent.GetIndex()
		}

		// 5. Advance
		n.rn.Advance(rd)
	}
	return out, applied, nil
}
