package sim

import (
	"fmt"
	"slices"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// RaftConfig は raft.Config に渡す値（データ仕様の raft.*）。既定値を持たない。
type RaftConfig struct {
	TickMs          SimTime // RaftTick の間隔（raft.tickMs）
	HeartbeatTick   int
	ElectionTick    int
	MaxSizePerMsg   uint64
	MaxInflightMsgs int
}

// Config は 1 ラウンドの設定。値はすべてステージ定義（T-201）から渡す。このパッケージに既定値を持たない。
type Config struct {
	Islands       int     // 島（= ノード）の数 N。ID は 1..N（ステージ定義の islands の順）
	InitialLeader uint64  // 開会式で立候補させる島（1..N）
	DurationMs    SimTime // ラウンドの長さ（durationMs）。この時刻に航行が終わる
	Raft          RaftConfig
	Routes        []ferry.RouteConfig
	Storms        []game.StormDef     // 台本の嵐（events の kind: storm、書いた順）。nil は嵐なし
	Breakdowns    []game.BreakdownDef // 台本の船の故障（events の kind: breakdown、書いた順）。nil は故障なし
	Ferries       []ferry.FerryConfig
	DockMs        int64                // transport.dockMs
	PortCap       int                  // transport.portCap
	PerLinkAppCap int                  // transport.perLinkAppCap
	Resources     []ledger.ResourceDef // 台帳の資源（各ノードに同じ定義で 1 つずつ作る）
	Requests      game.RequestConfig   // 依頼（requests.*）。ゼロ値は依頼なし
	Score         game.ScoreConfig     // スコア・満足度・クリア判定（scoring.*・satisfaction.*）。ゼロ値は採点しない
	DemoStorm     *DemoStorm           // デモの嵐（demo.storm）。nil = ステージに書いていない（demo.trigger は bad_request）
}

// Options はゲームの結果に影響しない動作の切り替え。
type Options struct {
	CheckInvariants bool // テストモード（§11）。イベントの処理ごとに検査し、違反したら panic する
}

// Phase はラウンドのフェーズ（FR-GAME-02）。
type Phase string

const (
	PhaseIntro   Phase = "intro"   // New の直後（ステージ紹介）。cmd.game.start を待つ
	PhaseOpening Phase = "opening" // 開会式。Start の処理の中だけ（外からは phase.changed でだけ見える）
	PhaseSailing Phase = "sailing" // 航行
	PhaseEnded   Phase = "ended"   // 終了（DurationMs に達した）
)

// スケジューラのイベントの種類。
const (
	kindRaftTick     EventKind = iota + 1 // ClassRaftTick。Data なし
	kindFerryArrive                       // ClassFerryArrive。Data は ferry.FerryID
	kindFerryReady                        // ClassFerryReady。Data は ferry.FerryID
	kindRoundEnd                          // ClassGameTimer。Data なし
	kindRequestSpawn                      // ClassRequestSpawn。Data は int（帳簿の添字）
	kindRequestRetry                      // ClassRequestRetry。Data は retryTimer
)

// nodeView は node.state に出す値（最後に出した値と比べる）。
type nodeView struct {
	role                                         raft.StateType
	term, vote, lead, commit, applied, lastIndex uint64
}

// Engine は 1 ゲーム分の世界（DesignDoc §4.3）。I/O・goroutine・実時間を使わずに動く。
// 外から動かす方法は Apply（コマンド）と RunUntil（時間）だけで、出力はドメインイベントの列（DrainEvents）と
// そのハッシュ（TraceHash）。状態を変えない観察として Snapshot と NextNotableAt がある。
// 1 つの goroutine から使う（ADR-0003）。
//
// Engine の誤り（Process・PostRaft・Arrive・AdvanceTo・Push のエラー、表にない ferry のエラー、
// 台帳の結果の型の違い、テストモードの不変条件の違反）は panic する。正しく使えば起きない。
type Engine struct {
	cfg Config
	opt Options
	inv *invariantState // テストモードの検査が覚えておく値（invariants.go）。テストモードでなければ nil

	sched      Scheduler
	nodes      []*cluster.Node  // 添字 = ID - 1
	ledgers    []*ledger.Ledger // 添字 = ID - 1
	tr         *ferry.Transport
	storms     stormBook     // 嵐の帳簿（storm.go）
	breakdowns breakdownBook // 船の故障の帳簿（breakdown.go）
	reqs       requestBook   // 依頼の帳簿（request.go）
	score      scoreBook     // 採点の帳簿（score.go）
	stats      statsBook     // 統計と「今日の出来事」の帳簿（stats.go）

	phase   Phase
	opening cluster.OpeningResult

	last       []nodeView // node.state を最後に出した値。添字 = ID - 1
	leaderTerm []uint64   // Leader になったときの Term。添字 = ID - 1
	staleTerm  []uint64   // 送信元の手紙を古いと判定した基準の Term（letter.stale）。添字 = ID - 1
	contactAt  []SimTime  // 最後に Leader から手紙を受け取った時刻（NodeDetail の選挙の推定。観察だけ）。添字 = ID - 1

	seq     uint64        // 最後に振ったイベントの連番
	pending []DomainEvent // まだ取り出していないイベント
	trace   traceHash
	input   []InputEntry
}

// New は cfg と seed から Engine を作り、開会式（§5.3）まで計算する。
// 開会式のイベントは cmd.game.start を適用したときに出す。Phase() は PhaseIntro、Now() は 0。
func New(cfg Config, seed uint64, opt Options) (*Engine, error) {
	switch {
	case cfg.Islands < 1:
		return nil, fmt.Errorf("sim: islands %d < 1", cfg.Islands)
	case cfg.InitialLeader < 1 || cfg.InitialLeader > uint64(cfg.Islands):
		return nil, fmt.Errorf("sim: initial leader %d is not in 1..%d", cfg.InitialLeader, cfg.Islands)
	case cfg.DurationMs < 1:
		return nil, fmt.Errorf("sim: durationMs %d < 1", cfg.DurationMs)
	case cfg.Raft.TickMs < 1:
		return nil, fmt.Errorf("sim: raft.tickMs %d < 1", cfg.Raft.TickMs)
	}
	cfg.Routes = slices.Clone(cfg.Routes)
	cfg.Ferries = slices.Clone(cfg.Ferries)
	cfg.Resources = slices.Clone(cfg.Resources)

	n := cfg.Islands
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}

	e := &Engine{
		cfg:        cfg,
		opt:        opt,
		nodes:      make([]*cluster.Node, n),
		ledgers:    make([]*ledger.Ledger, n),
		phase:      PhaseIntro,
		last:       make([]nodeView, n),
		leaderTerm: make([]uint64, n),
		staleTerm:  make([]uint64, n),
		contactAt:  make([]SimTime, n),
		trace:      newTraceHash(),
	}
	for i, id := range ids {
		l, err := ledger.New(cfg.Resources)
		if err != nil {
			return nil, fmt.Errorf("sim: island %d: %w", id, err)
		}
		e.ledgers[i] = l
	}
	for i, id := range ids {
		node, err := cluster.NewNode(cluster.NodeConfig{
			ID:              id,
			Peers:           ids,
			ElectionTick:    cfg.Raft.ElectionTick,
			HeartbeatTick:   cfg.Raft.HeartbeatTick,
			MaxSizePerMsg:   cfg.Raft.MaxSizePerMsg,
			MaxInflightMsgs: cfg.Raft.MaxInflightMsgs,
			RandIntn:        RaftRandIntn(seed, id),
			StateMachine:    ledgerSM{l: e.ledgers[i]},
		})
		if err != nil {
			return nil, fmt.Errorf("sim: %w", err)
		}
		e.nodes[i] = node
	}
	tr, err := ferry.New(ferry.Config{
		Islands:       ids,
		Routes:        cfg.Routes,
		Ferries:       cfg.Ferries,
		DockMs:        cfg.DockMs,
		PortCap:       cfg.PortCap,
		PerLinkAppCap: cfg.PerLinkAppCap,
	})
	if err != nil {
		return nil, fmt.Errorf("sim: %w", err)
	}
	e.tr = tr
	if err := e.initStorms(seed); err != nil {
		return nil, err
	}
	if err := e.initBreakdowns(seed); err != nil {
		return nil, err
	}
	if err := e.initDemo(); err != nil {
		return nil, err
	}
	e.cfg.Requests = cfg.Requests.Clone()
	plan, err := game.PlanRequests(e.cfg.Requests, cfg.Islands, int64(cfg.DurationMs), e.cfg.Resources, Sub(seed, StreamRequests))
	if err != nil {
		return nil, fmt.Errorf("sim: requests: %w", err)
	}
	e.reqs = newRequestBook(plan, e.cfg.Requests.RetryMs)
	if err := e.initScore(); err != nil {
		return nil, err
	}
	e.initStats()
	res, err := cluster.Opening(e.nodes, cfg.InitialLeader)
	if err != nil {
		return nil, fmt.Errorf("sim: %w", err)
	}
	e.opening = res
	return e, nil
}

