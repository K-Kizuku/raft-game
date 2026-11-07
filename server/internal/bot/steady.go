package bot

import (
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// steadyBot は greedy と同じ動き方で、手紙の優先度を状況で切り替えるボット（T-501c #257）。乱数を引かない。
//
//   - 代表（Leader）が健在なあいだ: 定期便（「記録して」「お元気ですか」とその返事）を投票の手紙より優先する。
//     PreVote と CheckQuorum が有効なら、代表が健在なあいだの投票のお願いはほかの島に断られるので、運んでも効き目が小さい。
//     定期便を届ければ、立候補した島は Follower に戻る
//   - 代表がいない・孤立したとき: greedy と同じ優先度（投票の手紙を定期便より先に運ぶ）
//
// どちらのときも、次の 2 つが greedy と違う（嵐で分かれた側で、投票の手紙を運ぶ船を残すため）。
//   - 行き先までの最短経路に、今は通れない航路がある手紙を積まない（経由の手紙は途中の港で降ろせないので、
//     積むと嵐が明けるまで船の空きをふさぐ）
//   - 積み荷がすべて嵐の向こう側へ行く手紙でも、空きがあれば迎えに行く（greedy は積み荷が空のときだけ）
//
// greedy は状況によらず投票の手紙を優先するので、端の島が立候補するとほかの島への定期便が止まり、
// 代表が CheckQuorum でおりることがある（T-501b #218 の伊豆版）。
type steadyBot struct {
	base
}

func newSteady() *steadyBot {
	return &steadyBot{base: base{name: nameSteady}}
}

// Act は Bot.Act。
func (b *steadyBot) Act(e *sim.Engine) error { return b.act(e, b.plan) }

// plan は動ける船 s.Ferries[i] の計画を作る。状況は船ごとに s から読み直す（同じ Act の中でも、前の船の積み込みで変わらない）。
func (b *steadyBot) plan(s sim.Snapshot, i int) []sim.Command {
	if leaderHealthy(b.topo, s) {
		return planByPriority(b.topo, s, i, steadyPriority)
	}
	return planByPriority(b.topo, s, i, steadyElectionPriority)
}

// steadyRank は、代表が健在なあいだの手紙の順位（小さいほど先に積む）。greedy の順位（rank）から投票の手紙だけを
// 定期便の後ろに下げた順: 依頼書 → 記録して → 記録した → お元気ですか・元気です → 投票の手紙 → そのほか。
func steadyRank(l sim.LetterView) int {
	switch l.Label {
	case "request":
		return 1
	case "append", "append_probe":
		return 2
	case "append_resp", "append_reject":
		return 3
	case "heartbeat", "heartbeat_resp":
		return 4
	case "prevote", "prevote_resp", "vote", "vote_resp":
		return 5
	}
	return 6
}

// steadyRankWeights は steadyRank の順位ごとの重み（添字 = 順位）。投票の手紙以外は greedy の重み（rankWeights）と同じ。
// ボットの方針で、ゲームの数値ではない。
var steadyRankWeights = [...]int{0, 100, 10, 5, 1, 1, 1}

// steadyWeight は、代表が健在なあいだの手紙の重み。古い手紙（Raft.Stale）は staleWeight。
func steadyWeight(l sim.LetterView) int {
	if l.Raft != nil && l.Raft.Stale {
		return staleWeight
	}
	return steadyRankWeights[steadyRank(l)]
}

// steadyPriority は、代表が健在なあいだの優先度。順位がおまかせ積み込みと違うので、直行の手紙も load で積む。
var steadyPriority = priority{rank: steadyRank, weight: steadyWeight, autoload: false, pickupWithCargo: true, skipBlocked: true}

// steadyElectionPriority は、代表がいない・孤立したときの優先度。順位と重みは greedy と同じ。
var steadyElectionPriority = priority{rank: rank, weight: weight, autoload: true, pickupWithCargo: true, skipBlocked: true}

// leaderHealthy は、s の時点で代表が健在か。次の 2 つを満たすとき true。
//   - 代表（role が leader）がいて、その Term がどの島の Term より小さくない（新しい Term の立候補が始まっていない）
//   - 代表の島から、通れる航路だけでたどれる島（代表の島を含む）が過半数ある（嵐で孤立していない）
func leaderHealthy(t *topology, s sim.Snapshot) bool {
	var (
		lead     uint64
		leadTerm uint64
		maxTerm  uint64
	)
	for _, nd := range s.Nodes { // ID の昇順
		maxTerm = max(maxTerm, nd.Term)
		if nd.Role == "leader" && (lead == 0 || nd.Term > leadTerm) {
			lead, leadTerm = nd.ID, nd.Term
		}
	}
	if lead == 0 || leadTerm < maxTerm {
		return false
	}
	return t.reachable(s, lead)*2 > t.n
}

// reachable は、島 src から s の時点で通れる航路だけでたどれる島の数（src を含む）。
func (t *topology) reachable(s sim.Snapshot, src uint64) int {
	if !t.valid(src) {
		return 0
	}
	seen := make([]bool, t.n+1)
	seen[src] = true
	queue := []uint64{src}
	count := 0
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		count++
		for _, d := range t.openNeighbors(s, v) {
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	return count
}

// pathOpen は、島 from から島 to への最短経路（next でたどる）の航路が、s の時点ですべて通れるか。
// from == to なら true。届かない・範囲の外なら false。
func (t *topology) pathOpen(s sim.Snapshot, from, to uint64) bool {
	if !t.valid(from) || !t.valid(to) {
		return false
	}
	for v := from; v != to; {
		d := t.next(v, to)
		if d == 0 || !t.passable(s, v, d) {
			return false
		}
		v = d
	}
	return true
}

// openLetters は、島 at の港の手紙 ls のうち、at から行き先までの最短経路がすべて通れるものを返す（同じ順。ls を書き換える）。
func openLetters(t *topology, s sim.Snapshot, at uint64, ls []sim.LetterView) []sim.LetterView {
	return slices.DeleteFunc(ls, func(l sim.LetterView) bool { return !t.pathOpen(s, at, l.To) })
}
