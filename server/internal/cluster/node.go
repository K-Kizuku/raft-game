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

// StateMachine は、このノードの状態機械（台帳の複製、§5.6・§7）。ノードごとに別のものを渡す。
// Commit 済みの通常のエントリのうち Data が空でないものを、index の昇順に 1 回ずつ受け取る。
// data は Applied.Data と同じ複製なので、書き換えない。決定的でなければならない（同じ順序で同じ入力 → 同じ結果）。
// T-106 は *ledger.Ledger を次のように包んで渡す（Go では戻り値の型が違うと満たせないため）:
//
//	type ledgerSM struct{ l *ledger.Ledger }
//	func (s ledgerSM) Apply(i uint64, d []byte) any { return s.l.Apply(i, d) }
type StateMachine interface {
	Apply(index uint64, data []byte) any
}

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
	StateMachine    StateMachine    // 必須。nil はエラー
}

// Applied は、このノードで適用した通常のエントリ 1 つ。
type Applied struct {
	Index  uint64
	Term   uint64
	Data   []byte // エントリの Data の複製（ledger.applied の reqId と I-3 の比較に使う）
	Result any    // StateMachine.Apply の戻り値（T-106 で ledger.Result に型アサーションする）
}

// RoleChange は、ノードの役割か、そのノードが知っている Leader が変わったこと（Ready.SoftState の変化）。
// 時刻を持たない。sim.Engine が、Process を呼んだ時点の Now() を付けてドメインイベントにする。
// 1 つの Ready の間に役割が 2 回変わったときは、Ready に入る最後の状態だけが見える（raft の仕様）。
type RoleChange struct {
	Node     uint64
	Term     uint64 // この変化を観察した Ready を処理した後の Term（保存した HardState の Term）
	PrevRole raft.StateType
	Role     raft.StateType
	PrevLead uint64 // 0 = Leader を知らない
	Lead     uint64
}

// Output は、1 回の Process（Ready がなくなるまで処理したもの）の結果。
type Output struct {
	Messages []*pb.Message // 送る手紙（proto.Clone した複製）。Raft が出した順
	Applied  []Applied     // 適用した通常のエントリ（index の昇順）
	Events   []RoleChange  // SoftState の変化（起きた順）
}

// Status は、Process の直後に読むノードの状態（node.state・Snapshot の元。docs/06-protocol.md §5.1）。
type Status struct {
	ID        uint64
	Role      raft.StateType
	Term      uint64
	Vote      uint64
	Lead      uint64
	Commit    uint64
	Applied   uint64 // 最後に適用したエントリの index（構成変更と空のエントリを含む）
	LastIndex uint64 // Storage の最後のエントリの index
}

// nodeStorage は Node が使う Storage。テストで記録用の包みに差し替えるためのインターフェース。
type nodeStorage interface {
	raft.Storage
	Append(entries []*pb.Entry) error
	SetHardState(st *pb.HardState) error
}

// readyStep は、V-5 のテストで記録する Ready の処理の段階。
type readyStep uint8

const (
	stepAppend       readyStep = iota + 1 // 記録用の Storage の包みが記録する
	stepSetHardState                      // 同上
	stepSend                              // processReady が手紙 1 通ごとに onStep で記録する
	stepApply                             // processReady が適用したエントリ 1 つごとに記録する
	stepAdvance                           // processReady が Advance の直後に記録する
)

// Node は 1 つの RawNode とその Storage を持つ。1 つの goroutine からだけ使う（ADR-0003）。
type Node struct {
	id      uint64
	rn      *raft.RawNode
	storage nodeStorage
	sm      StateMachine
	applied uint64         // 最後に適用したエントリの index（Bootstrap の構成変更と空のエントリを含む）
	term    uint64         // 最後に保存した HardState の Term
	role    raft.StateType // 最後に観察した SoftState の役割
	lead    uint64         // 最後に観察した SoftState の Leader（0 = 知らない）

	onStep func(readyStep) // テスト専用。nil なら何もしない
}

// errUnexpectedSnapshot は、コンパクションをしないので来ないはずのスナップショットが Ready に入っていたことを表す。
var errUnexpectedSnapshot = errors.New("cluster: unexpected snapshot in Ready")

// NewNode は cfg から Node を作り、Peers で Bootstrap する。
func NewNode(cfg NodeConfig) (*Node, error) {
	return newNode(cfg, raft.NewMemoryStorage())
}