// Now はゲーム内時刻を返す。
func (e *Engine) Now() SimTime { return e.sched.Now() }

// Phase は今のフェーズを返す。
func (e *Engine) Phase() Phase { return e.phase }

// Apply はコマンドを Now() の時刻で適用する。成功したコマンドだけを入力ログに記録する。
// 失敗したときは、状態・イベント・入力ログ・時刻のどれも変えない。
func (e *Engine) Apply(cmd Command) Ack {
	switch cmd.Type {
	case CmdGameStart:
		if e.phase != PhaseIntro {
			return e.fail(CodePhaseInvalid, fmt.Sprintf("sim: %s in phase %s", cmd.Type, e.phase))
		}
		e.start()
	case CmdFerryLoad, CmdFerryUnload, CmdFerryDepart, CmdFerryAutoload:
		if e.phase != PhaseSailing {
			return e.fail(CodePhaseInvalid, fmt.Sprintf("sim: %s in phase %s", cmd.Type, e.phase))
		}
		if err := e.applyFerry(cmd); err != nil {
			return e.fail(ferryErrCode(err), err.Error())
		}
		e.afterStep()
	case CmdDemoTrigger:
		if code, msg := e.applyDemo(cmd); code != "" {
			return e.fail(code, msg)
		}
		e.afterStep()
	default:
		return e.fail(CodeBadRequest, fmt.Sprintf("sim: unknown command type %q", cmd.Type))
	}
	e.input = append(e.input, InputEntry{T: e.Now(), Cmd: cloneCommand(cmd)})
	return Ack{OK: true, T: e.Now()}
}

