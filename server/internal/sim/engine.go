package sim

import (
	"fmt"
	"slices"

	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/coordcore"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
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
	Storms        []game.StormDef        // 台本の嵐（events の kind: storm、書いた順）。nil は嵐なし
	Cancellations []game.CancellationDef // 欠航（events の kind: cancellation、書いた順）。nil は欠航なし
	Breakdowns    []game.BreakdownDef    // 台本の船の故障（events の kind: breakdown、書いた順）。nil は故障なし
	Rushes        []game.RushDef         // 台本の注文ラッシュ（events の kind: rush、書いた順）。nil はラッシュなし
	Drifts        []DriftDef             // 台本の漂流瓶（events の kind: drift、書いた順）。nil は漂流瓶なし
	Ferries       []ferry.FerryConfig
	DockMs        int64                // transport.dockMs
	PortCap       int                  // transport.portCap
	PerLinkAppCap int                  // transport.perLinkAppCap
	RelayCap      int                  // transport.relayCap（島ごとの中継倉庫の上限。0 = 積み替えなし）
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

// Coordinator のスケジューラのイベントの種類。島のイベント（RaftTick・依頼の発生・再送）は各島の
// islandcore.Core の待ち行列にある（DTN-00 #363）。
const (
	kindFerryArrive EventKind = iota + 2 // ClassFerryArrive。Data は ferry.FerryID
	kindFerryReady                       // ClassFerryReady。Data は ferry.FerryID
	kindRoundEnd                         // ClassGameTimer。Data なし
)

// nodeView は node.state に出す値（最後に出した値と比べる）。
type nodeView struct {
	role                                         raft.StateType
	term, vote, lead, commit, applied, lastIndex uint64
}

// Engine は 1 ゲーム分の世界（DesignDoc §4.3）。I/O・goroutine・実時間を使わずに動く。
// 参照モデル（DTN-00 #363）: 島ごとの状態は N 個の islandcore.Core、Coordinator の状態（仮想時計・航路・船・手紙の
// 写し・古い手紙の検出）は coordcore.Core が持ち、Engine は今までと同じ順序でそれらを呼んで、島の出力を
// ドメインイベントにする。依頼の帳簿の表示・スコア・統計・台本のイベントは Engine が持つ。
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

	coord   *coordcore.Core
	tr      *ferry.Fleet       // = coord.Fleet（航路・船・手紙の写し）
	islands []*islandcore.Core // 添字 = ID - 1
	nodes   []*cluster.Node    // 観察だけに使う（= islands[i].Node()）。添字 = ID - 1
	ledgers []*ledger.Ledger   // 観察だけに使う（= islands[i].Ledger()）。添字 = ID - 1

	islandSeq    uint64         // 島のイベントと待ちに振る、全島で 1 本の Seq（今の Engine の Seq の相対的な順を保つ）
	lastLetterID ferry.LetterID // 最後に振った手紙の ID（全体で 1 本の連番）
	storms       stormBook      // 嵐の帳簿（storm.go）
	breakdowns   breakdownBook  // 船の故障の帳簿（breakdown.go）
	rushes       rushBook       // 注文ラッシュの帳簿（rush.go）
	drifts       driftBook      // 漂流瓶の帳簿（drift.go）
	reqs         requestBook    // 依頼の帳簿（request.go）
	score        scoreBook      // 採点の帳簿（score.go）
	stats        statsBook      // 統計と「今日の出来事」の帳簿（stats.go）

	phase   Phase
	opening cluster.OpeningResult

	last []nodeView // node.state を最後に出した値（島の出力の写し）。添字 = ID - 1

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
	e := &Engine{
		cfg:   cfg,
		opt:   opt,
		phase: PhaseIntro,
		last:  make([]nodeView, n),
		trace: newTraceHash(),
	}
	nodes, ledgers, opening, err := islandcore.OpeningAll(islandcore.NodeParams{
		Islands:         n,
		InitialLeader:   cfg.InitialLeader,
		ElectionTick:    cfg.Raft.ElectionTick,
		HeartbeatTick:   cfg.Raft.HeartbeatTick,
		MaxSizePerMsg:   cfg.Raft.MaxSizePerMsg,
		MaxInflightMsgs: cfg.Raft.MaxInflightMsgs,
		Resources:       cfg.Resources,
		RandIntn:        func(id uint64) func(int) int { return RaftRandIntn(seed, id) },
	})
	if err != nil {
		return nil, fmt.Errorf("sim: %w", err)
	}
	e.nodes, e.ledgers, e.opening = nodes, ledgers, opening
	coord, err := coordcore.New(coordcore.Config{
		Islands:  n,
		Routes:   cfg.Routes,
		Ferries:  cfg.Ferries,
		DockMs:   cfg.DockMs,
		RelayCap: cfg.RelayCap,
	})
	if err != nil {
		return nil, fmt.Errorf("sim: %w", err)
	}
	switch {
	case cfg.PortCap < 1:
		return nil, fmt.Errorf("sim: ferry: portCap %d < 1", cfg.PortCap)
	case cfg.PerLinkAppCap < 1:
		return nil, fmt.Errorf("sim: ferry: perLinkAppCap %d < 1", cfg.PerLinkAppCap)
	case cfg.RelayCap < 0:
		return nil, fmt.Errorf("sim: ferry: relayCap %d < 0", cfg.RelayCap)
	}
	e.coord = coord
	e.tr = coord.Fleet
	if err := e.initStorms(seed); err != nil {
		return nil, err
	}
	if err := e.initCancellations(seed); err != nil {
		return nil, err
	}
	if err := e.initBreakdowns(seed); err != nil {
		return nil, err
	}
	if err := e.initDemo(); err != nil {
		return nil, err
	}
	if err := e.initDrifts(seed); err != nil {
		return nil, err
	}
	e.cfg.Requests = cfg.Requests.Clone()
	reqCfg, err := e.planRushes(seed)
	if err != nil {
		return nil, err
	}
	plan, err := game.PlanRequests(reqCfg, cfg.Islands, int64(cfg.DurationMs), e.cfg.Resources, Sub(seed, StreamRequests))
	if err != nil {
		return nil, fmt.Errorf("sim: requests: %w", err)
	}
	e.reqs = newRequestBook(plan, e.cfg.Requests.RetryMs)
	if err := e.initScore(); err != nil {
		return nil, err
	}
	e.initStats()
	e.islands = make([]*islandcore.Core, n)
	for i := range e.islands {
		isl, err := islandcore.New(islandcore.Config{
			Island:        uint64(i + 1),
			Islands:       n,
			TickMs:        int64(cfg.Raft.TickMs),
			RetryMs:       e.cfg.Requests.RetryMs,
			PortCap:       cfg.PortCap,
			PerLinkAppCap: cfg.PerLinkAppCap,
			RelayCap:      cfg.RelayCap,
			Requests:      plan,
			NewLetterID:   e.nextLetterID,
			NextSeq:       e.nextIslandSeq,
		}, nodes[i], ledgers[i])
		if err != nil {
			return nil, fmt.Errorf("sim: %w", err)
		}
		e.islands[i] = isl
	}
	return e, nil
}

