package sim

import (
	"fmt"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// 依頼のイベント（プロトコル §6）。
const (
	EvRequestCreated EventType = "request.created"
	EvRequestUpdated EventType = "request.updated"
)

// RequestState は依頼の表示用の値（プロトコル §5.3 の Request）。キーの順はプロトコルの例のとおり。
type RequestState struct {
	ID        string         `json:"id"`
	Origin    uint64         `json:"origin"`
	Kind      string         `json:"kind"`
	Resource  string         `json:"resource"`
	Critical  bool           `json:"critical"`
	CreatedAt SimTime        `json:"createdAt"`
	Deadline  SimTime        `json:"deadline"`
	Status    string         `json:"status"`
	Expired   bool           `json:"expired"`
	Result    *RequestResult `json:"result"` // 確定・予約失敗のときだけ。ほかは null
}

// RequestResult は依頼元の台帳の結果（設計書にない判断。プロトコル §5.3 の result の形）。
type RequestResult struct {
	Status string `json:"status"`           // "ok" | "rejected"
	Reason string `json:"reason,omitempty"` // rejected のときだけ（ledger の理由。例: "already_reserved"）
}

// RequestCreated は request.created。status は received。
type RequestCreated struct {
	Request RequestState `json:"request"`
}

// RequestUpdated は request.updated。同じステップの中の変化はまとめる。
type RequestUpdated struct {
	ID      string         `json:"id"`
	Status  string         `json:"status"`
	Expired bool           `json:"expired"`
	Result  *RequestResult `json:"result"`
}

func (RequestCreated) EventType() EventType { return EvRequestCreated }
func (RequestUpdated) EventType() EventType { return EvRequestUpdated }

// requestBook は依頼の帳簿（DesignDoc §8）。依頼は New で全部を決める。
type requestBook struct {
	list    []requestEntry // plan の順（= ID の順）
	byID    map[string]int // reqId → 添字。検索だけに使い、走査しない
	next    int            // まだ発生していない最初の添字（NextNotableAt が使う）
	retryMs SimTime
}

// requestEntry は依頼 1 件の帳簿の行。
type requestEntry struct {
	req     game.Request
	created bool
	track   game.Track
	letter  ferry.LetterID // 今の依頼書（track.Outcome == OutcomeLetter のとき）。0 = なし
	expired bool           // T-203 が使う。今は常に false
	last    requestView    // 最後に出した status・expired・result
}

// requestView は request.* で最後に出した値（比較用）。
type requestView struct {
	status  game.Status
	expired bool
	result  *RequestResult
}

func (v requestView) equal(w requestView) bool {
	if v.status != w.status || v.expired != w.expired {
		return false
	}
	if v.result == nil || w.result == nil {
		return v.result == nil && w.result == nil
	}
	return *v.result == *w.result
}

// newRequestBook は計画した依頼の列から帳簿を作る。
func newRequestBook(plan []game.Request, retryMs int64) requestBook {
	b := requestBook{
		list:    make([]requestEntry, len(plan)),
		byID:    make(map[string]int, len(plan)),
		retryMs: SimTime(retryMs),
	}
	for i, r := range plan {
		b.list[i] = requestEntry{req: r}
		b.byID[r.ID] = i
	}
	return b
}

// scheduleRequests は依頼の発生を、依頼元の島の待ち行列に計画の順に積む（cmd.game.start）。
// 積む順（全島で 1 本の Seq）が同じ時刻の処理の順になる。
func (e *Engine) scheduleRequests() {
	for i, r := range e.reqs.list {
		e.islands[r.req.Origin-1].ScheduleSpawn(i)
	}
}

// spawnRequest は依頼 i の発生（島の RequestSpawn）のうち、帳簿と request.created の部分。
// 窓口の route は依頼元の島が行う（handleIslandEvent）。ほかの島に発生を知らせる（依頼書を受けたときの判定）。
func (e *Engine) spawnRequest(i int) {
	r := &e.reqs.list[i]
	r.created = true
	e.reqs.next++
	r.last = requestView{status: game.StatusReceived, expired: r.expired}
	e.emit(RequestCreated{Request: r.state()})
	for _, isl := range e.islands {
		isl.MarkCreated(r.req.ID)
	}
	if e.remote != nil {
		e.remote.NoteCreated(r.req.ID)
	}
}

// observeApplied は、島 id の台帳への適用の結果 r を観察する（ADR-0015）。
// いずれかの島で最初に適用されたら committed、依頼元で適用されたら結果を持つ。duplicate は見ない。
func (e *Engine) observeApplied(id uint64, r ledger.Result) {
	i, ok := e.reqs.byID[r.ReqID]
	if !ok || r.Status == ledger.StatusDuplicate {
		return
	}
	entry := &e.reqs.list[i]
	entry.track.Committed = true
	if id == entry.req.Origin && entry.track.Result == nil {
		entry.track.Result = &game.Result{Status: r.Status, Reason: r.Reason}
		// 依頼元で結果が出たので、どの島の窓口もこの依頼を route しない（今の Engine の route の判定と同じ）。
		for _, isl := range e.islands {
			isl.MarkResolved(r.ReqID)
		}
		if e.remote != nil {
			e.remote.NoteResolved(r.ReqID)
		}
	}
}

// observeRouted は、島の窓口の判断（request.routed）を依頼の帳簿に写す。
func (e *Engine) observeRouted(d islandcore.RequestRoutedOut) {
	r := &e.reqs.list[d.Req]
	switch d.Action {
	case islandcore.ActionProposed:
		e.noteProposed(d.Req)
		r.track.Outcome = game.OutcomeProposed
		r.letter = 0
	case islandcore.ActionForwarded:
		r.track.Outcome = game.OutcomeLetter
		r.letter = d.LetterID
	case islandcore.ActionWaiting:
		r.track.Outcome = game.OutcomeWaiting
		r.letter = 0
	default:
		panic(fmt.Sprintf("sim: request %s: unknown route action %q", d.ReqID, d.Action))
	}
}

// route は、島 island の窓口に依頼 i を今の時刻で route させる（テストが使う）。
func (e *Engine) route(i int, island uint64, hops int) {
	isl := e.islands[island-1]
	isl.Route(int64(e.Now()), i, hops)
	e.drain(isl)
}

// refreshRequests は、発生済みの依頼の状態を判定し、変わったものを ID の順に request.updated にする
// （ステップの後処理。Reclaim の前なので、配達・破棄した手紙も引ける）。
func (e *Engine) refreshRequests() {
	for i := range e.reqs.list {
		r := &e.reqs.list[i]
		if !r.created {
			continue
		}
		if s := r.last.status; s == game.StatusSucceeded || s == game.StatusRejected {
			continue
		}
		if r.track.Outcome == game.OutcomeLetter {
			l, ok := e.tr.Letter(r.letter)
			// 中継倉庫に預けた依頼書も、まだ運んでいる途中なので「運搬中」（in_transit）にする。
			r.track.OnFerry = ok && (l.Location.Kind == ferry.LocFerry || l.Location.Kind == ferry.LocRelay)
		}
		v := r.view()
		if v.equal(r.last) {
			continue
		}
		r.last = v
		e.emit(RequestUpdated{ID: r.req.ID, Status: string(v.status), Expired: v.expired, Result: cloneResult(v.result)})
	}
}

// view は今の判定の値を作る。
func (r *requestEntry) view() requestView {
	v := requestView{status: r.track.Status(), expired: r.expired}
	if res := r.track.Result; res != nil {
		v.result = &RequestResult{Status: res.Status, Reason: res.Reason}
	}
	return v
}

// state は、最後に出した値での表示用の値を作る。
func (r *requestEntry) state() RequestState {
	return RequestState{
		ID:        r.req.ID,
		Origin:    r.req.Origin,
		Kind:      r.req.Kind,
		Resource:  r.req.Resource,
		Critical:  r.req.Critical,
		CreatedAt: SimTime(r.req.CreatedAt),
		Deadline:  SimTime(r.req.Deadline),
		Status:    string(r.last.status),
		Expired:   r.last.expired,
		Result:    cloneResult(r.last.result),
	}
}

// requestStates は発生済みの依頼を ID の順に返す（Snapshot）。nil にしない。
func (e *Engine) requestStates() []RequestState {
	out := make([]RequestState, 0, e.reqs.next)
	for i := range e.reqs.list {
		if r := &e.reqs.list[i]; r.created {
			out = append(out, r.state())
		}
	}
	return out
}

// nextRequestAt は、まだ発生していない最初の依頼の時刻を返す（NextNotableAt）。
func (e *Engine) nextRequestAt() (SimTime, bool) {
	if e.reqs.next >= len(e.reqs.list) {
		return 0, false
	}
	return SimTime(e.reqs.list[e.reqs.next].req.CreatedAt), true
}

func cloneResult(r *RequestResult) *RequestResult {
	if r == nil {
		return nil
	}
	c := *r
	return &c
}
