package islandcore

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// NodeParams は全ノードを作って開会式を行うための値（ステージ定義と seed から決まる）。
type NodeParams struct {
	Islands         int // 島の数 N。ノードの ID は 1..N
	InitialLeader   uint64
	ElectionTick    int
	HeartbeatTick   int
	MaxSizePerMsg   uint64
	MaxInflightMsgs int
	Resources       []ledger.ResourceDef
	// RandIntn はノード id の選挙のタイムアウトの乱数（ADR-0005。sim.RaftRandIntn(seed, id)）。ノードごとに 1 回呼ぶ。
	RandIntn func(id uint64) func(n int) int
}

// OpeningAll は全ノードと台帳を作り、開会式（cluster.Opening）を行う（参照モデル。全ノードを持つ）。
// 添字は ID - 1。各ノードの乱数（raft/<id>）は開会式の中で進んだ状態のまま残る。
func OpeningAll(p NodeParams) ([]*cluster.Node, []*ledger.Ledger, cluster.OpeningResult, error) {
	n := p.Islands
	if n < 1 {
		return nil, nil, cluster.OpeningResult{}, fmt.Errorf("islandcore: islands %d < 1", n)
	}
	if p.RandIntn == nil {
		return nil, nil, cluster.OpeningResult{}, fmt.Errorf("islandcore: RandIntn is nil")
	}
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	ledgers := make([]*ledger.Ledger, n)
	for i, id := range ids {
		l, err := ledger.New(p.Resources)
		if err != nil {
			return nil, nil, cluster.OpeningResult{}, fmt.Errorf("islandcore: island %d: %w", id, err)
		}
		ledgers[i] = l
	}
	nodes := make([]*cluster.Node, n)
	for i, id := range ids {
		node, err := cluster.NewNode(cluster.NodeConfig{
			ID:              id,
			Peers:           ids,
			ElectionTick:    p.ElectionTick,
			HeartbeatTick:   p.HeartbeatTick,
			MaxSizePerMsg:   p.MaxSizePerMsg,
			MaxInflightMsgs: p.MaxInflightMsgs,
			RandIntn:        p.RandIntn(id),
			StateMachine:    ledgerSM{l: ledgers[i]},
		})
		if err != nil {
			return nil, nil, cluster.OpeningResult{}, fmt.Errorf("islandcore: %w", err)
		}
		nodes[i] = node
	}
	res, err := cluster.Opening(nodes, p.InitialLeader)
	if err != nil {
		return nil, nil, cluster.OpeningResult{}, fmt.Errorf("islandcore: %w", err)
	}
	return nodes, ledgers, res, nil
}

// Opening は、全ノードで開会式を計算して、island のノード・台帳・そのノードの役割の変化（起きた順）だけを残す
// （分散版の島の Actor と Coordinator。simulation-time.md §4）。ほかのノードは捨てる。開会式の電文は誰にも送らない。
func Opening(p NodeParams, island uint64) (*cluster.Node, *ledger.Ledger, []cluster.RoleChange, error) {
	if island < 1 || island > uint64(p.Islands) {
		return nil, nil, nil, fmt.Errorf("islandcore: island %d is not in 1..%d", island, p.Islands)
	}
	nodes, ledgers, res, err := OpeningAll(p)
	if err != nil {
		return nil, nil, nil, err
	}
	return nodes[island-1], ledgers[island-1], OwnRoleChanges(res, island), nil
}

// OwnRoleChanges は開会式の役割の変化のうち、island のノードのものを起きた順に返す。
func OwnRoleChanges(res cluster.OpeningResult, island uint64) []cluster.RoleChange {
	var out []cluster.RoleChange
	for _, rc := range res.Events {
		if rc.Node == island {
			out = append(out, rc)
		}
	}
	return out
}

// ledgerSM は *ledger.Ledger を cluster.StateMachine に合わせる包み（DesignDoc §5.6）。
type ledgerSM struct{ l *ledger.Ledger }

func (s ledgerSM) Apply(index uint64, data []byte) any { return s.l.Apply(index, data) }
