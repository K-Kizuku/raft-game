package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// TechStats はリザルトの技術的な結果（DesignDoc §10.3、プロトコル §5.5 の stats のうち T-208 の分）。
type TechStats struct {
	LettersDelivered   map[string]int64 `json:"lettersDelivered"` // 手紙の区分（label）ごとの配達の数。0 の区分は入れない。nil にしない
	LeaderChanges      int64            `json:"leaderChanges"`
	AvgCommitLatencyMs int64            `json:"avgCommitLatencyMs"`
	LongestSilenceMs   int64            `json:"longestSilenceMs"`
	FinalLogs          []FinalLog       `json:"finalLogs"` // ノードの ID の順。nil にしない
}

// FinalLog はノード 1 つのログの形（プロトコル §5.5 の finalLogs[]）。
type FinalLog struct {
	ID        uint64      `json:"id"`
	LastIndex uint64      `json:"lastIndex"`
	Commit    uint64      `json:"commit"`
	Terms     [][3]uint64 `json:"terms"` // game.TermRanges の値。nil にしない
}

// statsLabels は手紙の区分（label.go の値）の決まった順。最後の "other" は知らない区分の受け皿。
var statsLabels = []string{
	"heartbeat", "heartbeat_resp", "append", "append_probe", "append_resp", "append_reject",
	"prevote", "prevote_resp", "vote", "vote_resp", "request", "other",
}

// statsLabelOther は statsLabels の "other" の添字。
var statsLabelOther = len(statsLabels) - 1

// statsLabelIndex は区分 label の statsLabels の添字（知らない区分は other）。
func statsLabelIndex(label string) int {
	if k := slices.Index(statsLabels, label); k >= 0 {
		return k
	}
	return statsLabelOther
}

// statsBook は統計と「今日の出来事」の帳簿（DesignDoc §10.3）。emit が出したイベントを observeStats で観察して作る。
type statsBook struct {
	letters   map[uint64]statsLetter // 港と船にある手紙（letter.created で入れ、delivered・dropped で消す）。検索だけに使い、走査しない
	delivered []int64                // statsLabels と同じ添字。配達の数
	leaders   int64                  // 航行中の node.leader_elected の数

	proposedAt  []SimTime // e.reqs.list と同じ添字。役場が最初に Propose した時刻。-1 = まだ
	committedAt []SimTime // e.reqs.list と同じ添字。最初の ledger.applied（duplicate を除く）の時刻。-1 = まだ

	lastDelivered []SimTime // 添字 = 島の ID − 1。最後の letter.delivered の時刻（まだなら 0 = 航行の始め）
	gap           []SimTime // 添字 = 島の ID − 1。配達と配達の間隔の最大（今の時刻までの分は含めない）
	gapStart      []SimTime // 添字 = 島の ID − 1。gap の間隔の始まり
	nodeTerm      []uint64  // 添字 = 島の ID − 1。最後の node.state の Term

	routeIDs     []string    // e.tr.Routes() の順の航路の ID（"A-B"）
	routeEnds    [][2]uint64 // routeIDs と同じ添字。{A, B}（A < B）
	routeBlocked []bool      // routeIDs と同じ添字。route.blocked・route.unblocked で覚えた、止まっているか

	facts                                            []game.StoryFact // 観察した出来事の材料（silence を除く。観察した順）
	sawCommit, sawRejected, sawDup, sawStale, sawPre bool             // 最初の 1 件だけを覚える種類を、もう覚えたか
}

// statsLetter は港と船にある手紙 1 通の、統計に使う値。
type statsLetter struct {
	label  int // statsLabels の添字
	from   uint64
	to     uint64
	raft   bool   // Raft の手紙か
	term   uint64 // Raft の手紙の Term
	reject bool   // Raft の手紙の Reject
}

// initStats は統計の帳簿を作る（New の中。initScore の後）。
func (e *Engine) initStats() {
	n := e.cfg.Islands
	routes := e.tr.Routes()
	b := statsBook{
		letters:       make(map[uint64]statsLetter),
		delivered:     make([]int64, len(statsLabels)),
		proposedAt:    make([]SimTime, len(e.reqs.list)),
		committedAt:   make([]SimTime, len(e.reqs.list)),
		lastDelivered: make([]SimTime, n),
		gap:           make([]SimTime, n),
		gapStart:      make([]SimTime, n),
		nodeTerm:      make([]uint64, n),
		routeIDs:      make([]string, len(routes)),
		routeEnds:     make([][2]uint64, len(routes)),
		routeBlocked:  make([]bool, len(routes)),
	}
	for i := range b.proposedAt {
		b.proposedAt[i] = -1
		b.committedAt[i] = -1
	}
	for k, r := range routes {
		b.routeIDs[k] = r.ID()
		b.routeEnds[k] = [2]uint64{r.A, r.B}
	}
	e.stats = b
}

