package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// 船の故障のイベント（プロトコル §6、FR-EVT-03）。
const (
	EvFerryBroken   EventType = "ferry.broken"
	EvFerryRepaired EventType = "ferry.repaired"
)

// FerryBroken は ferry.broken（故障した・故障が延びた）。
type FerryBroken struct {
	FerryID string  `json:"ferryId"`
	Until   SimTime `json:"until"` // この時刻から出航できる
}

// FerryRepaired は ferry.repaired（故障が直った）。
type FerryRepaired struct {
	FerryID string  `json:"ferryId"`
	Until   SimTime `json:"until"` // 直った時刻（= このイベントの t）
}

func (FerryBroken) EventType() EventType   { return EvFerryBroken }
func (FerryRepaired) EventType() EventType { return EvFerryRepaired }

// 故障のスケジューラのイベントの種類。
// engine.go の iota（1〜）・score.go（300〜）・storm.go（400〜）と重ならないよう 500 から振る。
// 重なれば handleEvent の switch がコンパイルエラーになる。
const (
	kindBreakdownStart  EventKind = 500 + iota // ClassWorldEvent。Data は帳簿の添字 int
	kindBreakdownRepair                        // ClassWorldEvent。Data は repairTimer
)

// repairTimer は修理のタイマーの Data。積んだときの船の世代と比べ、延びた故障の古い修理を捨てる。
type repairTimer struct {
	Ferry ferry.FerryID
	Gen   uint64
}

// breakdownBook は故障の帳簿。台本の故障は New で全部を決める。
// 船ごとの値は船の添字（Config.Ferries の順）の配列で持つ（map を走査しない）。
type breakdownBook struct {
	list    []breakdownEntry
	pending []int64  // 添字 = 船。着いたら故障する長さ（0 = なし）
	gen     []uint64 // 添字 = 船。Break のたびに 1 増やす
}

// breakdownEntry は故障 1 つの帳簿の行。
type breakdownEntry struct {
	b         game.Breakdown
	startDone bool
}

// initBreakdowns は台本の故障の時刻と船を決めて帳簿に入れる（New の中）。
// 故障がなければ乱数列を作らない（ほかの用途の乱数列は変わらない。DesignDoc §9.1）。
func (e *Engine) initBreakdowns(seed uint64) error {
	e.cfg.Breakdowns = slices.Clone(e.cfg.Breakdowns)
	if len(e.cfg.Breakdowns) == 0 {
		return nil
	}
	ids := make([]string, len(e.cfg.Ferries))
	for i, f := range e.cfg.Ferries {
		ids[i] = string(f.ID)
	}
	plan, err := game.PlanBreakdowns(e.cfg.Breakdowns, ids, int64(e.cfg.DurationMs), Sub(seed, StreamBreakdown))
	if err != nil {
		return fmt.Errorf("sim: breakdowns: %w", err)
	}
	e.breakdowns.list = make([]breakdownEntry, len(plan))
	for i, b := range plan {
		e.breakdowns.list[i] = breakdownEntry{b: b}
	}
	e.breakdowns.pending = make([]int64, len(ids))
	e.breakdowns.gen = make([]uint64, len(ids))
	return nil
}

// startBreakdowns は帳簿の故障の開始を定義の順にスケジューラに積む（cmd.game.start）。
// DurationMs より後でも積む（ラウンドが先に終わるので処理されない）。
func (e *Engine) startBreakdowns() {
	for i, en := range e.breakdowns.list {
		e.push(SimTime(en.b.At), ClassWorldEvent, kindBreakdownStart, i)
	}
}

// handleBreakdown は故障のスケジューラのイベントを処理する。
func (e *Engine) handleBreakdown(ev Event) {
	switch ev.Kind {
	case kindBreakdownStart:
		e.breakdownStart(ev.Data.(int))
	case kindBreakdownRepair:
		e.breakdownRepair(ev.Data.(repairTimer))
	default:
		panic(fmt.Sprintf("sim: unknown breakdown event kind %d", ev.Kind))
	}
}

