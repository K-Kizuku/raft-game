package bot

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// Bot はボット船長。1 つの Bot は 1 つの Engine だけに使う。goroutine から同時に使わない。
type Bot interface {
	// Name は New に渡した名前（"greedy"・"novice"・"random"・"steady" のどれか）。
	Name() string
	// Act は、今の時刻に行う操作を e に Apply する。e.Phase() が PhaseSailing でなければ何もしない。
	// RunUntil・DrainEvents は呼ばない。失敗した Ack が返ったら、そこで止めて error を返す（ボットの誤り）。
	Act(e *sim.Engine) error
}

// ボットの名前。
const (
	nameGreedy = "greedy"
	nameNovice = "novice"
	nameRandom = "random"
	nameSteady = "steady"
)

// Names はボットの名前の一覧を昇順で返す（["greedy", "novice", "random", "steady"]）。返す slice は呼ぶたびに新しく作る。
func Names() []string {
	return []string{nameGreedy, nameNovice, nameRandom, nameSteady}
}

// New は name のボットを作る。ボットはすべての船を担当する。乱数は sim.Sub(seed, "bot/"+name) から取る
// （greedy・novice・steady は乱数を引かない）。name が Names() にない（空・大文字を含む）ならエラー。
func New(name string, seed uint64) (Bot, error) {
	b, err := newBot(name, seed)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// NewFor は、船 ferries（FerryID の集合）だけを担当する name のボットを作る（T-723 #379）。ボットは担当でない船に
// 操作を出さず、その船の分の乱数も引かない。ferries の順と重複は問わない。Engine にない船は、ないものとして扱う。
// name が Names() にない、ferries が空、または空の ID を含むならエラー。乱数は New と同じ流れから取る。
func NewFor(name string, seed uint64, ferries []string) (Bot, error) {
	b, err := newBot(name, seed)
	if err != nil {
		return nil, err
	}
	if len(ferries) == 0 {
		return nil, fmt.Errorf("bot: %s: no ferries", name)
	}
	if slices.Contains(ferries, "") {
		return nil, fmt.Errorf("bot: %s: empty ferry id in %q", name, ferries)
	}
	owned := slices.Clone(ferries)
	slices.Sort(owned)
	b.core().owned = slices.Compact(owned)
	return b, nil
}

// botImpl は 4 つのボットの型（共通の部分 base を持つ）。
type botImpl interface {
	Bot
	core() *base
}

// newBot は name のボットを、すべての船を担当するものとして作る。
func newBot(name string, seed uint64) (botImpl, error) {
	switch name {
	case nameRandom:
		return newRandom(sim.Sub(seed, streamName(name))), nil
	case nameGreedy:
		return newGreedy(), nil
	case nameNovice:
		return newNovice(), nil
	case nameSteady:
		return newSteady(), nil
	}
	return nil, fmt.Errorf("bot: unknown bot %q (want one of %q)", name, Names())
}

// streamName は乱数列の名前 "bot/<name>"（DesignDoc §9.1）。
func streamName(name string) string { return "bot/" + name }

// planFunc は、s の上で船 s.Ferries[i]（動ける船）の計画（コマンドの列）を作る。
// 計画は s の上で必ず成功するコマンドだけからなる。
type planFunc func(s sim.Snapshot, i int) []sim.Command

// base はボットに共通の部分（名前と航路と担当の船）。
type base struct {
	name string
	topo *topology // 最初の Act で Snapshot から作る
	// owned は担当の船の ID（昇順・重複なし）。nil ならすべての船を担当する（New）。
	owned []string
}

// Name は New に渡した名前。
func (b *base) Name() string { return b.name }

// core は共通の部分（botImpl）。
func (b *base) core() *base { return b }

// owns は、船 id がこのボットの担当か。
func (b *base) owns(id string) bool {
	if b.owned == nil {
		return true
	}
	_, ok := slices.BinarySearch(b.owned, id)
	return ok
}

// observe は、最初の 1 回は s から航路を作り、2 回目以降は s の航路が最初と同じであることを確かめる。
func (b *base) observe(s sim.Snapshot) {
	if b.topo == nil {
		b.topo = newTopology(s)
		return
	}
	b.topo.check(s)
}

// act は Act の流れ（すべてのボットで共通）。
func (b *base) act(e *sim.Engine, plan planFunc) error {
	if e.Phase() != sim.PhaseSailing {
		return nil
	}
	s := e.Snapshot()
	b.observe(s)
	for i := range s.Ferries {
		if !b.owns(s.Ferries[i].ID) || !canMove(s.Ferries[i], s.T) {
			continue
		}
		cmds := plan(s, i)
		if len(cmds) == 0 {
			continue
		}
		if err := applyPlan(e, cmds); err != nil {
			return err
		}
		// 同じ島のほかの船が、この船の積んだ後の港を見るように取り直す。Ferries の順は変わらない。
		s = e.Snapshot()
	}
	return nil
}

// applyPlan は cmds を順に e に Apply する。失敗した Ack が返ったら、残りを適用せずに error を返す。
func applyPlan(e *sim.Engine, cmds []sim.Command) error {
	for _, c := range cmds {
		ack := e.Apply(c)
		if !ack.OK {
			return fmt.Errorf("bot: %s ferry %q at %d: %s: %s", c.Type, c.FerryID, ack.T, ack.Code, ack.Message)
		}
	}
	return nil
}

// canMove は、船 f が今（t）動けるか（停泊していて、停泊時間が終わっている）。
func canMove(f sim.FerryView, t sim.SimTime) bool {
	return f.State == "docked" && f.ReadyAt <= t
}

// portLetters は島 island の港の手紙（ID の昇順）。
func portLetters(s sim.Snapshot, island uint64) []sim.LetterView {
	var out []sim.LetterView
	for _, l := range s.Letters {
		if l.Loc.Type == "port" && l.Loc.IslandID == island {
			out = append(out, l)
		}
	}
	return out
}

// cargoLetters は船 f の積み荷の手紙（積んだ順）。s.Letters に見つからなければ panic する（Engine の誤り）。
func cargoLetters(s sim.Snapshot, f sim.FerryView) []sim.LetterView {
	out := make([]sim.LetterView, 0, len(f.Cargo))
	for _, id := range f.Cargo {
		k, ok := slices.BinarySearchFunc(s.Letters, id, func(l sim.LetterView, id uint64) int {
			return cmp.Compare(l.ID, id)
		})
		if !ok {
			panic(fmt.Sprintf("bot: letter %d on ferry %q not in snapshot", id, f.ID))
		}
		out = append(out, s.Letters[k])
	}
	return out
}

// freeSpace は船 f の空き（負なら 0）。
func freeSpace(f sim.FerryView) int {
	return max(f.Capacity-len(f.Cargo), 0)
}

// rank は手紙の順位（小さいほど先に積む）。ゲームデザイン §3.4・sim.autoloadRank と同じ順
// （一覧にない手紙 other と未知の値は 6）。
func rank(l sim.LetterView) int {
	switch l.Label {
	case "request":
		return 1
	case "prevote", "prevote_resp", "vote", "vote_resp":
		return 2
	case "append", "append_probe":
		return 3
	case "append_resp", "append_reject":
		return 4
	case "heartbeat", "heartbeat_resp":
		return 5
	}
	return 6
}

// rankWeights は順位ごとの重み（greedy の点数。添字 = 順位）。ボットの方針で、ゲームの数値ではない。
var rankWeights = [...]int{0, 100, 30, 10, 5, 1, 1}

// staleWeight は古い手紙（Raft.Stale）の重み。順位によらない（運んでも効き目が小さいため）。
const staleWeight = 1

// weight は手紙の重み（greedy の点数に使う）。
func weight(l sim.LetterView) int {
	if l.Raft != nil && l.Raft.Stale {
		return staleWeight
	}
	return rankWeights[rank(l)]
}

// sortBy は手紙を（rankOf の順位, ID）の昇順に並べる。
func sortBy(ls []sim.LetterView, rankOf func(sim.LetterView) int) {
	slices.SortFunc(ls, func(a, b sim.LetterView) int {
		if c := cmp.Compare(rankOf(a), rankOf(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// letterIDs は手紙の ID の列（同じ順）。
func letterIDs(ls []sim.LetterView) []uint64 {
	out := make([]uint64, len(ls))
	for i, l := range ls {
		out[i] = l.ID
	}
	return out
}

// sumWeights は手紙の重み（weightOf）の和。
func sumWeights(ls []sim.LetterView, weightOf func(sim.LetterView) int) int {
	w := 0
	for _, l := range ls {
		w += weightOf(l)
	}
	return w
}

func cmdLoad(f string, ids []uint64) sim.Command {
	return sim.Command{Type: sim.CmdFerryLoad, FerryID: f, LetterIDs: ids}
}

func cmdUnload(f string, ids []uint64) sim.Command {
	return sim.Command{Type: sim.CmdFerryUnload, FerryID: f, LetterIDs: ids}
}

func cmdAutoload(f string, to uint64) sim.Command {
	return sim.Command{Type: sim.CmdFerryAutoload, FerryID: f, To: to}
}

func cmdDepart(f string, to uint64) sim.Command {
	return sim.Command{Type: sim.CmdFerryDepart, FerryID: f, To: to}
}