// noteProposed は、役場が依頼 i を Propose したことを記録する（route の中）。最初の 1 回だけ覚える。
func (e *Engine) noteProposed(i int) {
	if e.stats.proposedAt[i] < 0 {
		e.stats.proposedAt[i] = e.Now()
	}
}

// observeStats は emit が出したイベント ev を 1 件観察する。イベントを出さず、スケジューラに積まず、乱数も引かない。
// 読むのはイベントの中身と、帳簿・e.cfg・e.phase・e.reqs だけ（Raft のノードの状態を読まない）。
func (e *Engine) observeStats(ev DomainEvent) {
	b := &e.stats
	sailing := e.phase == PhaseSailing
	switch d := ev.Data.(type) {
	case LetterCreated:
		l := statsLetter{label: statsLabelIndex(d.Letter.Label), from: d.Letter.From, to: d.Letter.To}
		if r := d.Letter.Raft; r != nil {
			l.raft, l.term, l.reject = true, r.Term, r.Reject
		}
		b.letters[d.Letter.ID] = l
	case LetterDelivered:
		l, ok := b.letters[d.LetterID]
		if !ok {
			l.label = statsLabelOther
		}
		delete(b.letters, d.LetterID)
		b.delivered[l.label]++
		e.statsDelivered(ev.T, d.IslandID, l, sailing)
	case LetterDropped:
		delete(b.letters, d.LetterID)
	case NodeState:
		if d.ID >= 1 && d.ID <= uint64(len(b.nodeTerm)) {
			b.nodeTerm[d.ID-1] = d.Term
		}
	case LeaderElected:
		if sailing {
			b.leaders++
			b.facts = append(b.facts, game.StoryFact{T: int64(ev.T), Kind: game.StoryLeaderElected, Island: d.ID, Term: d.Term})
		}
	case SteppedDown:
		if sailing {
			b.facts = append(b.facts, game.StoryFact{T: int64(ev.T), Kind: game.StorySteppedDown, Island: d.ID, Term: d.Term, Reason: d.Reason})
		}
	case LedgerApplied:
		e.statsApplied(ev.T, d, sailing)
	case RouteBlocked:
		var routes [][2]uint64
		for _, id := range d.RouteIDs {
			k := slices.Index(b.routeIDs, id)
			if k < 0 || b.routeBlocked[k] {
				continue
			}
			b.routeBlocked[k] = true
			routes = append(routes, b.routeEnds[k])
		}
		if sailing && len(routes) > 0 {
			b.facts = append(b.facts, game.StoryFact{T: int64(ev.T), Kind: game.StoryStorm, Routes: routes, Isolated: e.statsIsolated()})
		}
	case RouteUnblocked:
		for _, id := range d.RouteIDs {
			if k := slices.Index(b.routeIDs, id); k >= 0 {
				b.routeBlocked[k] = false
			}
		}
	}
}

// statsDelivered は手紙 l が島 island に届いた（letter.delivered）ことを、音信不通と出来事の材料に記録する。
func (e *Engine) statsDelivered(t SimTime, island uint64, l statsLetter, sailing bool) {
	b := &e.stats
	if island < 1 || island > uint64(len(b.lastDelivered)) {
		return
	}
	i := island - 1
	if g := t - b.lastDelivered[i]; g > b.gap[i] {
		b.gap[i], b.gapStart[i] = g, b.lastDelivered[i]
	}
	if !sailing || !l.raft {
		b.lastDelivered[i] = t
		return
	}
	// 届いた島の最後の node.state の Term より古い Term の手紙（#27 の scnStaleDeliveries と同じ判定）。
	if !b.sawStale && l.term < b.nodeTerm[i] {
		b.sawStale = true
		b.facts = append(b.facts, game.StoryFact{T: int64(t), Kind: game.StoryStaleIgnored, Island: l.from, Term: l.term})
	}
	if !b.sawPre && l.reject && statsLabels[l.label] == "prevote_resp" {
		b.sawPre = true
		b.facts = append(b.facts, game.StoryFact{T: int64(t), Kind: game.StoryPreVoteRejected, Island: island})
	}
	b.lastDelivered[i] = t
}

