package sim

// 決定性テスト（T-111、要件 V-6。DesignDoc §3.1・§4.3・§10.1・§10.2・§14）。
// 同じシード・操作入力からは、時間の進め方（RunUntil の刻み方）・DrainEvents の時点・テストモードの有無に
// よらず、同じイベントの列と TraceHash になることを確かめる。
// 補助の名前はすべて det で始める（同じパッケージのほかのテストとの衝突を避けるため）。

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// detOp は記録した操作 1 つ。Cmd と Propose のどちらか一方だけを持つ。
type detOp struct {
	T       SimTime      // 操作した時点の Now()
	Cmd     *Command     // Apply したコマンド（失敗したものも含む）
	Propose *detProposal // e.propose したもの
}

type detProposal struct {
	Island uint64
	Data   []byte // ledger.Encode の結果
}

// detRun は 1 回の実行の結果（「比べるもの」）。
type detRun struct {
	Ops      []detOp
	Acks     []Ack
	PropErrs []bool
	Events   []DomainEvent
	Hash     [32]byte
	Input    []InputEntry
	Now      SimTime
	Phase    Phase
}

// detConfig3 は testConfig に航路 1-3 と船 f2 を足した 3 島の設定。
func detConfig3() Config {
	cfg := testConfig()
	cfg.Routes = append(slices.Clone(cfg.Routes), ferry.RouteConfig{A: 1, B: 3, TravelMs: 6000})
	cfg.Ferries = append(slices.Clone(cfg.Ferries), ferry.FerryConfig{ID: "f2", Home: 3, Capacity: 3, SpeedPermille: 1200})
	return cfg
}