// nextLetterID は手紙の ID を全体で 1 本の連番で振る（島の港が使う）。
func (e *Engine) nextLetterID() ferry.LetterID {
	e.lastLetterID++
	return e.lastLetterID
}

// nextIslandSeq は島のイベントと待ちの Seq を全島で 1 本の連番で振る。
func (e *Engine) nextIslandSeq() uint64 {
	e.islandSeq++
	return e.islandSeq
}

// Now はゲーム内時刻を返す。
func (e *Engine) Now() SimTime { return e.coord.Now() }

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
	case CmdFerryLoad, CmdFerryUnload, CmdFerryDepart, CmdFerryAutoload, CmdRelayLoad:
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
		isl := e.islands[rc.Node-1]
		isl.ApplyRoleChange(int64(e.Now()), rc)
		e.drain(isl)
	}
	// 即時配達した手紙（e.opening.Delivered）は港を通らず ID もないので、手紙のイベントにしない。
	e.afterStepAt(islandcore.ClassStart)
	e.phase = PhaseSailing
	e.emit(PhaseChanged{Phase: PhaseSailing})
	e.startStorms()
	e.startBreakdowns()
	e.startRushes()
	e.startDrifts()
	for _, isl := range e.islands {
		isl.StartTicks(int64(e.Now()))
	}
	e.push(e.cfg.DurationMs, ClassGameTimer, kindRoundEnd, nil)
	e.scheduleRequests()
	e.startScore()
}

