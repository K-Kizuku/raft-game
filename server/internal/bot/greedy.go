package bot

import "github.com/K-Kizuku/raft-game/server/internal/sim"

// greedyBot は、優先度の高い手紙が多い島へ向かうボット。乱数を引かない。
type greedyBot struct {
	base
}

func newGreedy() *greedyBot {
	return &greedyBot{base: base{name: nameGreedy}}
}

// Act は Bot.Act。
func (b *greedyBot) Act(e *sim.Engine) error { return b.act(e, b.plan) }

// greedyChoice は、隣の島 d へ向かうときの点数と積む手紙。
type greedyChoice struct {
	to     uint64
	score  int
	direct []sim.LetterView // To == d の手紙（おまかせ積み込みが選ぶものと同じ）
	relay  []sim.LetterView // d を経由する手紙（cmd.ferry.load で積む）
}

// plan は動ける船 s.Ferries[i] の計画を作る。
func (b *greedyBot) plan(s sim.Snapshot, i int) []sim.Command {
	f := s.Ferries[i]
	at := f.IslandID
	t := b.topo
	cargo := cargoLetters(s, f)
	port := portLetters(s, at)
	free := freeSpace(f)

	// 1・2. 通れる隣の島ごとの点数。最大の島（同じなら ID の小さい方）。
	var best greedyChoice
	for _, d := range t.openNeighbors(s, at) {
		c := greedyScore(t, at, d, cargo, port, free)
		if c.score > best.score {
			best = c
		}
	}

	// 3. 運ぶものがある。経由の手紙を先に load で積み、残りの空きにおまかせ積み込みで直行の手紙を積む
	// （おまかせ積み込みは空きの数まで積むので、先に呼ぶと経由の手紙の場所がなくなる）。
	// おまかせ積み込みは直行の手紙を (順位, ID) の昇順に積むので、残りの空き（= len(best.direct)）に積むのは best.direct と同じ。
	if best.score >= 1 {
		var cmds []sim.Command
		if len(best.relay) > 0 {
			cmds = append(cmds, cmdLoad(f.ID, letterIDs(best.relay)))
		}
		if len(best.direct) > 0 {
			cmds = append(cmds, cmdAutoload(f.ID, best.to))
		}
		return append(cmds, cmdDepart(f.ID, best.to))
	}

	// 4. 迎えに行く（積み荷が空のときだけ）。
	if len(cargo) > 0 {
		return nil
	}
	var (
		target  uint64
		targetW int
	)
	for j := uint64(1); j <= uint64(t.n); j++ {
		if j == at || t.next(at, j) == 0 {
			continue
		}
		w := sumWeights(portLetters(s, j))
		if target == 0 || w > targetW || (w == targetW && t.hops[at][j] < t.hops[at][target]) {
			target, targetW = j, w
		}
	}
	if target == 0 || targetW == 0 {
		return nil
	}
	d := t.next(at, target)
	if !t.passable(s, at, d) {
		return nil
	}
	return []sim.Command{cmdDepart(f.ID, d)}
}

// greedyScore は、島 at の船が隣の島 d へ向かうときの点数と積む手紙を求める。
// cargo は積み荷の手紙、port は島 at の港の手紙（ID の昇順）、free は空き。
func greedyScore(t *topology, at, d uint64, cargo, port []sim.LetterView, free int) greedyChoice {
	c := greedyChoice{to: d}

	// 積み荷の分: d で配達される・d を経由する手紙。
	for _, l := range cargo {
		if t.next(at, l.To) == d {
			c.score += weight(l)
		}
	}

	// 直行の分と経由の分。直行と経由を分けずに（順位, ID）の昇順に並べ、空きの数まで選ぶ。
	// 直行を先に選ぶと、近い島宛ての手紙で船が埋まり、遠い島宛ての MsgApp が港で溢れて（R4）、
	// 遠い島の台帳が Commit に追いつけなくなる（T-207b #203）。
	var cands []sim.LetterView
	for _, l := range port {
		if l.To == d || t.next(at, l.To) == d {
			cands = append(cands, l)
		}
	}
	sortByRank(cands)
	for _, l := range cands[:min(free, len(cands))] {
		if l.To == d {
			c.direct = append(c.direct, l)
		} else {
			c.relay = append(c.relay, l)
		}
		c.score += weight(l)
	}
	return c
}
