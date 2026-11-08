package cluster

// Propose は data をこのノードの Raft に提案する（§8.1 の route が使う）。呼んだ後に Process を呼ぶのは呼び出し側。
// DisableProposalForwarding（ADR-0008）なので、Leader でなければ raft.ErrProposalDropped を返す。
func (n *Node) Propose(data []byte) error { return n.rn.Propose(data) }