// applyFerry は cmd.ferry.*・cmd.relay.load を適用し、成功したらイベントを出す。失敗したら何も変えずに ferry のエラーを返す。
func (e *Engine) applyFerry(cmd Command) error {
	id := ferry.FerryID(cmd.FerryID)
	switch cmd.Type {
	case CmdFerryLoad:
		if err := e.load(id, toLetterIDs(cmd.LetterIDs)); err != nil {
			return err
		}
		for _, lid := range cmd.LetterIDs {
			e.emit(LetterMoved{LetterID: lid, Loc: ferryLoc(id)})
		}
	case CmdFerryUnload:
		unloaded, err := e.tr.Unload(id, toLetterIDs(cmd.LetterIDs))
		if err != nil {
			return err
		}
		f, _ := e.tr.Ferry(id)
		for _, l := range unloaded {
			// 送信元がこの島の手紙は港へ戻し、経由の手紙は中継倉庫に預けた（ferry.Fleet.Unload）。
			// 港で破棄された手紙も港の位置を出す（破棄は島の出力の letter.dropped）。
			loc := portLoc(f.At)
			if l.Location.Kind == ferry.LocRelay {
				loc = relayLoc(f.At)
			}
			e.emit(LetterMoved{LetterID: uint64(l.ID), Loc: loc})
		}
		isl := e.islands[f.At-1]
		isl.Unload(int64(e.Now()), unloaded)
		e.drain(isl)
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
	case CmdRelayLoad:
		return e.applyRelayLoad(id, cmd.LetterIDs)
	}
	return nil
}

// RunUntil は t 以下の時刻のイベントをすべて処理し、Now() を t にする。
// 航行中でなければ何もしない。t が DurationMs を超えていても DurationMs で止まり、ラウンドを終える。
// t < Now() なら何もしない。
//
// イベントは Coordinator のスケジューラと各島の待ち行列から (At, Class, Seq) の順に取り出す（島の Seq は全島で
// 1 本なので、今の Engine の 1 本のスケジューラと同じ順になる）。
func (e *Engine) RunUntil(t SimTime) {
	if e.phase != PhaseSailing {
		return
	}
	target := min(t, e.cfg.DurationMs)
	if target < e.Now() {
		return
	}
	for {
		k, iev, ok := e.nextIslandEvent(target)
		cev, cok := e.coord.Sched.Peek()
		cok = cok && cev.At <= target
		if !ok && !cok {
			break
		}
		if !ok || (cok && coordFirst(cev, iev)) {
			ev, _ := e.coord.Sched.Pop()
			e.handleEvent(ev)
			e.afterStepAt(islandcore.Class(ev.Class))
			if ev.Kind == kindRoundEnd {
				return
			}
			continue
		}
		if err := e.coord.Sched.AdvanceTo(SimTime(iev.At)); err != nil {
			panic(fmt.Sprintf("sim: advance to island event at %d: %v", iev.At, err))
		}
		e.handleIslandEvent(k, iev)
		e.afterStepAt(iev.Class)
	}
	if err := e.coord.Sched.AdvanceTo(target); err != nil {
		panic(fmt.Sprintf("sim: advance to %d: %v", target, err))
	}
}

// coordFirst は、Coordinator のイベント c が島のイベント i より先か（(At, Class) で比べる。種類は重ならない）。
func coordFirst(c Event, i islandcore.Event) bool {
	if int64(c.At) != i.At {
		return int64(c.At) < i.At
	}
	return uint8(c.Class) < uint8(i.Class)
}

