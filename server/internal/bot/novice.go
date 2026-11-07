package bot

import "github.com/K-Kizuku/raft-game/server/internal/sim"

// noviceThinkMs は、船が着いてから novice が操作するまでの時間（人が港の様子を見て、ボタンを押すまでの間）。
// 停泊時間（transport.dockMs）が長ければ、停泊時間が終わるまで待つ。ボットの方針で、ゲームの数値ではない。
const noviceThinkMs sim.SimTime = 3000

// noviceBot は、初めて遊ぶ人がチュートリアルのとおりに遊ぶ動きをまねるボット（T-501f #311）。乱数を引かない。
//
//   - 船が着いてから noviceThinkMs たつ（停泊時間が終わっていなければ、それまで待つ）と操作する。
//     ラウンドの始めは、時刻 0 に着いたものとみなす
//   - 行き先は考えない。島を順に回る: 通れる隣の島のうち、来た島を除いたもの（ほかになければ来た島）から、
//     来た島の次の ID（来た島より大きい ID の最小。なければ最小の ID）を選ぶ。最初の出航は、通れる隣の島の最小の ID
//   - 行き先宛ての手紙が港にあり、空きがあれば、おまかせ積み込み（cmd.ferry.autoload）で積む。
//     経由の手紙は積まない（ドラッグで 1 通ずつ選ぶことはしない）
//   - 積めるものがなくても出航する。通れる隣の島がなければ待つ
type noviceBot struct {
	base
	// arrived は、船ごとの最後に着いた時刻（航海中の Snapshot の ArriveAt）。ない船は時刻 0 に着いたものとみなす。
	arrived map[string]sim.SimTime
	// came は、船ごとの来た島（最後に出航した島）。ない船はまだ出航していない。
	came map[string]uint64
}

func newNovice() *noviceBot {
	return &noviceBot{
		base:    base{name: nameNovice},
		arrived: map[string]sim.SimTime{},
		came:    map[string]uint64{},
	}
}

// Act は Bot.Act。航海中の船の着く時刻を覚えてから、動ける船を動かす。
func (b *noviceBot) Act(e *sim.Engine) error {
	if e.Phase() != sim.PhaseSailing {
		return nil
	}
	for _, f := range e.Snapshot().Ferries {
		if f.State == "sailing" {
			b.arrived[f.ID] = f.ArriveAt
		}
	}
	return b.act(e, b.plan)
}

// plan は動ける船 s.Ferries[i] の計画を作る。結果は 空・[depart]・[autoload, depart] のどれか。
func (b *noviceBot) plan(s sim.Snapshot, i int) []sim.Command {
	f := s.Ferries[i]
	if s.T < b.arrived[f.ID]+noviceThinkMs {
		return nil
	}
	at := f.IslandID
	came, hasCame := b.came[f.ID]
	to := nextInTurn(b.topo.openNeighbors(s, at), came, hasCame)
	if to == 0 {
		return nil
	}
	var cmds []sim.Command
	if freeSpace(f) > 0 && hasLetterTo(portLetters(s, at), to) {
		cmds = append(cmds, cmdAutoload(f.ID, to))
	}
	b.came[f.ID] = at
	return append(cmds, cmdDepart(f.ID, to))
}

// nextInTurn は、通れる隣の島 open（昇順）から、来た島 came の次に回る島を選ぶ。純粋な関数。
//   - open が空なら 0
//   - まだ出航していない（hasCame == false）なら open の先頭
//   - came を除いた島が残れば、その中で came より大きい ID の最小（なければ最小の ID）
//   - came しか残らなければ came（行き止まりから戻る）
func nextInTurn(open []uint64, came uint64, hasCame bool) uint64 {
	if len(open) == 0 {
		return 0
	}
	if !hasCame {
		return open[0]
	}
	var first, after uint64
	for _, d := range open {
		if d == came {
			continue
		}
		if first == 0 {
			first = d
		}
		if after == 0 && d > came {
			after = d
		}
	}
	switch {
	case after != 0:
		return after
	case first != 0:
		return first
	}
	return came
}

// hasLetterTo は、港の手紙 port に島 to 宛てのものがあるか（おまかせ積み込みが積めるか）。
func hasLetterTo(port []sim.LetterView, to uint64) bool {
	for _, l := range port {
		if l.To == to {
			return true
		}
	}
	return false
}