// detConfig5 は 5 島の環状の設定（島・航路・船のほかは testConfig のまま）。
func detConfig5() Config {
	cfg := testConfig()
	cfg.Islands = 5
	cfg.Routes = []ferry.RouteConfig{
		{A: 1, B: 2, TravelMs: 3000},
		{A: 2, B: 3, TravelMs: 4000},
		{A: 3, B: 4, TravelMs: 3500},
		{A: 4, B: 5, TravelMs: 4500},
		{A: 1, B: 5, TravelMs: 5000},
	}
	cfg.Ferries = []ferry.FerryConfig{
		{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		{ID: "f2", Home: 3, Capacity: 4, SpeedPermille: 1000},
		{ID: "f3", Home: 5, Capacity: 3, SpeedPermille: 800},
	}
	return cfg
}

// detCase は 1 つの（設定, シード）。
type detCase struct {
	name string
	cfg  Config
	seed uint64
}

// detCases は detConfig3 のシード 1..seeds3 と、detConfig5 のシード 1..seeds5 の組を並べる。
func detCases(seeds3, seeds5 int) []detCase {
	var out []detCase
	for s := 1; s <= seeds3; s++ {
		out = append(out, detCase{fmt.Sprintf("3islands/seed%d", s), detConfig3(), uint64(s)})
	}
	for s := 1; s <= seeds5; s++ {
		out = append(out, detCase{fmt.Sprintf("5islands/seed%d", s), detConfig5(), uint64(s)})
	}
	return out
}

// detNeighbors は island と航路でつながる島を、e.tr.Routes() の順に返す。
func detNeighbors(e *Engine, island uint64) []uint64 {
	var out []uint64
	for _, r := range e.tr.Routes() {
		switch island {
		case r.A:
			out = append(out, r.B)
		case r.B:
			out = append(out, r.A)
		}
	}
	return out
}

// detLetterTo は手紙の宛先を返す（見つからなければ 0）。
func detLetterTo(e *Engine, id ferry.LetterID) uint64 {
	l, ok := e.tr.Letter(id)
	if !ok {
		return 0
	}
	return l.To
}

// detIsVote は、手紙が選挙（事前投票・投票とその返事）の Raft のメッセージかを返す。
func detIsVote(e *Engine, id ferry.LetterID) bool {
	l, ok := e.tr.Letter(id)
	if !ok || l.Kind != ferry.LetterRaft {
		return false
	}
	switch l.Raft.GetType() {
	case pb.MessageType_MsgPreVote, pb.MessageType_MsgPreVoteResp, pb.MessageType_MsgVote, pb.MessageType_MsgVoteResp:
		return true
	}
	return false
}

// detLeader は Status() が Leader のノードのうち、ID が最小のものを返す（いなければ 0）。
func detLeader(e *Engine) uint64 {
	for _, n := range e.nodes {
		if st := n.Status(); st.Role == raft.StateLeader {
			return st.ID
		}
	}
	return 0
}

// detRecorder は detRecord の 1 回の実行の途中の状態。
type detRecorder struct {
	t   *testing.T
	e   *Engine
	r   *rand.Rand
	run detRun
}

func (d *detRecorder) apply(cmd Command) Ack {
	c := cloneCommand(cmd)
	d.run.Ops = append(d.run.Ops, detOp{T: d.e.Now(), Cmd: &c})
	ack := d.e.Apply(cmd)
	d.run.Acks = append(d.run.Acks, ack)
	return ack
}

// mustFail は失敗するはずのコマンドを送る。成功したらボットの誤り。
func (d *detRecorder) mustFail(cmd Command) {
	if ack := d.apply(cmd); ack.OK {
		d.t.Fatalf("t=%d: %+v succeeded, want failure", d.e.Now(), cmd)
	}
}

func (d *detRecorder) propose(island uint64, data []byte) {
	d.run.Ops = append(d.run.Ops, detOp{T: d.e.Now(), Propose: &detProposal{Island: island, Data: slices.Clone(data)}})
	err := d.e.propose(island, data)
	if err != nil && !errors.Is(err, raft.ErrProposalDropped) {
		d.t.Fatalf("t=%d: propose(%d): %v", d.e.Now(), island, err)
	}
	d.e.afterStep()
	d.run.PropErrs = append(d.run.PropErrs, err != nil)
}

func (d *detRecorder) drain() {
	d.run.Events = append(d.run.Events, d.e.DrainEvents()...)
}

// moveFerry は停泊していて出航できる船 f に、積んで出航させる（1/4 の確率で積んだまま待つ）。
// avoid が 0 でなければ、その島へは向かわず、その島宛ての手紙を積まない。その島にいる船は、何も積まずに離れる。
func (d *detRecorder) moveFerry(f ferry.Ferry, avoid uint64) {
	e, r := d.e, d.r
	nb := slices.DeleteFunc(detNeighbors(e, f.At), func(x uint64) bool { return x == avoid })
	if len(nb) == 0 {
		return
	}
	port := slices.DeleteFunc(e.tr.Port(f.At), func(id ferry.LetterID) bool { return avoid != 0 && detLetterTo(e, id) == avoid })
	free := f.Capacity - len(f.Cargo)
	if f.At == avoid {
		port, free = nil, 0
	}
	// 選挙の手紙（隣の島宛て）はいつも先に積む。Leader の交代の場面を作るため。
	var pick []ferry.LetterID
	for _, id := range port {
		if slices.Contains(nb, detLetterTo(e, id)) && detIsVote(e, id) {
			pick = append(pick, id)
		}
	}
	if r.IntN(2) == 0 {
		// 隣の島宛ての手紙を優先する（港の順）。足りなければ残りから順に足す。
		for _, id := range port {
			if slices.Contains(nb, detLetterTo(e, id)) && !slices.Contains(pick, id) {
				pick = append(pick, id)
			}
		}
		for _, id := range port {
			if !slices.Contains(pick, id) {
				pick = append(pick, id)
			}
		}
	} else {
		// 港の手紙をランダムに選ぶ。
		for _, i := range r.Perm(len(port)) {
			if !slices.Contains(pick, port[i]) {
				pick = append(pick, port[i])
			}
		}
	}
	pick = pick[:min(len(pick), free)]
	if len(pick) > 0 {
		ids := make([]uint64, len(pick))
		for i, id := range pick {
			ids[i] = uint64(id)
		}
		if ack := d.apply(Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: ids}); !ack.OK {
			d.t.Fatalf("t=%d: load %v on %s: %+v", e.Now(), ids, f.ID, ack)
		}
	}
	f, _ = e.tr.Ferry(f.ID)
	lead := detLeader(e)
	var dests, votes []uint64
	toLeader := false
	for _, id := range f.Cargo {
		if to := detLetterTo(e, id); slices.Contains(nb, to) {
			dests = append(dests, to)
			if detIsVote(e, id) {
				votes = append(votes, to)
			}
			toLeader = toLeader || (lead != 0 && to == lead)
		}
	}
	// 積んだまま待つ（港で降ろす操作の場面を作る）。選挙の手紙を積んでいれば待たない。
	if len(pick) > 0 && len(votes) == 0 && r.IntN(4) == 0 {
		return
	}
	to := nb[r.IntN(len(nb))]
	switch {
	case len(votes) > 0:
		to = votes[0]
	case toLeader && r.IntN(4) < 3: // Leader への返事を先に届ける（Leader を保つ）
		to = lead
	case len(dests) > 0 && r.IntN(4) < 3:
		to = dests[r.IntN(len(dests))]
	}
	if ack := d.apply(Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: to}); !ack.OK {
		d.t.Fatalf("t=%d: depart %s to %d: %+v", e.Now(), f.ID, to, ack)
	}
}