// statsApplied は台帳への適用（ledger.applied）を、Commit Latency の終点と出来事の材料に記録する。
// 依頼の帳簿にない reqId（テストが直接 Propose したものなど）は見ない。
func (e *Engine) statsApplied(t SimTime, d LedgerApplied, sailing bool) {
	b := &e.stats
	i, ok := e.reqs.byID[d.ReqID]
	if !ok {
		return
	}
	if d.Result != ledger.StatusDuplicate && b.committedAt[i] < 0 {
		b.committedAt[i] = t
	}
	if !sailing {
		return
	}
	req := e.reqs.list[i].req
	f := game.StoryFact{T: int64(t), Island: req.Origin, Resource: req.Resource}
	switch {
	case d.Result == ledger.StatusOK && !b.sawCommit:
		b.sawCommit = true
		f.Kind = game.StoryFirstCommit
	case d.Result == ledger.StatusRejected && !b.sawRejected:
		b.sawRejected = true
		f.Kind = game.StoryRejected
		f.Reason = d.Reason
	case d.Result == ledger.StatusDuplicate && !b.sawDup:
		b.sawDup = true
		f.Kind = game.StoryDuplicate
	default:
		return
	}
	b.facts = append(b.facts, f)
}

// statsIsolated は、航路が 1 本以上あり、そのすべてが止まっている島を ID の昇順に返す。nil にしない。
func (e *Engine) statsIsolated() []uint64 {
	b := &e.stats
	out := []uint64{}
	for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
		n, all := 0, true
		for k, r := range b.routeEnds {
			if r[0] != id && r[1] != id {
				continue
			}
			n++
			all = all && b.routeBlocked[k]
		}
		if n > 0 && all {
			out = append(out, id)
		}
	}
	return out
}

// statsNow は音信不通の区間の終わり（今の時刻。終わった後は DurationMs）。
func (e *Engine) statsNow() SimTime {
	if e.phase == PhaseEnded {
		return e.cfg.DurationMs
	}
	return e.Now()
}

// statsLongestSilence は最長の音信不通の島・始まり・長さを返す。同じ長さなら ID の小さい島、同じ島では早い間隔。
func (e *Engine) statsLongestSilence() (island uint64, start, dur SimTime) {
	b := &e.stats
	now := e.statsNow()
	dur = -1
	for i := range b.lastDelivered {
		g, s := b.gap[i], b.gapStart[i]
		if last := now - b.lastDelivered[i]; last > g {
			g, s = last, b.lastDelivered[i]
		}
		if g > dur {
			island, start, dur = uint64(i+1), s, g
		}
	}
	return island, start, max(dur, 0)
}

// TechStats は今の時点の技術的な結果を返す。どのフェーズでも呼べる（終わった後は最終の値）。
// 採点の有無に関係ない。状態・イベント・TraceHash・入力ログを変えない。
func (e *Engine) TechStats() TechStats {
	b := &e.stats
	ts := TechStats{
		LettersDelivered: map[string]int64{},
		LeaderChanges:    b.leaders,
		FinalLogs:        make([]FinalLog, len(e.nodes)),
	}
	for k, n := range b.delivered {
		if n != 0 {
			ts.LettersDelivered[statsLabels[k]] = n
		}
	}
	var sum, cnt int64
	for i, p := range b.proposedAt {
		if c := b.committedAt[i]; p >= 0 && c >= 0 {
			sum += int64(c - p)
			cnt++
		}
	}
	if cnt > 0 {
		ts.AvgCommitLatencyMs = sum / cnt
	}
	_, _, longest := e.statsLongestSilence()
	ts.LongestSilenceMs = int64(longest)
	for i, n := range e.nodes {
		ents, err := n.Log()
		if err != nil {
			panic(fmt.Sprintf("sim: node %d: log: %v", i+1, err))
		}
		terms := make([]uint64, len(ents))
		for j, ent := range ents {
			terms[j] = ent.Term
		}
		st := n.Status()
		ts.FinalLogs[i] = FinalLog{ID: st.ID, LastIndex: st.LastIndex, Commit: st.Commit, Terms: game.TermRanges(terms)}
	}
	return ts
}

// StoryFacts は今の時点の「今日の出来事」の材料を、T の昇順（同じ T は観察した順、silence はその T の最後）で返す。
// どのフェーズでも呼べる。nil にしない。状態・イベント・TraceHash・入力ログを変えない。
func (e *Engine) StoryFacts() []game.StoryFact {
	b := &e.stats
	out := make([]game.StoryFact, 0, len(b.facts)+1)
	for _, f := range b.facts {
		f.Routes = slices.Clone(f.Routes)
		f.Isolated = slices.Clone(f.Isolated)
		out = append(out, f)
	}
	if sc := e.cfg.Score; sc.Enabled() {
		island, start, dur := e.statsLongestSilence()
		if int64(dur) >= sc.SilenceAfterMs {
			f := game.StoryFact{T: int64(start), Kind: game.StorySilence, Island: island, DurationMs: int64(dur)}
			k := len(out)
			for k > 0 && out[k-1].T > f.T {
				k--
			}
			out = slices.Insert(out, k, f)
		}
	}
	return out
}