// newNode は NewNode の本体。storage を raft.Config.Storage と n.storage の両方に使う。
func newNode(cfg NodeConfig, storage nodeStorage) (*Node, error) {
	if cfg.RandIntn == nil {
		return nil, fmt.Errorf("cluster: node %d: RandIntn is required", cfg.ID)
	}
	if cfg.StateMachine == nil {
		return nil, fmt.Errorf("cluster: node %d: StateMachine is required", cfg.ID)
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

	return &Node{
		id:      cfg.ID,
		rn:      rn,
		storage: storage,
		sm:      cfg.StateMachine,
		role:    raft.StateFollower,
	}, nil
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

// Tick は Raft の論理時計を 1 つ進める（§4.2 の RaftTick）。呼んだ後に Process を呼ぶのは呼び出し側。
// Opening の前に呼ばない（呼んでも壊れないが、開会式の手順が変わる）。
func (n *Node) Tick() { n.rn.Tick() }

// Process は Ready がなくなるまで処理し、その結果を返す（§5.4）。processReady を呼ぶだけの公開の入口。
func (n *Node) Process() (Output, error) { return n.processReady() }

// Status はノードの状態を返す。Process の直後（未処理の Ready がないとき）に読む。
// 未処理の Ready があると、Term などと Applied・LastIndex がずれ得る。
func (n *Node) Status() Status {
	st := n.rn.BasicStatus()
	// MemoryStorage の LastIndex はエラーを返さない（コンパクションをしないので ErrCompacted も起きない）。
	last, _ := n.storage.LastIndex()
	return Status{
		ID:        n.id,
		Role:      st.RaftState,
		Term:      st.HardState.GetTerm(),
		Vote:      st.HardState.GetVote(),
		Lead:      st.Lead,
		Commit:    st.HardState.GetCommit(),
		Applied:   n.applied,
		LastIndex: last,
	}
}

func (n *Node) step(s readyStep) {
	if n.onStep != nil {
		n.onStep(s)
	}
}

// processReady は Ready がなくなるまで処理し、送る手紙・適用した通常のエントリ・役割の変化を返す（§5.4）。
// 順序は「追記 → HardState の保存 → 送信 → 適用 → Advance」で固定。
// Ready を扱ってよいのはこの関数だけ。エラーのときは Output{}, err を返す（途中までの結果は捨てる）。
// StateMachine.Apply の panic は回復しない。
// 1 つの Ready の間に役割が 2 回変わったときは、Ready に入る最後の状態だけが見える（raft の仕様）。
func (n *Node) processReady() (Output, error) {
	var out Output
	for n.rn.HasReady() {
		rd := n.rn.Ready()

		if rd.Snapshot != nil && !raft.IsEmptySnap(rd.Snapshot) {
			return Output{}, errUnexpectedSnapshot
		}

		// 1. 追記
		if err := n.storage.Append(rd.Entries); err != nil {
			return Output{}, fmt.Errorf("cluster: node %d: append: %w", n.id, err)
		}

		// 2. HardState の保存
		if rd.HardState != nil && !raft.IsEmptyHardState(rd.HardState) {
			if err := n.storage.SetHardState(rd.HardState); err != nil {
				return Output{}, fmt.Errorf("cluster: node %d: set hard state: %w", n.id, err)
			}
			n.term = rd.HardState.GetTerm()
		}

		// 3. 送信（Raft のバッファを共有しないよう複製する）
		for _, m := range rd.Messages {
			out.Messages = append(out.Messages, proto.Clone(m).(*pb.Message))
			n.step(stepSend)
		}

		// 4. 適用
		for _, ent := range rd.CommittedEntries {
			n.step(stepApply)
			if ent.GetIndex() != n.applied+1 {
				return Output{}, fmt.Errorf("cluster: node %d: apply index %d, want %d", n.id, ent.GetIndex(), n.applied+1)
			}
			switch ent.GetType() {
			case pb.EntryNormal:
				if len(ent.GetData()) > 0 {
					d := bytes.Clone(ent.GetData())
					res := n.sm.Apply(ent.GetIndex(), d)
					out.Applied = append(out.Applied, Applied{
						Index:  ent.GetIndex(),
						Term:   ent.GetTerm(),
						Data:   d,
						Result: res,
					})
				}
			case pb.EntryConfChange:
				var cc pb.ConfChange
				if err := proto.Unmarshal(ent.GetData(), &cc); err != nil {
					return Output{}, fmt.Errorf("cluster: node %d: decode conf change at %d: %w", n.id, ent.GetIndex(), err)
				}
				n.rn.ApplyConfChange(&cc)
			case pb.EntryConfChangeV2:
				var cc pb.ConfChangeV2
				if err := proto.Unmarshal(ent.GetData(), &cc); err != nil {
					return Output{}, fmt.Errorf("cluster: node %d: decode conf change v2 at %d: %w", n.id, ent.GetIndex(), err)
				}
				n.rn.ApplyConfChange(&cc)
			default:
				return Output{}, fmt.Errorf("cluster: node %d: unknown entry type %v at %d", n.id, ent.GetType(), ent.GetIndex())
			}
			n.applied = ent.GetIndex()
		}

		// 5. Advance
		n.rn.Advance(rd)
		n.step(stepAdvance)

		// SoftState の変化をドメインイベントにする（§5.4 の emitRoleChange）
		if ss := rd.SoftState; ss != nil && (ss.RaftState != n.role || ss.Lead != n.lead) {
			out.Events = append(out.Events, RoleChange{
				Node:     n.id,
				Term:     n.term,
				PrevRole: n.role,
				Role:     ss.RaftState,
				PrevLead: n.lead,
				Lead:     ss.Lead,
			})
			n.role, n.lead = ss.RaftState, ss.Lead
		}
	}
	return out, nil
}