// detReqIDs は提案する予約の ReqID（s- は屋台、f- は花火）。
var detReqIDs = []string{"s-1", "s-2", "s-3", "f-1", "f-2"}

func (d *detRecorder) proposeRandom(cfg Config) {
	e, r := d.e, d.r
	island := 1 + uint64(r.IntN(cfg.Islands))
	// 3/4 で Leader（いれば）に提案する（Commit の場面を増やす）。
	if lead := detLeader(e); lead != 0 && r.IntN(4) < 3 {
		island = lead
	}
	reqID := detReqIDs[r.IntN(len(detReqIDs))]
	kind, res := ledger.KindStallReserve, "stall:main"
	if strings.HasPrefix(reqID, "f-") {
		kind, res = ledger.KindFireworkReserve, "firework:a"
	}
	data, err := ledger.Encode(ledger.Command{V: ledger.Version, ReqID: reqID, Origin: island, Kind: kind, Resource: res})
	if err != nil {
		d.t.Fatalf("Encode: %v", err)
	}
	d.propose(island, data)
}

// unloadRandom は、港にいて今いる島から積んだ手紙を持つ船から 1 通降ろす。
func (d *detRecorder) unloadRandom() {
	e, r := d.e, d.r
	type cand struct {
		f  ferry.FerryID
		id ferry.LetterID
	}
	var cs []cand
	for _, f := range e.tr.Ferries() {
		if f.State != ferry.Docked {
			continue
		}
		for _, id := range f.Cargo {
			if l, ok := e.tr.Letter(id); ok && l.From == f.At {
				cs = append(cs, cand{f.ID, id})
			}
		}
	}
	if len(cs) == 0 {
		return
	}
	c := cs[r.IntN(len(cs))]
	if ack := d.apply(Command{Type: CmdFerryUnload, FerryID: string(c.f), LetterIDs: []uint64{uint64(c.id)}}); !ack.OK {
		d.t.Fatalf("t=%d: unload %d from %s: %+v", e.Now(), c.id, c.f, ack)
	}
}

// failRandom は、わざと失敗するコマンドを 1 つ送る。
func (d *detRecorder) failRandom() {
	e, r := d.e, d.r
	fs := e.tr.Ferries()
	now := int64(e.Now())
	switch r.IntN(3) {
	case 0: // 航行中の船への load
		for _, f := range fs {
			if f.State == ferry.Sailing {
				d.mustFail(Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: lids(1, 1)})
				return
			}
		}
	case 1: // 停泊時間の途中の depart
		for _, f := range fs {
			if f.State == ferry.Docked && now < f.ReadyAt {
				nb := detNeighbors(e, f.At)
				d.mustFail(Command{Type: CmdFerryDepart, FerryID: string(f.ID), To: nb[r.IntN(len(nb))]})
				return
			}
		}
	}
	// 存在しない手紙の load（上の 2 つの場面がないときも、これを送る）。
	f := fs[r.IntN(len(fs))]
	d.mustFail(Command{Type: CmdFerryLoad, FerryID: string(f.ID), LetterIDs: []uint64{999999}})
}

