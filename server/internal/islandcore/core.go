package islandcore

import (
	"errors"
	"fmt"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// LetterIDBase は、島ごとの手紙の ID の桁（islandId × LetterIDBase + n、simulation-time.md §5.1）。
const LetterIDBase = 1_000_000_000

// Config は 1 島の核の設定。値はすべてステージ定義から渡す。このパッケージに既定値を持たない。
type Config struct {
	Island        uint64         // この島（= ノード）の ID（1..Islands）
	Islands       int            // 島の数 N
	TickMs        int64          // raft.tickMs（RaftTick の間隔）
	RetryMs       int64          // requests.retryMs（依頼の再送の間隔）
	PortCap       int            // transport.portCap
	PerLinkAppCap int            // transport.perLinkAppCap
	RelayCap      int            // transport.relayCap
	Requests      []game.Request // 依頼の計画（plan の順）。依頼の手紙を受けて route するのに使う

	// NewLetterID は手紙の ID を振る関数。nil なら島ごとの番号 Island × LetterIDBase + n（n は 1 から）。
	NewLetterID func() ferry.LetterID
	// NextSeq は待ち行列のイベントと待っている依頼に振る Seq の関数。nil なら島の中の連番。
	// 参照モデルは全島で 1 本の連番を渡し、同じ時刻・同じ種類のイベントの島をまたぐ順を今の Engine と同じにする。
	NextSeq func() uint64
}

// Wait は、代表が分からずに待っている route（次の RaftTick でやり直す）。
type Wait struct {
	Req  int    // 依頼の添字
	Hops int    // この島で依頼書を書くときの hops
	Seq  uint64 // 入れた順（Config.NextSeq）
}

// reqState は依頼 1 件の、この島の窓口の状態。
type reqState struct {
	req      game.Request
	created  bool   // 発生した（依頼元が発生させた・MarkCreated）
	resolved bool   // 依頼元の台帳の結果が出た（依頼元は自分で知る。ほかの島は MarkResolved）
	gen      uint64 // 再送のタイマーの世代（依頼元だけが使う）
}

// Core は 1 島の核。
type Core struct {
	id      uint64
	islands int
	tickMs  int64
	retryMs int64

	node   *cluster.Node
	ledger *ledger.Ledger
	port   *ferry.Port

	q       queue
	now     int64
	horizon Mark
	cur     Mark // 今の入力の時刻の印（出力に付ける）

	nextSeq func() uint64
	seq     uint64 // 島の中の Seq（Config.NextSeq が nil のとき）
	outSeq  uint64
	out     []Output
	letterN uint64 // 島で作った手紙の数（島ごとの ID）

	last         NodeStateOut // node.state を最後に出した値
	leaderTerm   uint64       // Leader になったときの Term
	reportedTerm uint64       // term.changed で最後に知らせた Term
	contactAt    int64        // 最後に Leader から手紙を受け取った時刻（観察だけ）

	reqs  []reqState
	byID  map[string]int // reqId → 添字。検索だけに使い、走査しない
	waits []Wait
}

// New は、開会式を済ませたノード node と、その状態機械の台帳 l から島の核を作る（Opening・OpeningAll で作る）。
func New(cfg Config, node *cluster.Node, l *ledger.Ledger) (*Core, error) {
	switch {
	case cfg.Islands < 1 || cfg.Island < 1 || cfg.Island > uint64(cfg.Islands):
		return nil, fmt.Errorf("islandcore: island %d is not in 1..%d", cfg.Island, cfg.Islands)
	case node == nil || l == nil:
		return nil, errors.New("islandcore: node or ledger is nil")
	case node.ID() != cfg.Island:
		return nil, fmt.Errorf("islandcore: node %d is not island %d", node.ID(), cfg.Island)
	case cfg.TickMs < 1:
		return nil, fmt.Errorf("islandcore: tickMs %d < 1", cfg.TickMs)
	}
	c := &Core{
		id:      cfg.Island,
		islands: cfg.Islands,
		tickMs:  cfg.TickMs,
		retryMs: cfg.RetryMs,
		node:    node,
		ledger:  l,
		nextSeq: cfg.NextSeq,
		reqs:    make([]reqState, len(cfg.Requests)),
		byID:    make(map[string]int, len(cfg.Requests)),
	}
	if c.nextSeq == nil {
		c.nextSeq = func() uint64 { c.seq++; return c.seq }
	}
	newID := cfg.NewLetterID
	if newID == nil {
		newID = func() ferry.LetterID {
			c.letterN++
			return ferry.LetterID(c.id*LetterIDBase + c.letterN)
		}
	}
	ids := make([]uint64, cfg.Islands)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	p, err := ferry.NewPort(ferry.PortConfig{
		Island:        cfg.Island,
		Islands:       ids,
		PortCap:       cfg.PortCap,
		PerLinkAppCap: cfg.PerLinkAppCap,
		RelayCap:      cfg.RelayCap,
		NewID:         newID,
	})
	if err != nil {
		return nil, fmt.Errorf("islandcore: %w", err)
	}
	c.port = p
	for i, r := range cfg.Requests {
		c.reqs[i] = reqState{req: r}
		c.byID[r.ID] = i
	}
	return c, nil
}

// ID は島の ID を返す。
func (c *Core) ID() uint64 { return c.id }

// Now は島の今の時刻（最後に処理した入力・イベントの時刻）を返す。
func (c *Core) Now() int64 { return c.now }

// Horizon は最後に AdvanceTo で受けた上限を返す。
func (c *Core) Horizon() Mark { return c.horizon }

// Node はノードを返す（観察だけに使う。NodeDetail・不変条件の検査）。
func (c *Core) Node() *cluster.Node { return c.node }

// Ledger は台帳を返す（観察だけに使う）。
func (c *Core) Ledger() *ledger.Ledger { return c.ledger }

// Port は港と中継倉庫を返す（観察だけに使う）。
func (c *Core) Port() *ferry.Port { return c.port }

// ContactAt は、最後に Leader から手紙を受け取った時刻を返す（NodeDetail の選挙の推定）。
func (c *Core) ContactAt() int64 { return c.contactAt }

// Drain は、前回呼んでからの出力を作った順に取り出す。
func (c *Core) Drain() []Output {
	out := c.out
	c.out = nil
	return out
}

func (c *Core) setNow(t int64) {
	if t < c.now {
		panic(fmt.Sprintf("islandcore: island %d: time goes backwards: %d < %d", c.id, t, c.now))
	}
	c.now = t
}

// begin は入力 1 つの処理を始める（時刻を進め、出力に付ける印を決める）。
func (c *Core) begin(t int64, class Class) {
	c.setNow(t)
	c.cur = Mark{T: t, Class: class}
}

func (c *Core) emit(typ string, data any) {
	c.outSeq++
	c.out = append(c.out, Output{T: c.cur.T, Class: c.cur.Class, Seq: c.outSeq, Type: typ, Data: data})
}

func (c *Core) emitDrops(drops []ferry.Drop) {
	for _, d := range drops {
		c.emit(OutLetterDropped, LetterDroppedOut{LetterID: d.Letter, Reason: d.Reason, Rule: d.Rule})
	}
}

// StartTicks は最初の RaftTick（時刻 t + TickMs）を積む（cmd.game.start）。
func (c *Core) StartTicks(t int64) {
	c.setNow(t)
	c.push(t+c.tickMs, ClassRaftTick, KindRaftTick, 0, 0)
}

// ScheduleSpawn は依頼 i の発生を待ち行列に積む（依頼元の島で、計画の順に呼ぶ）。
func (c *Core) ScheduleSpawn(i int) {
	r := c.reqs[i].req
	if r.Origin != c.id {
		panic(fmt.Sprintf("islandcore: island %d: schedule request %s of origin %d", c.id, r.ID, r.Origin))
	}
	c.push(r.CreatedAt, ClassRequestSpawn, KindRequestSpawn, i, 0)
}

// Start は StartTicks と、この島が依頼元の依頼すべての ScheduleSpawn（計画の順）を行う（分散版の島の Actor）。
func (c *Core) Start(t int64) {
	c.StartTicks(t)
	for i := range c.reqs {
		if c.reqs[i].req.Origin == c.id {
			c.ScheduleSpawn(i)
		}
	}
}

// AdvanceTo は、待ち行列のイベントのうち (At, Class) < h のものを (At, Class, Seq) の順にすべて処理する。
// イベントごとに Handle・Process・ReportState を行う。最後に島の時刻を h.T にする。
// h が前回の上限より小さければエラーで、何もしない（simulation-time.md §2.1）。
func (c *Core) AdvanceTo(h Mark) error {
	if h.Less(c.horizon) {
		return fmt.Errorf("islandcore: island %d: horizon %+v is before %+v", c.id, h, c.horizon)
	}
	if h.T < c.now {
		return fmt.Errorf("islandcore: island %d: horizon %+v is before now %d", c.id, h, c.now)
	}
	for {
		ev, ok := c.q.peek()
		if !ok || !ev.Mark().Less(h) {
			break
		}
		ev = c.Pop()
		c.Handle(ev)
		c.Process(ev.At, ev.Class)
		c.ReportState(ev.At, ev.Class)
	}
	c.horizon = h
	c.setNow(h.T)
	return nil
}

// Handle はイベント ev（Pop したもの）を処理する。Process・ReportState は呼ばない。
// RaftTick は HandleTick と、待っている依頼すべての RetryWait（入れた順）。
func (c *Core) Handle(ev Event) {
	switch ev.Kind {
	case KindRaftTick:
		c.HandleTick(ev)
		for _, w := range c.TakeWaits() {
			c.RetryWait(ev.At, w)
		}
	case KindRequestSpawn:
		c.begin(ev.At, ev.Class)
		r := &c.reqs[ev.Req]
		r.created = true
		c.route(ev.Req, 0)
	case KindRequestRetry:
		c.begin(ev.At, ev.Class)
		r := &c.reqs[ev.Req]
		if ev.Gen != r.gen || r.resolved {
			return
		}
		c.route(ev.Req, 0)
	default:
		panic(fmt.Sprintf("islandcore: island %d: unknown event kind %d", c.id, ev.Kind))
	}
}

// HandleTick は RaftTick ev のうち、ノードの Tick と次の RaftTick の登録だけを行う（参照モデルの部品）。
func (c *Core) HandleTick(ev Event) {
	if ev.Kind != KindRaftTick {
		panic(fmt.Sprintf("islandcore: island %d: HandleTick with kind %d", c.id, ev.Kind))
	}
	c.begin(ev.At, ev.Class)
	c.node.Tick()
	c.push(ev.At+c.tickMs, ClassRaftTick, KindRaftTick, 0, 0)
}

// TakeWaits は待っている route を入れた順に取り出して空にする（参照モデルの部品）。
func (c *Core) TakeWaits() []Wait {
	ws := c.waits
	c.waits = nil
	return ws
}

// Waits は待っている route の複製を返す（状態は変えない）。
func (c *Core) Waits() []Wait { return append([]Wait(nil), c.waits...) }

// RetryWait は、待っている route w を時刻 t（RaftTick）にやり直す（参照モデルの部品）。
func (c *Core) RetryWait(t int64, w Wait) {
	c.begin(t, ClassRaftTick)
	c.route(w.Req, w.Hops)
}

// Process は、ノードの Ready を処理して出力にする（入力がなければ何もしない）。
func (c *Core) Process(t int64, class Class) {
	c.begin(t, class)
	c.process()
}

func (c *Core) process() {
	out, err := c.node.Process()
	if err != nil {
		panic(fmt.Sprintf("islandcore: node %d: process: %v", c.id, err))
	}
	c.handleOutput(out)
}

// ReportState は、ノードの状態が最後に出した node.state から変わっていれば node.state を出す。
func (c *Core) ReportState(t int64, class Class) {
	c.begin(t, class)
	st := c.node.Status()
	v := NodeStateOut{
		Role:      st.Role,
		Term:      st.Term,
		Vote:      st.Vote,
		Lead:      st.Lead,
		Commit:    st.Commit,
		Applied:   st.Applied,
		LastIndex: st.LastIndex,
	}
	if v == c.last {
		return
	}
	c.last = v
	c.emit(OutNodeState, v)
}

// LastState は最後に出した node.state の値を返す。
func (c *Core) LastState() NodeStateOut { return c.last }

// ApplyRoleChange は、開会式の役割の変化 rc（自島のノードのもの）を時刻 t の出力にする（cmd.game.start）。
func (c *Core) ApplyRoleChange(t int64, rc cluster.RoleChange) {
	if rc.Node != c.id {
		panic(fmt.Sprintf("islandcore: island %d: role change of node %d", c.id, rc.Node))
	}
	c.begin(t, ClassStart)
	c.handleRoleChange(rc)
}

// handleOutput は Process の出力を島の出力にする。順は 役割の変化 → term.changed → 手紙 → 適用。
func (c *Core) handleOutput(out cluster.Output) {
	for _, rc := range out.Events {
		c.handleRoleChange(rc)
	}
	c.checkTerm()
	for _, m := range out.Messages {
		c.postRaft(m)
	}
	for _, a := range out.Applied {
		r, ok := a.Result.(ledger.Result)
		if !ok {
			panic(fmt.Sprintf("islandcore: applied result is %T, want ledger.Result", a.Result))
		}
		c.emit(OutLedgerApplied, LedgerAppliedOut{Index: a.Index, ReqID: r.ReqID, Result: r.Status, Reason: r.Reason})
		c.observeApplied(r)
	}
}

// checkTerm は、ノードの Term が最後に知らせた値より上がっていれば term.changed を出す。
func (c *Core) checkTerm() {
	if cur := c.node.Status().Term; cur > c.reportedTerm {
		c.reportedTerm = cur
		c.emit(OutTermChanged, TermChangedOut{Term: cur})
	}
}

// ReportedTerm は term.changed で最後に知らせた Term を返す。
func (c *Core) ReportedTerm() uint64 { return c.reportedTerm }

func (c *Core) handleRoleChange(rc cluster.RoleChange) {
	switch {
	case rc.Role == raft.StateLeader && rc.PrevRole != raft.StateLeader:
		c.emit(OutLeaderElected, LeaderElectedOut{Term: rc.Term})
		c.leaderTerm = rc.Term
	case rc.PrevRole == raft.StateLeader && rc.Role != raft.StateLeader:
		c.emit(OutSteppedDown, SteppedDownOut{Term: rc.Term, Reason: stepDownReason(c.leaderTerm, rc.Term)})
	}
}

// stepDownReason は Leader でなくなった理由の推定（プロトコル §6 の注）。
// Leader になったときより Term が上がっていれば higher_term、そうでなければ check_quorum。
func stepDownReason(leaderTerm, term uint64) string {
	if term > leaderTerm {
		return "higher_term"
	}
	return "check_quorum"
}

// postRaft は Raft のメッセージ m を港に置き、letter.created と港の規則による letter.dropped を出す。
func (c *Core) postRaft(m *pb.Message) {
	created, dropped, err := c.port.PostRaft(c.now, []*pb.Message{m})
	if err != nil {
		panic(fmt.Sprintf("islandcore: node %d: post: %v", c.id, err))
	}
	term := c.node.Status().Term
	for _, l := range created {
		c.emit(OutLetterCreated, LetterCreatedOut{Letter: l, SenderTerm: term, MsgTerm: l.Raft.GetTerm()})
	}
	c.emitDrops(dropped)
}

// Repost は、Raft のメッセージ m（自島のノードが送ったものの複製）を、時刻 t に港に置き直す
// （漂流瓶。参照モデルだけ。分散版は対象外）。先に Term の変化を知らせる。m は書き換えない。
func (c *Core) Repost(t int64, m *pb.Message) {
	c.begin(t, ClassWorldEvent)
	c.checkTerm()
	c.postRaft(m)
}

// Deliver は、時刻 t に船が届けた手紙 letters（宛先がこの島）を、この順に処理する
// （actor-protocol.md §3.1 の deliver。今の Engine の arrive の手紙ごとの処理）。
// Raft の手紙は Deliver → letter.delivered → Process（返事はすぐ港に並ぶ）、依頼書は letter.delivered → 窓口。
// 最後に Process と ReportState を行う（依頼書の Propose の Process は最後まで遅らせる）。
func (c *Core) Deliver(t int64, letters []ferry.Letter) {
	c.begin(t, ClassArrive)
	for _, l := range letters {
		if l.To != c.id {
			panic(fmt.Sprintf("islandcore: island %d: deliver letter %d to %d", c.id, l.ID, l.To))
		}
		if l.Kind != ferry.LetterRaft {
			c.emit(OutLetterDelivered, LetterDeliveredOut{LetterID: l.ID, Accepted: true})
			c.deliverRequest(l)
			continue
		}
		// 手紙が持つメッセージを Raft に触らせないよう、複製を渡す。
		derr := c.node.Deliver(proto.Clone(l.Raft).(*pb.Message))
		c.emit(OutLetterDelivered, LetterDeliveredOut{LetterID: l.ID, Accepted: derr == nil})
		c.process()
		if lead := c.node.Status().Lead; lead != 0 && lead == l.From {
			c.contactAt = c.now
		}
	}
	c.process()
	c.ReportState(t, ClassArrive)
}

// Unload は、時刻 t に船から降ろした手紙 letters（Fleet.Unload が返した、ID の昇順で所在つきのもの）を、この順に
// 港（自島が送信元。規則 R1〜R6 をかけ直す）か中継倉庫に入れる（actor-protocol.md §3.1 の unload）。
func (c *Core) Unload(t int64, letters []ferry.Letter) {
	c.begin(t, ClassCommand)
	for _, l := range letters {
		switch l.Location.Kind {
		case ferry.LocPort:
			ds, err := c.port.Return(l)
			if err != nil {
				panic(fmt.Sprintf("islandcore: island %d: unload: %v", c.id, err))
			}
			c.emitDrops(ds)
		case ferry.LocRelay:
			if err := c.port.Store(l); err != nil {
				panic(fmt.Sprintf("islandcore: island %d: unload: %v", c.id, err))
			}
		default:
			panic(fmt.Sprintf("islandcore: island %d: unload letter %d to %+v", c.id, l.ID, l.Location))
		}
	}
}

// Take は港の手紙 ids を渡した順に取り出す（船に積んだ。port.remove）。
func (c *Core) Take(ids []ferry.LetterID) ([]ferry.Letter, error) { return c.port.Take(ids) }

// TakeRelay は中継倉庫の手紙 ids を渡した順に取り出す（relay.remove）。
func (c *Core) TakeRelay(ids []ferry.LetterID) ([]ferry.Letter, error) { return c.port.TakeRelay(ids) }

// PostRequest は、依頼書を港に直接置く（テストが使う。窓口を通さない）。
func (c *Core) PostRequest(t int64, to uint64, note ferry.RequestNote) (ferry.Letter, []ferry.Drop, error) {
	c.begin(t, ClassCommand)
	l, ds, err := c.port.PostRequest(t, to, note)
	if err != nil {
		return ferry.Letter{}, nil, err
	}
	c.emit(OutLetterCreated, LetterCreatedOut{Letter: l, SenderTerm: c.node.Status().Term})
	c.emitDrops(ds)
	return l, ds, nil
}

// Propose はノードに data を提案する。Leader でなければ raft.ErrProposalDropped（包まない）。
// Process は呼ばない（呼び出し側の後処理で出力になる）。
func (c *Core) Propose(data []byte) error { return c.node.Propose(data) }