// nextIslandEvent は、全島の待ち行列のうち At <= target で (At, Class, Seq) が最小のイベントと、その島の添字を返す。
func (e *Engine) nextIslandEvent(target SimTime) (int, islandcore.Event, bool) {
	best, found := -1, false
	var bev islandcore.Event
	for k, isl := range e.islands {
		ev, ok := isl.Peek()
		if !ok || ev.At > int64(target) {
			continue
		}
		if !found || islandEventLess(ev, bev) {
			best, bev, found = k, ev, true
		}
	}
	return best, bev, found
}

func islandEventLess(a, b islandcore.Event) bool {
	if a.At != b.At {
		return a.At < b.At
	}
	if a.Class != b.Class {
		return a.Class < b.Class
	}
	return a.Seq < b.Seq
}

// handleIslandEvent は島 k の待ち行列の先頭のイベント iev を処理する（後処理は呼び出し側）。
//
// RaftTick は全島で同じ時刻にあるので、今の Engine と同じく、全ノードの Tick → 全島の待っている依頼の再試行
// （入れた順）の順にまとめて処理する。
func (e *Engine) handleIslandEvent(k int, iev islandcore.Event) {
	now := int64(e.Now())
	switch iev.Kind {
	case islandcore.KindRaftTick:
		for _, isl := range e.islands {
			ev, ok := isl.Peek()
			if !ok || ev.Kind != islandcore.KindRaftTick || ev.At != iev.At {
				panic(fmt.Sprintf("sim: island %d: next event %+v is not the RaftTick at %d", isl.ID(), ev, iev.At))
			}
			isl.HandleTick(isl.Pop())
		}
		type wait struct {
			isl *islandcore.Core
			w   islandcore.Wait
		}
		var ws []wait
		for _, isl := range e.islands {
			for _, w := range isl.TakeWaits() {
				ws = append(ws, wait{isl, w})
			}
		}
		slices.SortFunc(ws, func(a, b wait) int { return cmpUint64(a.w.Seq, b.w.Seq) })
		for _, w := range ws {
			w.isl.RetryWait(now, w.w)
			e.drain(w.isl)
		}
	case islandcore.KindRequestSpawn:
		isl := e.islands[k]
		ev := isl.Pop()
		e.spawnRequest(ev.Req)
		isl.Handle(ev)
		e.drain(isl)
	case islandcore.KindRequestRetry:
		isl := e.islands[k]
		isl.Handle(isl.Pop())
		e.drain(isl)
	default:
		panic(fmt.Sprintf("sim: unknown island event kind %d", iev.Kind))
	}
}

func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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
	if _, err := e.coord.Sched.Push(at, class, kind, data); err != nil {
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

// handleEvent はスケジューラのイベント 1 つを処理する（後処理は呼び出し側）。
func (e *Engine) handleEvent(ev Event) {
	switch ev.Kind {
	case kindStormForecast, kindStormStart, kindStormEnd:
		e.handleStorm(ev)
	case kindBreakdownStart, kindBreakdownRepair:
		e.handleBreakdown(ev)
	case kindRushForecast:
		e.rushForecast(ev.Data.(int))
	case kindDrift:
		e.drift(ev.Data.(int))
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
	default:
		panic(fmt.Sprintf("sim: unknown event kind %d", ev.Kind))
	}
}

// arrive は船の到着を処理し、その島宛ての手紙を積んだ順に、その島の核に配達する（§6.3）。
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
	letters := make([]ferry.Letter, 0, len(delivered))
	for _, lid := range delivered {
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: delivered letter %d not found", lid))
		}
		e.noteDriftCandidate(l)
		letters = append(letters, l)
	}
	isl := e.islands[f.At-1]
	isl.Deliver(int64(e.Now()), letters)
	e.drain(isl)
}

// load は停泊中の船 id に、今いる島の港の手紙を積む（Coordinator の写しで検査して積み、島の港から取り出す）。
func (e *Engine) load(id ferry.FerryID, letters []ferry.LetterID) error {
	if err := e.tr.Load(id, letters); err != nil {
		return err
	}
	f, _ := e.tr.Ferry(id)
	if _, err := e.islands[f.At-1].Take(letters); err != nil {
		panic(fmt.Sprintf("sim: load %q: island %d: %v", id, f.At, err))
	}
	return nil
}