// detRecord は、cfg・seed の Engine をボットで動かし、操作と結果を記録する。
// withPropose が偽なら提案をしない（入力ログだけで再生できる実行になる）。
func detRecord(t *testing.T, cfg Config, seed uint64, withPropose bool) detRun {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := &detRecorder{t: t, e: e, r: rand.New(rand.NewPCG(seed, 0xde7))}
	if ack := d.apply(Command{Type: CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	r := d.r
	// avoidUntil まで、Leader の島 avoid に船を向かわせない。Leader を孤立させて、交代の場面を作る。
	var avoid uint64
	var avoidUntil SimTime
	for e.Phase() == PhaseSailing {
		now := int64(e.Now())
		if e.Now() >= avoidUntil {
			avoid = 0
			// 20 秒より後に、1 ラウンドに 1 回まで（それより前とほかの時間に、Commit の場面を残す）。
			if lead := detLeader(e); lead != 0 && avoidUntil == 0 && e.Now() >= 20000 && r.IntN(8) == 0 {
				avoid = lead
				avoidUntil = e.Now() + SimTime(65000+r.IntN(30000))
			}
		}
		for _, f := range e.tr.Ferries() {
			if f.State == ferry.Docked && f.ReadyAt <= now && r.IntN(2) == 0 {
				d.moveFerry(f, avoid)
			}
		}
		if withPropose && r.IntN(10) == 0 {
			d.proposeRandom(cfg)
		}
		if r.IntN(20) == 0 {
			d.unloadRandom()
		}
		if r.IntN(20) == 0 {
			d.failRandom()
		}
		e.RunUntil(e.Now() + SimTime(50+r.IntN(1950)))
		if r.IntN(3) == 0 {
			d.drain()
		}
	}
	d.drain()
	d.run.Hash = e.TraceHash()
	d.run.Input = e.InputLog()
	d.run.Now = e.Now()
	d.run.Phase = e.Phase()
	return d.run
}

// detAdvancer は「to まで時間を進める」方法 1 つ。Now() が to になるか、航行が終わるまで RunUntil を呼ぶ。
type detAdvancer func(e *Engine, to SimTime)

// detStrategy は時間の進め方 1 つと、再生の条件。
type detStrategy struct {
	name      string
	mk        func(cfg Config, seed uint64) detAdvancer // 状態を持つ進め方のため、再生のたびに作る
	opt       Options
	drainEach bool
}

func detJump(cfg Config, seed uint64) detAdvancer {
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			e.RunUntil(to)
		}
	}
}

func detOvershoot(cfg Config, seed uint64) detAdvancer {
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			if to == cfg.DurationMs {
				e.RunUntil(cfg.DurationMs + 60000) // 終わりを越える時刻を渡しても止まる
			} else {
				e.RunUntil(to)
			}
		}
	}
}

func detMs1(cfg Config, seed uint64) detAdvancer {
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			e.RunUntil(e.Now() + 1)
		}
	}
}

func detEvent(cfg Config, seed uint64) detAdvancer {
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			next := to
			if at, ok := e.nextEventAt(); ok && e.Now() < at && at < to {
				next = at
			}
			e.RunUntil(next)
		}
	}
}

// detFrameMs は実時間の 1 フレームの長さ（ms）。
const detFrameMs = 17

// detSpeed は速度 speedPermille（千分率）で、フレームごとに進める。
func detSpeed(speedPermille int64) func(cfg Config, seed uint64) detAdvancer {
	return func(cfg Config, seed uint64) detAdvancer {
		var acc int64
		return func(e *Engine, to SimTime) {
			for e.Phase() == PhaseSailing && e.Now() < to {
				acc += detFrameMs * speedPermille
				e.RunUntil(min(SimTime(acc/1000), to))
			}
		}
	}
}

