package sim

import (
	"fmt"
	"slices"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/tracker"
)

// nodeDetailLogLimit は NodeDetail の Log の件数の上限（プロトコル §5.4「末尾の 50 件」。プロトコルが決めた値なのでコードに置く）。
const nodeDetailLogLimit = 50

// NodeDetail はエンジニアモードのノードの詳細（プロトコル §5.4 の元）。
// JSON の形はプロトコルと違ってよい（simconv.NodeDetailFromSim が変換する）。トレースに入らない（イベントではない）。
type NodeDetail struct {
	ID        uint64
	Role      string // roleString と同じ値
	Term      uint64
	Vote      uint64
	Lead      uint64
	Commit    uint64
	Applied   uint64
	LastIndex uint64
	Log       []LogEntryDetail // 末尾の nodeDetailLogLimit 件。index の昇順。nil にしない
	Progress  []ProgressView   // Leader のときだけ。自分を除く Follower の ID の昇順。Leader でなければ nil
	Ledger    []ResourceView   // 台帳の資源（定義の順）。nil にしない
	// TicksSinceLeaderContact は、最後に Leader から手紙を受け取ってからの tick 数の推定値（DesignDoc §3.3）。
	// etcd-raft の electionElapsed は公開されていないので、ゲーム側の観察（contactAt）から計算する。Leader は 0。
	TicksSinceLeaderContact int
}

// LogEntryDetail は NodeDetail の Log の 1 要素。
type LogEntryDetail struct {
	Index     uint64
	Term      uint64
	Type      string // "normal" | "conf_change" | "conf_change_v2"（EntryView と同じ）
	Summary   string // EntryView と同じ規則: 通常のエントリは "<kind> <reqId>"、読めなければ "invalid"、空のエントリと構成変更は ""
	Committed bool   // Index <= Commit
}

// ProgressView は Leader から見た 1 つの Follower の複製の進み具合。
type ProgressView struct {
	ID           uint64
	Match        uint64
	Next         uint64
	State        string // "probe" | "replicate" | "snapshot"
	RecentActive bool
}

// ResourceView は台帳の資源 1 つ。
type ResourceView struct {
	ID      string
	Kind    string   // "firework" | "stall"
	Holders []string // 予約が成功した reqId（適用の順）。nil にしない
}

// NodeDetail は島 id のノードの詳細を返す。状態・イベント・トレース・入力ログ・時刻のどれも変えない（読むだけ）。
// Phase が Intro のとき（開会式の結果をまだ出していない）と、id が 1..N にないときは false。
// Raft のログが読めないとき（cluster.Node.Log のエラー）は Engine の誤りとして panic する。
//
// Process・Tick・Deliver・Propose・emit・push を呼ばず、pending・seq・trace・input・contactAt を書き換えない。
// そのため、呼んでも呼ばなくても Engine の結果（トレースのハッシュ）は変わらない。
func (e *Engine) NodeDetail(id uint64) (NodeDetail, bool) {
	if e.phase == PhaseIntro || id < 1 || id > uint64(len(e.nodes)) {
		return NodeDetail{}, false
	}
	node := e.nodes[id-1]
	st := node.Status()
	d := NodeDetail{
		ID:        st.ID,
		Role:      roleString(st.Role),
		Term:      st.Term,
		Vote:      st.Vote,
		Lead:      st.Lead,
		Commit:    st.Commit,
		Applied:   st.Applied,
		LastIndex: st.LastIndex,
		Ledger:    []ResourceView{},
	}

	ents, err := node.Log()
	if err != nil {
		panic(fmt.Sprintf("sim: node detail %d: %v", id, err))
	}
	if len(ents) > nodeDetailLogLimit {
		ents = ents[len(ents)-nodeDetailLogLimit:]
	}
	d.Log = make([]LogEntryDetail, 0, len(ents))
	for _, ent := range ents {
		typ, summary := entryTypeSummary(ent.Type, ent.Data)
		d.Log = append(d.Log, LogEntryDetail{
			Index:     ent.Index,
			Term:      ent.Term,
			Type:      typ,
			Summary:   summary,
			Committed: ent.Index <= st.Commit,
		})
	}

	for _, pr := range node.Progress() {
		if pr.ID == id {
			continue
		}
		d.Progress = append(d.Progress, ProgressView{
			ID:           pr.ID,
			Match:        pr.Match,
			Next:         pr.Next,
			State:        progressStateString(pr.State),
			RecentActive: pr.RecentActive,
		})
	}

	for _, r := range e.ledgers[id-1].Snapshot().Resources {
		holders := slices.Clone(r.Holders)
		if holders == nil {
			holders = []string{}
		}
		d.Ledger = append(d.Ledger, ResourceView{ID: r.ID, Kind: string(r.Kind), Holders: holders})
	}

	if st.Role != raft.StateLeader {
		d.TicksSinceLeaderContact = int((e.Now() - e.contactAt[id-1]) / e.cfg.Raft.TickMs)
	}
	return d, true
}

// noteLeaderContact は、島 to のノードに島 from からの Raft の手紙を配達して Process した直後に呼ぶ。
// from がそのノードの知っている Leader なら、その時刻を覚える（NodeDetail の選挙の推定。観察だけで、
// イベント・乱数・判定に使わない）。arrive の中で決定的に呼ばれるので、Replay でも同じ値になる。
func (e *Engine) noteLeaderContact(to, from uint64) {
	if lead := e.nodes[to-1].Status().Lead; lead != 0 && lead == from {
		e.contactAt[to-1] = e.Now()
	}
}

// progressStateString は Progress の状態の文字列（プロトコル §5.4 の progress[].state）。
func progressStateString(s tracker.StateType) string {
	switch s {
	case tracker.StateProbe:
		return "probe"
	case tracker.StateReplicate:
		return "replicate"
	case tracker.StateSnapshot:
		return "snapshot"
	}
	panic(fmt.Sprintf("sim: unknown progress state %d", s))
}
