package islandcore

import (
	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Class は同じ時刻の処理の順（sim.EventClass と同じ値）。島が使うものだけを置く。
type Class uint8

const (
	ClassStart        Class = 0 // 開会式（cmd.game.start の処理。時刻 0）
	ClassArrive       Class = 1 // 船の到着の配達（deliver）。同じ時刻の RaftTick より前
	ClassRaftTick     Class = 3 // Tick と待っている依頼の再試行
	ClassWorldEvent   Class = 4 // 漂流瓶（参照モデルだけ。分散版の対象外）
	ClassRequestSpawn Class = 5 // 依頼の発生
	ClassRequestRetry Class = 6 // 依頼の再送
	ClassCommand      Class = 8 // プレイヤーのコマンドが起こした出力（unload など）。上限ではその時刻のすべての後
)

// Mark は時刻の印 (T, Class)。辞書式の順で比べる（simulation-time.md §2.1）。
type Mark struct {
	T     int64
	Class Class
}

// Less は m < o を返す。
func (m Mark) Less(o Mark) bool {
	if m.T != o.T {
		return m.T < o.T
	}
	return m.Class < o.Class
}

// 出力の種類（actor-protocol.md §4 の type）。
const (
	OutLetterCreated   = "letter.created"
	OutLetterDropped   = "letter.dropped"
	OutLetterDelivered = "letter.delivered"
	OutNodeState       = "node.state"
	OutLeaderElected   = "node.leader_elected"
	OutSteppedDown     = "node.stepped_down"
	OutTermChanged     = "term.changed"
	OutLedgerApplied   = "ledger.applied"
	OutRequestRouted   = "request.routed"
)

// Output は島の出力 1 件。T・Class は出力を起こした入力（イベント・配達・コマンド）の時刻の印。
// Seq は島の中の出力の連番（1 から）。
type Output struct {
	T     int64
	Class Class
	Seq   uint64
	Type  string
	Data  any // 下の *Out 型のどれか（値）
}

// LetterCreatedOut は letter.created。Letter は港に置いた時点の手紙（中身を持つ。Raft は読み取り専用）。
// SenderTerm は送信元のノードの今の Term（表示の raft.stale に使う）、MsgTerm は Raft のメッセージ自身の Term
// （古い手紙の検出に使う。依頼書は 0）。Digest・Size は Bundle の値（DTN-06 の Codec ができるまでゼロ値）。
type LetterCreatedOut struct {
	Letter     ferry.Letter
	SenderTerm uint64
	MsgTerm    uint64
	Digest     string
	Size       int
}

// LetterDroppedOut は letter.dropped（港の規則 R1〜R6 による破棄）。
type LetterDroppedOut struct {
	LetterID ferry.LetterID
	Reason   ferry.DropReason
	Rule     string
}

// LetterDeliveredOut は letter.delivered。
type LetterDeliveredOut struct {
	LetterID ferry.LetterID
	Accepted bool
}

// NodeStateOut は node.state（最後に出した値から変わったときだけ出す）。
type NodeStateOut struct {
	Role                                         raft.StateType
	Term, Vote, Lead, Commit, Applied, LastIndex uint64
}

// LeaderElectedOut は node.leader_elected。
type LeaderElectedOut struct {
	Term uint64
}

// SteppedDownOut は node.stepped_down。Reason は "higher_term" か "check_quorum"（ゲーム側の推定）。
type SteppedDownOut struct {
	Term   uint64
	Reason string
}

// TermChangedOut は term.changed。ノードの Term が、最後に知らせた値より上がった（古い手紙の検出に使う）。
type TermChangedOut struct {
	Term uint64
}

// LedgerAppliedOut は ledger.applied（今のドメインイベントの中身のまま）。
type LedgerAppliedOut struct {
	Index  uint64
	ReqID  string
	Result string
	Reason string
}

// 依頼の窓口の判断（request.routed の action）。
const (
	ActionProposed  = "proposed"  // Leader として Propose した
	ActionForwarded = "forwarded" // 知っている Leader へ依頼書を書いた（To・LetterID）
	ActionWaiting   = "waiting"   // 代表が分からない・提案が落とされた（次の RaftTick でやり直す）
)

// RequestRoutedOut は request.routed。Req は依頼の計画（Config.Requests）の添字。
type RequestRoutedOut struct {
	Req      int
	ReqID    string
	Action   string
	To       uint64
	LetterID ferry.LetterID
}