// detSpeedSchedule は speedMixed の速度の切り替えの表（速度の千分率, フレームの数）。0 は一時停止。
var detSpeedSchedule = []struct {
	speed  int64
	frames int
}{
	{1000, 40}, {4000, 40}, {0, 10}, {500, 40}, {8000, 40}, {2000, 40},
}

func detSpeedMixed(cfg Config, seed uint64) detAdvancer {
	var acc int64
	idx, frame := 0, 0
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			acc += detFrameMs * detSpeedSchedule[idx].speed
			if frame++; frame == detSpeedSchedule[idx].frames {
				idx, frame = (idx+1)%len(detSpeedSchedule), 0
			}
			e.RunUntil(min(SimTime(acc/1000), to))
		}
	}
}

func detRandom(cfg Config, seed uint64) detAdvancer {
	r := rand.New(rand.NewPCG(seed, 0xad5))
	return func(e *Engine, to SimTime) {
		for e.Phase() == PhaseSailing && e.Now() < to {
			switch r.IntN(8) {
			case 0:
				e.RunUntil(e.Now())
			case 1:
				e.RunUntil(e.Now() - 1 - SimTime(r.IntN(100))) // 過去（負の時刻を含む）なので何もしない
			default:
				e.RunUntil(min(e.Now()+1+SimTime(r.IntN(3000)), to))
			}
		}
	}
}

// detStrategies は時間の進め方の表（map にしない。§9.2）。
var detStrategies = []detStrategy{
	{"jump", detJump, Options{}, false},
	{"overshoot", detOvershoot, Options{}, false},
	{"ms1", detMs1, Options{}, false},
	{"event", detEvent, Options{CheckInvariants: true}, true},
	{"speed500", detSpeed(500), Options{}, true},
	{"speed1000", detSpeed(1000), Options{}, true},
	{"speed2000", detSpeed(2000), Options{}, true},
	{"speed4000", detSpeed(4000), Options{}, true},
	{"speed8000", detSpeed(8000), Options{}, true},
	{"speedMixed", detSpeedMixed, Options{}, false},
	{"random", detRandom, Options{}, false},
}

// detStrategyNamed は名前で進め方を探す。
func detStrategyNamed(t *testing.T, name string) detStrategy {
	t.Helper()
	i := slices.IndexFunc(detStrategies, func(s detStrategy) bool { return s.name == name })
	if i < 0 {
		t.Fatalf("no strategy %q", name)
	}
	return detStrategies[i]
}

// detPlayer は記録した操作を 1 つずつ再生する（detReplay と DT-3 で使う）。
type detPlayer struct {
	t         *testing.T
	cfg       Config
	e         *Engine
	ops       []detOp
	next      int
	adv       detAdvancer
	drainEach bool
	run       detRun
}

