package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
)

// Remote は、分散版の Coordinator（DTN-05 #162、docs/dtn/simulation-time.md §2.2）が Engine の Coordinator の部分
// （仮想時計・航路と船の時刻表・手紙の写し・依頼の帳簿・採点・統計・台本の嵐）を使うときの、島と船への作用の受け口。
//
// NewRemote で作った Engine は島の核（RawNode・台帳・港）を持たない。島と船を変える処理（到着の配達・積み下ろし・
// 出航・依頼の発生と結果の通知）は、Fleet の写しを更新した後に Remote に渡す。Remote の実装（Coordinator）は、
// それを Actor への要求にし、島の応答の出力を RemoteOutputs で Engine に返し、RemoteAfterStep でステップを閉じる。
//
// メソッドは Engine の中から同期に呼ばれる。実装は作用を記録するだけにし、Engine を呼び返さない。
// 引数の slice は Engine と共有しない（実装が持ってよい）。
type Remote interface {
	// Arrive: 船 id が時刻 t に island に着いた。delivered は宛先がその島の手紙（積んだ順。空なら配達はない）。
	Arrive(t SimTime, id ferry.FerryID, island uint64, delivered []ferry.LetterID)
	// Load: 停泊中の船 id に、island の港の手紙 letters を、この順に積んだ（cmd.ferry.load・cmd.ferry.autoload）。
	Load(t SimTime, id ferry.FerryID, island uint64, letters []ferry.LetterID)
	// RelayLoad: 停泊中の船 id に、island の中継倉庫の手紙 letters を、この順に積んだ（cmd.relay.load）。
	RelayLoad(t SimTime, id ferry.FerryID, island uint64, letters []ferry.LetterID)
	// Unload: 停泊中の船 id から island に手紙を下ろした。letters は ferry.Fleet.Unload が返した、ID の昇順で
	// 所在（港か中継倉庫）つきの手紙。
	Unload(t SimTime, id ferry.FerryID, island uint64, letters []ferry.Letter)
	// Depart: 船 id が時刻 t に to へ出航した（arriveAt に着く）。
	Depart(t SimTime, id ferry.FerryID, to uint64, arriveAt SimTime)
	// NoteCreated: 依頼 reqID が発生した（参照モデルが全島の MarkCreated を呼ぶ位置）。
	NoteCreated(reqID string)
	// NoteResolved: 依頼 reqID の依頼元の台帳の結果が出た（参照モデルが全島の MarkResolved を呼ぶ位置）。
	NoteResolved(reqID string)
}

// NewRemote は、島の核を持たない Engine（分散版の Coordinator の部分）を作る。r は nil でないこと。
// New と同じ検査と計画（嵐・依頼）をするが、開会式の計算と島の核は作らない（各島の Actor が init で行う）。
// 故障・注文ラッシュ・漂流瓶・欠航は分散版の対象外（simulation-time.md §6）なので、定義されていればエラー。
//
// 動かし方（Coordinator だけが使う）:
//   - cmd.game.start の前に SetRemoteOpening で各島の init の出力を渡す
//   - 時間は RemoteNextEvent・RemoteHandleEvent・RemoteAdvance・RemoteSpawn・RemoteOutputs・RemoteAfterStep で進める
//     （RunUntil は使えない）
//   - Apply は、成功した cmd.game.start 以外のコマンドの後処理をしない。島と船の応答を RemoteOutputs で渡した後に、
//     RemoteAfterStep(islandcore.ClassCommand) を呼ぶ
//
// 観察（NodeDetail・TechStats の FinalLogs・テストモードの検査）は島の核を使うので、分散版では使えない
// （NodeDetail は false、FinalLogs は空）。
func NewRemote(cfg Config, seed uint64, opt Options, r Remote) (*Engine, error) {
	if r == nil {
		return nil, fmt.Errorf("sim: NewRemote: nil Remote")
	}
	switch {
	case len(cfg.Breakdowns) > 0, len(cfg.Rushes) > 0, len(cfg.Drifts) > 0, len(cfg.Cancellations) > 0:
		return nil, ErrRemoteUnsupported
	}
	return buildEngine(cfg, seed, opt, r)
}

// ErrRemoteUnsupported は、分散版の対象外のイベント（故障・注文ラッシュ・漂流瓶・欠航）を定義した設定の NewRemote のエラー。
var ErrRemoteUnsupported = fmt.Errorf("sim: breakdowns, rushes, drifts and cancellations are not supported by the remote engine")

func (e *Engine) mustRemote(what string) {
	if e.remote == nil {
		panic("sim: " + what + " on a local engine")
	}
}

// RemotePlan は依頼の計画（plan の順 = ID の順）の複製を返す。
func (e *Engine) RemotePlan() []game.Request { return slices.Clone(e.plan) }