func (e *Engine) fail(code, msg string) Ack {
	return Ack{OK: false, T: e.Now(), Code: code, Message: msg}
}

// start は cmd.game.start（開会式と航行の開始）。時刻は 0 のまま。後処理はこの中で 1 回だけ行う。
func (e *Engine) start() {
	e.phase = PhaseOpening
	e.emit(PhaseChanged{Phase: PhaseOpening})
	for _, rc := range e.opening.Events {
		e.handleRoleChange(rc)
	}
	// 即時配達した手紙（e.opening.Delivered）は港を通らず ID もないので、手紙のイベントにしない。
	e.afterStep()
	e.phase = PhaseSailing
	e.emit(PhaseChanged{Phase: PhaseSailing})
	e.startStorms()
	e.startBreakdowns()
	e.push(e.cfg.Raft.TickMs, ClassRaftTick, kindRaftTick, nil)
	e.push(e.cfg.DurationMs, ClassGameTimer, kindRoundEnd, nil)
	e.scheduleRequests()
	e.startScore()
}

// applyFerry は cmd.ferry.* を適用し、成功したらイベントを出す。失敗したら何も変えずに ferry のエラーを返す。
func (e *Engine) applyFerry(cmd Command) error {
	id := ferry.FerryID(cmd.FerryID)
	switch cmd.Type {
	case CmdFerryLoad:
		if err := e.tr.Load(id, toLetterIDs(cmd.LetterIDs)); err != nil {
			return err
		}
		for _, lid := range cmd.LetterIDs {
			e.emit(LetterMoved{LetterID: lid, Loc: ferryLoc(id)})
		}
	case CmdFerryUnload:
		dropped, err := e.tr.Unload(id, toLetterIDs(cmd.LetterIDs))
		if err != nil {
			return err
		}
		f, _ := e.tr.Ferry(id)
		sorted := slices.Clone(cmd.LetterIDs)
		slices.Sort(sorted)
		for _, lid := range sorted {
			e.emit(LetterMoved{LetterID: lid, Loc: portLoc(f.At)})
		}
		e.emitDrops(dropped)
	case CmdFerryDepart:
		before, _ := e.tr.Ferry(id)
		arriveAt, err := e.tr.Depart(int64(e.Now()), id, cmd.To)
		if err != nil {
			return err
		}
		e.emit(FerryDeparted{
			FerryID:  cmd.FerryID,
			From:     before.At,
			To:       cmd.To,
			DepartAt: e.Now(),
			ArriveAt: SimTime(arriveAt),
		})
		// DurationMs より後でも積む（ラウンドが先に終わるので処理されない）。
		e.push(SimTime(arriveAt), ClassFerryArrive, kindFerryArrive, id)
		e.chargeVoyage(SimTime(arriveAt) - e.Now())
	case CmdFerryAutoload:
		return e.autoload(id, cmd.To)
	}
	return nil
}