func newDetPlayer(t *testing.T, cfg Config, seed uint64, ops []detOp, opt Options, adv detAdvancer, drainEach bool) *detPlayer {
	t.Helper()
	e, err := New(cfg, seed, opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &detPlayer{t: t, cfg: cfg, e: e, ops: ops, adv: adv, drainEach: drainEach, run: detRun{Ops: ops}}
}

func (p *detPlayer) maybeDrain() {
	if p.drainEach {
		p.run.Events = append(p.run.Events, p.e.DrainEvents()...)
	}
}

func (p *detPlayer) done() bool { return p.next >= len(p.ops) }

// step は次の操作を 1 つ再生する。
func (p *detPlayer) step() {
	t, e := p.t, p.e
	t.Helper()
	op := p.ops[p.next]
	p.next++
	p.adv(e, op.T)
	p.maybeDrain()
	if e.Now() != op.T {
		t.Fatalf("op %d: Now() = %d after advancing, want %d (phase %s)", p.next-1, e.Now(), op.T, e.Phase())
	}
	switch {
	case op.Cmd != nil:
		p.run.Acks = append(p.run.Acks, e.Apply(cloneCommand(*op.Cmd)))
	case op.Propose != nil:
		err := e.propose(op.Propose.Island, slices.Clone(op.Propose.Data))
		if err != nil && !errors.Is(err, raft.ErrProposalDropped) {
			t.Fatalf("op %d: propose(%d): %v", p.next-1, op.Propose.Island, err)
		}
		e.afterStep()
		p.run.PropErrs = append(p.run.PropErrs, err != nil)
	default:
		t.Fatalf("op %d: empty", p.next-1)
	}
	p.maybeDrain()
}

// finish は最後に DurationMs まで進め、結果を返す。
func (p *detPlayer) finish() detRun {
	p.adv(p.e, p.cfg.DurationMs)
	p.run.Events = append(p.run.Events, p.e.DrainEvents()...)
	p.run.Hash = p.e.TraceHash()
	p.run.Input = p.e.InputLog()
	p.run.Now = p.e.Now()
	p.run.Phase = p.e.Phase()
	return p.run
}

// detReplay は、記録した ops を新しい Engine で再生する。
// 各操作の前に adv(e, op.T)、最後に adv(e, cfg.DurationMs) を呼ぶ。drainEach なら、各操作と各 adv の後に DrainEvents する。
func detReplay(t *testing.T, cfg Config, seed uint64, ops []detOp, opt Options, adv detAdvancer, drainEach bool) detRun {
	t.Helper()
	p := newDetPlayer(t, cfg, seed, ops, opt, adv, drainEach)
	for !p.done() {
		p.step()
	}
	return p.finish()
}

// detDiff は 2 つのイベントの列の最初の違いを説明する。同じなら ""。
func detDiff(a, b []DomainEvent) string {
	for i := range min(len(a), len(b)) {
		if !reflect.DeepEqual(a[i], b[i]) {
			return fmt.Sprintf("first difference at index %d:\n  a: Seq=%d T=%d Type=%s Data=%+v\n  b: Seq=%d T=%d Type=%s Data=%+v",
				i, a[i].Seq, a[i].T, a[i].Type, a[i].Data, b[i].Seq, b[i].T, b[i].Type, b[i].Data)
		}
	}
	if len(a) != len(b) {
		i := min(len(a), len(b))
		var extra DomainEvent
		side := "a"
		if len(a) > len(b) {
			extra = a[i]
		} else {
			extra, side = b[i], "b"
		}
		return fmt.Sprintf("lengths differ: %d vs %d; first extra event in %s at index %d: Seq=%d T=%d Type=%s Data=%+v",
			len(a), len(b), side, i, extra.Seq, extra.T, extra.Type, extra.Data)
	}
	return ""
}

// detCompareTrace は、操作の結果を除く「比べるもの」（Events・Hash・Input・Now・Phase）を比べる。
func detCompareTrace(t *testing.T, label string, want, got detRun) {
	t.Helper()
	if d := detDiff(want.Events, got.Events); d != "" {
		t.Errorf("%s: events differ (%d vs %d): %s", label, len(want.Events), len(got.Events), d)
	}
	if want.Hash != got.Hash {
		t.Errorf("%s: TraceHash %x, want %x", label, got.Hash, want.Hash)
	}
	if !reflect.DeepEqual(want.Input, got.Input) {
		t.Errorf("%s: InputLog differs (%d vs %d entries)", label, len(want.Input), len(got.Input))
	}
	if want.Now != got.Now || want.Phase != got.Phase {
		t.Errorf("%s: end at (%d, %s), want (%d, %s)", label, got.Now, got.Phase, want.Now, want.Phase)
	}
}

// detCompare は「比べるもの」のすべてを比べる。
func detCompare(t *testing.T, label string, want, got detRun) {
	t.Helper()
	detCompareTrace(t, label, want, got)
	if !reflect.DeepEqual(want.Acks, got.Acks) {
		for i := range min(len(want.Acks), len(got.Acks)) {
			if want.Acks[i] != got.Acks[i] {
				t.Errorf("%s: ack %d = %+v, want %+v", label, i, got.Acks[i], want.Acks[i])
				break
			}
		}
		t.Errorf("%s: acks differ (%d vs %d)", label, len(want.Acks), len(got.Acks))
	}
	if !reflect.DeepEqual(want.PropErrs, got.PropErrs) {
		t.Errorf("%s: proposal results differ: %v, want %v", label, got.PropErrs, want.PropErrs)
	}
}

// detCheckEnd は記録がラウンドの終わりまで進んだことを確かめる。
func detCheckEnd(t *testing.T, cfg Config, rec detRun) {
	t.Helper()
	if rec.Now != cfg.DurationMs || rec.Phase != PhaseEnded {
		t.Fatalf("record ended at (%d, %s), want (%d, %s)", rec.Now, rec.Phase, cfg.DurationMs, PhaseEnded)
	}
}

func TestDeterminismInputLogReplay(t *testing.T) { // DT-1
	t.Parallel()
	for _, c := range detCases(6, 6) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := detRecord(t, c.cfg, c.seed, false)
			detCheckEnd(t, c.cfg, rec)
			if sha256OfTrace(t, rec.Events) != rec.Hash {
				t.Error("TraceHash does not match the definition (§10.2)")
			}
			if len(rec.Acks) <= len(rec.Input) {
				t.Errorf("no failed command in the record (%d acks, %d inputs)", len(rec.Acks), len(rec.Input))
			}
			if len(rec.PropErrs) != 0 {
				t.Errorf("record without proposals has %d proposals", len(rec.PropErrs))
			}

			e, err := New(c.cfg, c.seed, Options{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for i, in := range rec.Input {
				e.RunUntil(in.T)
				ack := e.Apply(cloneCommand(in.Cmd))
				if !ack.OK || ack.T != in.T {
					t.Fatalf("input %d (%+v at %d): ack %+v", i, in.Cmd, in.T, ack)
				}
			}
			e.RunUntil(c.cfg.DurationMs)
			got := detRun{
				Events: e.DrainEvents(),
				Hash:   e.TraceHash(),
				Input:  e.InputLog(),
				Now:    e.Now(),
				Phase:  e.Phase(),
			}
			detCompareTrace(t, "input log replay", rec, got)
		})
	}
}