// breakdownStart は故障 i の開始。停泊中の船はその場で壊れ、航行中の船は着いたときに壊れる
// （ゲームデザイン §6「港にいるときに発生し、その場で止まる」）。同じ船に 2 つ来たら長い方。
func (e *Engine) breakdownStart(i int) {
	en := &e.breakdowns.list[i]
	en.startDone = true
	id := ferry.FerryID(en.b.Ferry)
	k := e.ferryIndex(id)
	f, _ := e.tr.Ferry(id)
	if f.State == ferry.Docked {
		e.breakFerry(k, id, en.b.DurationMs)
		return
	}
	e.breakdowns.pending[k] = max(e.breakdowns.pending[k], en.b.DurationMs)
}

// breakOnArrival は、着いた船 id に「着いたら故障」が入っていれば、今の時刻から壊す（arrive の中）。
func (e *Engine) breakOnArrival(id ferry.FerryID) {
	if e.breakdowns.pending == nil {
		return
	}
	k := e.ferryIndex(id)
	d := e.breakdowns.pending[k]
	if d == 0 {
		return
	}
	e.breakdowns.pending[k] = 0
	e.breakFerry(k, id, d)
}

// breakFerry は停泊中の船 id（添字 k）を今から durationMs だけ壊し、ferry.broken を出して修理を積む。
// 重なった故障は延び（直る時刻は大きい方）、そのときも ferry.broken を出す。修理は最後に積んだ世代のものだけが出す。
func (e *Engine) breakFerry(k int, id ferry.FerryID, durationMs int64) {
	now := int64(e.Now())
	until, err := e.tr.Break(now, id, now+durationMs)
	if err != nil {
		panic(fmt.Sprintf("sim: break %q: %v", id, err))
	}
	e.breakdowns.gen[k]++
	e.emit(FerryBroken{FerryID: string(id), Until: SimTime(until)})
	// DurationMs より後でも積む（ラウンドが先に終わるので処理されない）。
	e.push(SimTime(until), ClassWorldEvent, kindBreakdownRepair, repairTimer{Ferry: id, Gen: e.breakdowns.gen[k]})
}

// breakdownRepair は修理のタイマー。故障が今ちょうど直り、積んだあとに故障が重なっていなければ ferry.repaired を出す。
// 出航できるようになるのは ferry の判定（now < BrokenUntil）のとおり直る時刻ちょうどで、これは知らせるだけ。
func (e *Engine) breakdownRepair(r repairTimer) {
	k := e.ferryIndex(r.Ferry)
	f, _ := e.tr.Ferry(r.Ferry)
	now := e.Now()
	if SimTime(f.BrokenUntil) != now || e.breakdowns.gen[k] != r.Gen {
		return
	}
	e.emit(FerryRepaired{FerryID: string(r.Ferry), Until: now})
}

// ferryIndex は船 id の Config.Ferries での添字を返す。ない船は Engine の誤りなので panic する。
func (e *Engine) ferryIndex(id ferry.FerryID) int {
	k := slices.IndexFunc(e.cfg.Ferries, func(f ferry.FerryConfig) bool { return f.ID == id })
	if k < 0 {
		panic(fmt.Sprintf("sim: unknown ferry %q", id))
	}
	return k
}

// nextBreakdownAt は、まだ処理していない故障の開始と、これから直る故障の修理のうち、DurationMs 以下で
// いちばん早い時刻を返す（NextNotableAt が使う）。修理は船の今の BrokenUntil（延びた故障は延びた方）。
func (e *Engine) nextBreakdownAt() (SimTime, bool) {
	var best SimTime
	found := false
	consider := func(t SimTime) {
		if t > e.cfg.DurationMs {
			return
		}
		if !found || t < best {
			best, found = t, true
		}
	}
	for _, en := range e.breakdowns.list {
		if !en.startDone {
			consider(SimTime(en.b.At))
		}
	}
	if len(e.breakdowns.list) == 0 {
		return best, found
	}
	now := e.Now()
	for _, f := range e.tr.Ferries() {
		if until := SimTime(f.BrokenUntil); until > now {
			consider(until)
		}
	}
	return best, found
}