// RunUntil は t 以下の時刻のイベントをすべて処理し、Now() を t にする。
// 航行中でなければ何もしない。t が DurationMs を超えていても DurationMs で止まり、ラウンドを終える。
// t < Now() なら何もしない。
func (e *Engine) RunUntil(t SimTime) {
	if e.phase != PhaseSailing {
		return
	}
	target := min(t, e.cfg.DurationMs)
	if target < e.Now() {
		return
	}
	for {
		ev, ok := e.sched.PopUntil(target)
		if !ok {
			break
		}
		e.handleEvent(ev)
		e.afterStep()
		if ev.Kind == kindRoundEnd {
			return
		}
	}
	if err := e.sched.AdvanceTo(target); err != nil {
		panic(fmt.Sprintf("sim: advance to %d: %v", target, err))
	}
}

// DrainEvents は前回呼んでから出たドメインイベントを取り出す。ほかの状態は変えない。
// 呼ばなければイベントは溜まり続ける（呼び出し側の責任）。
func (e *Engine) DrainEvents() []DomainEvent {
	out := e.pending
	e.pending = nil
	return out
}

// InputLog は入力ログ（成功したコマンドと適用した時刻）の深い複製を返す。
func (e *Engine) InputLog() []InputEntry {
	out := make([]InputEntry, len(e.input))
	for i, in := range e.input {
		out[i] = InputEntry{T: in.T, Cmd: cloneCommand(in.Cmd)}
	}
	return out
}

// TraceHash は New からの全イベントのトレースのハッシュ（§10.2）を返す。状態は変えない。
func (e *Engine) TraceHash() [32]byte { return e.trace.sum() }

// push はスケジューラにイベントを積む。エラーは Engine の誤りなので panic する。
func (e *Engine) push(at SimTime, class EventClass, kind EventKind, data any) {
	if _, err := e.sched.Push(at, class, kind, data); err != nil {
		panic(fmt.Sprintf("sim: push kind %d at %d: %v", kind, at, err))
	}
}

// emit はドメインイベントを 1 件出す。イベントを作るのはここだけ。
func (e *Engine) emit(d EventData) {
	e.seq++
	ev := DomainEvent{Seq: e.seq, T: e.Now(), Type: d.EventType(), Data: d}
	e.pending = append(e.pending, ev)
	e.trace.write(ev)
	e.observeStats(ev)
}

func (e *Engine) emitDrops(drops []ferry.Drop) {
	for _, d := range drops {
		e.emit(LetterDropped{LetterID: uint64(d.Letter), Reason: string(d.Reason)})
	}
}

// handleEvent はスケジューラのイベント 1 つを処理する（後処理は呼び出し側）。
func (e *Engine) handleEvent(ev Event) {
	switch ev.Kind {
	case kindRaftTick:
		for _, n := range e.nodes {
			n.Tick()
		}
		e.push(e.Now()+e.cfg.Raft.TickMs, ClassRaftTick, kindRaftTick, nil)
		e.retryWaits()
	case kindStormForecast, kindStormStart, kindStormEnd:
		e.handleStorm(ev)
	case kindBreakdownStart, kindBreakdownRepair:
		e.handleBreakdown(ev)
	case kindFerryArrive:
		e.arrive(ev.Data.(ferry.FerryID))
	case kindFerryReady:
		// 何もしない（プロトコル §6 に該当のイベントがない）。NextNotableAt の対象ではない（ドキュメントの出来事の一覧にないため）。
		// ボット（T-109）などが ReadyAt を使う。
	case kindRequestDeadline, kindSilenceCheck:
		e.handleScoreTimer(ev)
	case kindRoundEnd:
		e.finishScore()
		e.phase = PhaseEnded
		e.emit(PhaseChanged{Phase: PhaseEnded})
	case kindRequestSpawn:
		e.spawnRequest(ev.Data.(int))
	case kindRequestRetry:
		e.retryRequest(ev.Data.(retryTimer))
	default:
		panic(fmt.Sprintf("sim: unknown event kind %d", ev.Kind))
	}
}