func TestDeterminismAdvanceStrategies(t *testing.T) { // DT-2
	t.Parallel()
	// Issue #28 の表の進め方は、jump・overshoot・ms1・event・speed500〜speed8000（5 通り）・speedMixed・random の 11 通り。
	if len(detStrategies) != 11 {
		t.Fatalf("detStrategies has %d entries, want 11", len(detStrategies))
	}
	for _, c := range detCases(6, 4) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := detRecord(t, c.cfg, c.seed, true)
			detCheckEnd(t, c.cfg, rec)
			for _, s := range detStrategies {
				got := detReplay(t, c.cfg, c.seed, rec.Ops, s.opt, s.mk(c.cfg, c.seed), s.drainEach)
				detCompare(t, s.name, rec, got)
			}
		})
	}
}

func TestDeterminismInterleaved(t *testing.T) { // DT-3
	t.Parallel()
	for s := uint64(1); s <= 3; s++ {
		t.Run(fmt.Sprintf("3islands/seed%d", s), func(t *testing.T) {
			t.Parallel()
			cfg := detConfig3()
			a := detRecord(t, cfg, s, true)
			c := detRecord(t, cfg, s+100, true)
			mk := func(name string, seed uint64, ops []detOp) *detPlayer {
				st := detStrategyNamed(t, name)
				return newDetPlayer(t, cfg, seed, ops, st.opt, st.mk(cfg, seed), st.drainEach)
			}
			players := []*detPlayer{
				mk("jump", s, a.Ops),       // ea
				mk("speed2000", s, a.Ops),  // eb
				mk("random", s+100, c.Ops), // ec
			}
			for slices.ContainsFunc(players, func(p *detPlayer) bool { return !p.done() }) {
				for _, p := range players {
					if !p.done() {
						p.step()
					}
				}
			}
			detCompare(t, "ea (jump)", a, players[0].finish())
			detCompare(t, "eb (speed2000)", a, players[1].finish())
			detCompare(t, "ec (random)", c, players[2].finish())
		})
	}
}