// afterStep はコマンド（成功した Apply）の後処理（afterStepAt の ClassCommand）。
func (e *Engine) afterStep() { e.afterStepAt(islandcore.ClassCommand) }

// afterStepAt は 1 ステップ（成功した Apply・スケジューラのイベント 1 つ）の後処理。class は島の出力に付ける印。
func (e *Engine) afterStepAt(class islandcore.Class) {
	now := int64(e.Now())
	for _, isl := range e.islands {
		isl.Process(now, class)
		e.drain(isl)
	}
	for _, isl := range e.islands {
		isl.ReportState(now, class)
		e.drain(isl)
	}
	e.refreshRequests()
	e.refreshScore()
	// 配達・破棄した手紙のイベントを作り終えたので、ここで手放す（#103）。
	e.tr.Reclaim()
	if e.opt.CheckInvariants {
		e.checkInvariants()
	}
}

// drain は島 isl の出力を、出した順にドメインイベントと Coordinator の写しに反映する。
func (e *Engine) drain(isl *islandcore.Core) {
	id := isl.ID()
	for _, o := range isl.Drain() {
		switch d := o.Data.(type) {
		case islandcore.LetterCreatedOut:
			if err := e.tr.Track(d.Letter); err != nil {
				panic(fmt.Sprintf("sim: island %d: track: %v", id, err))
			}
			e.emit(LetterCreated{Letter: newLetterView(d.Letter, d.SenderTerm)})
		case islandcore.LetterDroppedOut:
			if err := e.tr.MarkDropped(d.LetterID); err != nil {
				panic(fmt.Sprintf("sim: island %d: drop: %v", id, err))
			}
			e.emit(LetterDropped{LetterID: uint64(d.LetterID), Reason: string(d.Reason)})
		case islandcore.LetterDeliveredOut:
			e.emit(LetterDelivered{LetterID: uint64(d.LetterID), IslandID: id, Accepted: d.Accepted})
		case islandcore.NodeStateOut:
			v := nodeView{
				role:      d.Role,
				term:      d.Term,
				vote:      d.Vote,
				lead:      d.Lead,
				commit:    d.Commit,
				applied:   d.Applied,
				lastIndex: d.LastIndex,
			}
			e.last[id-1] = v
			e.emit(NodeState{
				ID:        id,
				Role:      roleString(v.role),
				Term:      v.term,
				Vote:      v.vote,
				Lead:      v.lead,
				Commit:    v.commit,
				Applied:   v.applied,
				LastIndex: v.lastIndex,
			})
		case islandcore.LeaderElectedOut:
			e.emit(LeaderElected{ID: id, Term: d.Term})
		case islandcore.SteppedDownOut:
			e.emit(SteppedDown{ID: id, Term: d.Term, Reason: d.Reason})
		case islandcore.TermChangedOut:
			for _, lid := range e.coord.StaleLetters(id, d.Term) {
				e.emit(LetterStale{LetterID: uint64(lid)})
			}
		case islandcore.LedgerAppliedOut:
			e.emit(LedgerApplied{IslandID: id, Index: d.Index, ReqID: d.ReqID, Result: d.Result, Reason: d.Reason})
			e.observeApplied(id, ledger.Result{ReqID: d.ReqID, Status: d.Result, Reason: d.Reason})
		case islandcore.RequestRoutedOut:
			e.observeRouted(d)
		default:
			panic(fmt.Sprintf("sim: island %d: unknown output %T", id, o.Data))
		}
	}
}

// propose は island のノードに data を提案する。Leader でなければ raft.ErrProposalDropped（包まない）。
// 後処理は呼ばない（呼び出し側のステップの後処理で、手紙と適用のイベントになる）。
func (e *Engine) propose(island uint64, data []byte) error {
	if island < 1 || island > uint64(len(e.islands)) {
		return fmt.Errorf("sim: propose: unknown island %d", island)
	}
	return e.islands[island-1].Propose(data)
}
