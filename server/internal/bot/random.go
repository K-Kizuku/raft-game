package bot

import (
	"math/rand/v2"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// random の確率（ボットの方針で、ゲームの数値ではない）。
const (
	randomWaitOneIn   = 4  // 1/4 で、この船は今回は何もしない
	randomUnloadOneIn = 10 // 1/10 で、積み間違いを取り消す（その島から積んだ手紙を 1 通降ろす）
	randomAutoOneIn   = 2  // 1/2 で、おまかせ積み込みを使う
)

// randomBot はランダムに積んで出航するボット。乱数は rng の IntN だけを使う。
type randomBot struct {
	base
	rng *rand.Rand
}

func newRandom(rng *rand.Rand) *randomBot {
	return &randomBot{base: base{name: nameRandom}, rng: rng}
}

// Act は Bot.Act。
func (b *randomBot) Act(e *sim.Engine) error { return b.act(e, b.plan) }

// plan は動ける船 s.Ferries[i] の計画を作る。結果は 空・[unload]・[autoload, depart]・[load, depart]・[depart] のどれか。
// 乱数を引く順と回数は手順のとおり（条件が成り立たない手順では引かない）。
func (b *randomBot) plan(s sim.Snapshot, i int) []sim.Command {
	f := s.Ferries[i]
	a := f.IslandID

	// 1. 待つ。
	if b.rng.IntN(randomWaitOneIn) == 0 {
		return nil
	}

	// 2. 積み間違いの取り消し（From == a の手紙だけ。ほかは ferry が断る）。
	var mine []uint64
	for _, l := range cargoLetters(s, f) {
		if l.From == a {
			mine = append(mine, l.ID)
		}
	}
	if len(mine) > 0 && b.rng.IntN(randomUnloadOneIn) == 0 {
		id := mine[b.rng.IntN(len(mine))]
		return []sim.Command{cmdUnload(f.ID, []uint64{id})}
	}

	// 3. 行き先。
	open := b.topo.openNeighbors(s, a)
	if len(open) == 0 {
		return nil
	}
	to := open[b.rng.IntN(len(open))]

	// 4. 積む。
	var cmds []sim.Command
	free := freeSpace(f)
	port := portLetters(s, a)
	if free > 0 && len(port) > 0 {
		useAuto := b.rng.IntN(randomAutoOneIn) == 0
		if useAuto && slices.ContainsFunc(port, func(l sim.LetterView) bool { return l.To == to }) {
			cmds = append(cmds, cmdAutoload(f.ID, to))
		} else {
			ids := letterIDs(port)
			n := len(ids)
			k := b.rng.IntN(min(free, n) + 1)
			for j := range k {
				r := j + b.rng.IntN(n-j)
				ids[j], ids[r] = ids[r], ids[j]
			}
			if k > 0 {
				cmds = append(cmds, cmdLoad(f.ID, ids[:k:k]))
			}
		}
	}

	// 5. 出航する。
	return append(cmds, cmdDepart(f.ID, to))
}