// detCoverage は 1 つの記録に含まれる場面。
type detCoverage struct {
	leaderChange, committed, dropped, delivered, unloaded bool
}

func TestDeterminismCoverage(t *testing.T) { // DT-4
	t.Parallel()
	cases := detCases(6, 4)
	// 各サブテストは自分の要素だけに書く。
	hashes := make([][32]byte, len(cases))
	covs := make([]detCoverage, len(cases))
	t.Run("records", func(t *testing.T) {
		for i, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				rec := detRecord(t, c.cfg, c.seed, true)
				detCheckEnd(t, c.cfg, rec)
				hashes[i] = rec.Hash
				if n := len(rec.Events); n < 100 {
					t.Errorf("only %d events", n)
				}
				if len(rec.Events) > 0 {
					last := rec.Events[len(rec.Events)-1]
					if pc, ok := last.Data.(PhaseChanged); !ok || pc.Phase != PhaseEnded || last.T != c.cfg.DurationMs {
						t.Errorf("last event is %s %+v at %d, want phase.changed{ended} at %d", last.Type, last.Data, last.T, c.cfg.DurationMs)
					}
				}
				failed := 0
				for _, a := range rec.Acks {
					if !a.OK {
						failed++
					}
				}
				if failed == 0 {
					t.Error("no failed ack")
				}
				var cov detCoverage
				sailing := slices.IndexFunc(rec.Events, func(ev DomainEvent) bool {
					pc, ok := ev.Data.(PhaseChanged)
					return ok && pc.Phase == PhaseSailing
				})
				if sailing < 0 {
					t.Fatal("no phase.changed{sailing}")
				}
				for _, le := range ofType[LeaderElected](rec.Events[sailing+1:]) {
					cov.leaderChange = cov.leaderChange || le.Term > 2
				}
				for _, la := range ofType[LedgerApplied](rec.Events) {
					cov.committed = cov.committed || la.Result == ledger.StatusOK
				}
				cov.dropped = len(ofType[LetterDropped](rec.Events)) > 0
				for _, ld := range ofType[LetterDelivered](rec.Events) {
					cov.delivered = cov.delivered || ld.Accepted
				}
				for j, op := range rec.Ops {
					if op.Cmd != nil && op.Cmd.Type == CmdFerryUnload {
						// Ops と Acks は、コマンドについて同じ順に並ぶ。
						k := 0
						for _, o := range rec.Ops[:j] {
							if o.Cmd != nil {
								k++
							}
						}
						cov.unloaded = cov.unloaded || rec.Acks[k].OK
					}
				}
				covs[i] = cov
				t.Logf("%s: events=%d ops=%d failed=%d coverage=%+v", c.name, len(rec.Events), len(rec.Ops), failed, cov)
			})
		}
	})
	if t.Failed() {
		return
	}
	for i := range hashes {
		for j := range i {
			if hashes[i] == hashes[j] {
				t.Errorf("%s and %s have the same TraceHash", cases[i].name, cases[j].name)
			}
		}
	}
	var all detCoverage
	for _, c := range covs {
		all.leaderChange = all.leaderChange || c.leaderChange
		all.committed = all.committed || c.committed
		all.dropped = all.dropped || c.dropped
		all.delivered = all.delivered || c.delivered
		all.unloaded = all.unloaded || c.unloaded
	}
	if !all.leaderChange {
		t.Error("no node.leader_elected with Term > 2 after the opening")
	}
	if !all.committed {
		t.Error("no ledger.applied with result ok")
	}
	if !all.dropped {
		t.Error("no letter.dropped")
	}
	if !all.delivered {
		t.Error("no accepted letter.delivered")
	}
	if !all.unloaded {
		t.Error("no successful cmd.ferry.unload")
	}
}