// arrive は船の到着を処理し、その島宛ての手紙を積んだ順に 1 通ずつ配達する（§6.3）。
func (e *Engine) arrive(id ferry.FerryID) {
	delivered, err := e.tr.Arrive(int64(e.Now()), id)
	if err != nil {
		panic(fmt.Sprintf("sim: arrive %q: %v", id, err))
	}
	f, _ := e.tr.Ferry(id)
	e.emit(FerryArrived{FerryID: string(id), IslandID: f.At, ReadyAt: SimTime(f.ReadyAt)})
	e.breakOnArrival(id)
	e.push(SimTime(f.ReadyAt), ClassFerryReady, kindFerryReady, id)
	e.noteDelivered(f.At, len(delivered))
	for _, lid := range delivered {
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: delivered letter %d not found", lid))
		}
		if l.Kind != ferry.LetterRaft {
			// 依頼書。届いた島の役場に渡す（request.go）。
			e.emit(LetterDelivered{LetterID: uint64(lid), IslandID: l.To, Accepted: true})
			e.deliverRequest(l)
			continue
		}
		node := e.nodes[l.To-1]
		// ferry が持つメッセージを Raft に触らせないよう、複製を渡す。
		derr := node.Deliver(proto.Clone(l.Raft).(*pb.Message))
		e.emit(LetterDelivered{LetterID: uint64(lid), IslandID: l.To, Accepted: derr == nil})
		// 返事がすぐにその島の港に並び、同じ船で持ち帰れるようにする。
		out, err := node.Process()
		if err != nil {
			panic(fmt.Sprintf("sim: node %d: process: %v", l.To, err))
		}
		e.noteLeaderContact(l.To, l.From)
		e.handleOutput(l.To, out)
	}
}

// afterStep は 1 ステップ（成功した Apply・スケジューラのイベント 1 つ）の後処理。
func (e *Engine) afterStep() {
	for i, n := range e.nodes {
		out, err := n.Process()
		if err != nil {
			panic(fmt.Sprintf("sim: node %d: process: %v", i+1, err))
		}
		e.handleOutput(uint64(i+1), out)
	}
	for i, n := range e.nodes {
		st := n.Status()
		v := nodeView{
			role:      st.Role,
			term:      st.Term,
			vote:      st.Vote,
			lead:      st.Lead,
			commit:    st.Commit,
			applied:   st.Applied,
			lastIndex: st.LastIndex,
		}
		if v == e.last[i] {
			continue
		}
		e.last[i] = v
		e.emit(NodeState{
			ID:        st.ID,
			Role:      roleString(v.role),
			Term:      v.term,
			Vote:      v.vote,
			Lead:      v.lead,
			Commit:    v.commit,
			Applied:   v.applied,
			LastIndex: v.lastIndex,
		})
	}
	e.refreshRequests()
	e.refreshScore()
	// 配達・破棄した手紙のイベントを作り終えたので、ここで手放す（#103）。
	e.tr.Reclaim()
	if e.opt.CheckInvariants {
		e.checkInvariants()
	}
}

// handleOutput は 1 ノードの Process の出力をイベントに変える。
func (e *Engine) handleOutput(id uint64, out cluster.Output) {
	for _, rc := range out.Events {
		e.handleRoleChange(rc)
	}
	e.emitStale(id)
	for _, m := range out.Messages {
		created, dropped, err := e.tr.PostRaft(int64(e.Now()), id, []*pb.Message{m})
		if err != nil {
			panic(fmt.Sprintf("sim: node %d: post: %v", id, err))
		}
		term := e.nodes[id-1].Status().Term
		for _, lid := range created {
			l, ok := e.tr.Letter(lid)
			if !ok {
				panic(fmt.Sprintf("sim: created letter %d not found", lid))
			}
			e.emit(LetterCreated{Letter: newLetterView(l, term)})
		}
		e.emitDrops(dropped)
	}
	for _, a := range out.Applied {
		r := ledgerResult(a.Result)
		e.emit(LedgerApplied{IslandID: id, Index: a.Index, ReqID: r.ReqID, Result: r.Status, Reason: r.Reason})
		e.observeApplied(id, r)
	}
}

// handleRoleChange は役割の変化を node.leader_elected・node.stepped_down にする。
// そのほかの変化は node.state だけで表す。
func (e *Engine) handleRoleChange(rc cluster.RoleChange) {
	i := rc.Node - 1
	switch {
	case rc.Role == raft.StateLeader && rc.PrevRole != raft.StateLeader:
		e.emit(LeaderElected{ID: rc.Node, Term: rc.Term})
		e.leaderTerm[i] = rc.Term
	case rc.PrevRole == raft.StateLeader && rc.Role != raft.StateLeader:
		e.emit(SteppedDown{ID: rc.Node, Term: rc.Term, Reason: stepDownReason(e.leaderTerm[i], rc.Term)})
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

// propose は island のノードに data を提案する。Leader でなければ raft.ErrProposalDropped（包まない）。
// 後処理は呼ばない（呼び出し側のステップの後処理で、手紙と適用のイベントになる）。
func (e *Engine) propose(island uint64, data []byte) error {
	if island < 1 || island > uint64(len(e.nodes)) {
		return fmt.Errorf("sim: propose: unknown island %d", island)
	}
	return e.nodes[island-1].Propose(data)
}
