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

	// 3. 運ぶものがある。
	if best.score >= 1 {
		var cmds []sim.Command
		if len(best.direct) > 0 {
			cmds = append(cmds, cmdAutoload(f.ID, best.to))
		}
		if len(best.relay) > 0 {
			cmds = append(cmds, cmdLoad(f.ID, letterIDs(best.relay)))
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

	// 直行の分と経由の分。
	var direct, relay []sim.LetterView
	for _, l := range port {
		switch {
		case l.To == d:
			direct = append(direct, l)
		case t.next(at, l.To) == d:
			relay = append(relay, l)
		}
	}
	sortByRank(direct)
	sortByRank(relay)
	c.direct = direct[:min(free, len(direct))]
	c.relay = relay[:min(free-len(c.direct), len(relay))]
	c.score += sumWeights(c.direct) + sumWeights(c.relay)
	return c
}