// SetRemoteOpening は、cmd.game.start で適用する各島の init の出力（添字 = ID - 1、t = 0）を渡す。
func (e *Engine) SetRemoteOpening(outs [][]islandcore.Output) {
	e.mustRemote("SetRemoteOpening")
	if len(outs) != e.cfg.Islands {
		panic(fmt.Sprintf("sim: SetRemoteOpening: %d islands, want %d", len(outs), e.cfg.Islands))
	}
	e.remoteOpening = outs
}

// SetRemoteApply は、Apply の代わりに呼ぶ関数を渡す（nil で外す）。Coordinator は、ボット（Apply を呼ぶ）を
// 自分の Apply（Actor への作用を含む）に通すのに使う。Coordinator 自身は RemoteApply で Engine に適用する。
func (e *Engine) SetRemoteApply(f func(Command) Ack) {
	e.mustRemote("SetRemoteApply")
	e.remoteApply = f
}

// RemoteApply は、コマンドを Engine に適用する（SetRemoteApply を通さない Apply）。
func (e *Engine) RemoteApply(cmd Command) Ack {
	e.mustRemote("RemoteApply")
	return e.apply(cmd)
}

// RemoteNextEvent は、Coordinator のスケジューラの次のイベントの時刻と種類を返す（無ければ false）。
func (e *Engine) RemoteNextEvent() (SimTime, EventClass, bool) {
	ev, ok := e.coord.Sched.Peek()
	if !ok {
		return 0, 0, false
	}
	return ev.At, ev.Class, true
}

// RemoteHandleEvent は、Coordinator のスケジューラの次のイベントを取り出して処理する（後処理は RemoteAfterStep）。
// 取り出したイベントの種類を返す。ラウンドの終わりなら roundEnd が true（Phase は PhaseEnded）。
func (e *Engine) RemoteHandleEvent() (class EventClass, roundEnd bool) {
	e.mustRemote("RemoteHandleEvent")
	ev, ok := e.coord.Sched.Pop()
	if !ok {
		panic("sim: RemoteHandleEvent: no event")
	}
	e.handleEvent(ev)
	return ev.Class, ev.Kind == kindRoundEnd
}

// RemoteAdvance は Coordinator の時計を t に進める（t より前のイベントが残っていればエラー）。
func (e *Engine) RemoteAdvance(t SimTime) error {
	e.mustRemote("RemoteAdvance")
	return e.coord.Sched.AdvanceTo(t)
}

// RemoteSpawn は、時刻 Now() に島 origin で発生する依頼（計画の順）を帳簿に入れ、request.created を出す
// （参照モデルの RequestSpawn のうち Coordinator の部分。窓口の route は島が行う）。発生させた依頼の数を返す。
func (e *Engine) RemoteSpawn(origin uint64) int {
	e.mustRemote("RemoteSpawn")
	n := 0
	for i := range e.reqs.list {
		r := &e.reqs.list[i]
		if !r.created && r.req.Origin == origin && SimTime(r.req.CreatedAt) == e.Now() {
			e.spawnRequest(i)
			n++
		}
	}
	return n
}

// RemoteOutputs は島 island の出力 outs を、並びの順にドメインイベントと写しに反映する。
// 出力の時刻は Now() であること（呼び出し側が RemoteAdvance で合わせる）。
func (e *Engine) RemoteOutputs(island uint64, outs []islandcore.Output) {
	e.mustRemote("RemoteOutputs")
	if island < 1 || island > uint64(e.cfg.Islands) {
		panic(fmt.Sprintf("sim: RemoteOutputs: unknown island %d", island))
	}
	for _, o := range outs {
		if SimTime(o.T) != e.Now() {
			panic(fmt.Sprintf("sim: RemoteOutputs: island %d output at %d, now %d", island, o.T, e.Now()))
		}
	}
	e.drainOutputs(island, outs)
}

// RemoteAfterStep は 1 ステップの後処理（依頼の状態・採点の更新と、配達・破棄した手紙の写しの片付け）。
func (e *Engine) RemoteAfterStep(class islandcore.Class) {
	e.mustRemote("RemoteAfterStep")
	e.afterStepAt(class)
}

func cloneLetters(ls []ferry.Letter) []ferry.Letter {
	out := make([]ferry.Letter, len(ls))
	copy(out, ls)
	return out
}

// LetterViewOf は、港に置いた時点の手紙 l の表示用の値（letter.created の letter）を作る。
// senderTerm は送信元のノードの今の Term。分散版の島の Actor が letter.created の出力を作るのに使う。
func LetterViewOf(l ferry.Letter, senderTerm uint64) LetterView { return newLetterView(l, senderTerm) }
