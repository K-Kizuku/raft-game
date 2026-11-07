package sim

import (
	"errors"
	"fmt"

	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
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
	waits   []routeWait    // 次の RaftTick でやり直す route（入れた順）
	retryMs SimTime
}

// requestEntry は依頼 1 件の帳簿の行。
type requestEntry struct {
	req     game.Request
	created bool
	track   game.Track
	letter  ferry.LetterID // 今の依頼書（track.Outcome == OutcomeLetter のとき）。0 = なし
	gen     uint64         // 再送のタイマーの世代
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

// routeWait は、代表が分からずに待っている route（次の RaftTick でやり直す）。
type routeWait struct {
	req    int    // 帳簿の添字
	island uint64 // route する島
	hops   int    // その島で依頼書を書くときの hops
}

// retryTimer は kindRequestRetry の Data。
type retryTimer struct {
	req int
	gen uint64
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

// scheduleRequests は依頼の発生をスケジューラに積む（cmd.game.start）。積む順が同じ時刻の処理の順になる。
func (e *Engine) scheduleRequests() {
	for i, r := range e.reqs.list {
		e.push(SimTime(r.req.CreatedAt), ClassRequestSpawn, kindRequestSpawn, i)
	}
}

// spawnRequest は依頼 i の発生（kindRequestSpawn）。
func (e *Engine) spawnRequest(i int) {
	r := &e.reqs.list[i]
	r.created = true
	e.reqs.next++
	r.last = requestView{status: game.StatusReceived, expired: r.expired}
	e.emit(RequestCreated{Request: r.state()})
	e.route(i, r.req.Origin, 0)
}

// retryRequest は再送の判定（kindRequestRetry、FR-REQ-09）。
func (e *Engine) retryRequest(t retryTimer) {
	r := &e.reqs.list[t.req]
	if t.gen != r.gen || r.track.Result != nil {
		return
	}
	e.route(t.req, r.req.Origin, 0)
}

// retryWaits は待っている route を入れた順にやり直す（kindRaftTick の最後）。
func (e *Engine) retryWaits() {
	ws := e.reqs.waits
	e.reqs.waits = nil
	for _, w := range ws {
		e.route(w.req, w.island, w.hops)
	}
}

// route は island の役場が依頼 i を代表の島へ届ける（DesignDoc §8.1）。
// 役場が読むのはノードの役割と知っている Leader だけで、Leader を決めない。
// hops は、island で依頼書を書くときの hops（転送された回数）。
func (e *Engine) route(i int, island uint64, hops int) {
	r := &e.reqs.list[i]
	if r.track.Result != nil {
		return
	}
	st := e.nodes[island-1].Status()
	d := game.Decide(st.Role == raft.StateLeader, st.Lead, island)
	switch d.Action {
	case game.ActPropose:
		data, err := ledger.Encode(r.req.Command())
		if err != nil {
			panic(fmt.Sprintf("sim: request %s: encode: %v", r.req.ID, err))
		}
		err = e.propose(island, data)
		switch {
		case err == nil:
			e.noteProposed(i)
			r.track.Outcome = game.OutcomeProposed
			r.letter = 0
			if island == r.req.Origin {
				e.armRetry(i)
			}
		case errors.Is(err, raft.ErrProposalDropped):
			e.waitRoute(i, island, hops)
		default:
			panic(fmt.Sprintf("sim: request %s: propose at %d: %v", r.req.ID, island, err))
		}
	case game.ActLetter:
		lid, dropped, err := e.tr.PostRequest(int64(e.Now()), island, d.To, ferry.RequestNote{ReqID: r.req.ID, Hops: hops})
		if err != nil {
			panic(fmt.Sprintf("sim: request %s: post at %d: %v", r.req.ID, island, err))
		}
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: created letter %d not found", lid))
		}
		e.emit(LetterCreated{Letter: newLetterView(l, 0)})
		e.emitDrops(dropped)
		r.track.Outcome = game.OutcomeLetter
		r.letter = lid
		if island == r.req.Origin {
			e.armRetry(i)
		}
	case game.ActWait:
		e.waitRoute(i, island, hops)
	default:
		panic(fmt.Sprintf("sim: unknown route action %d", d.Action))
	}
}

// waitRoute は、代表が分からないので待つ。同じ (依頼, 島) の待ちは 1 つだけ。再送のタイマーは積まない。
func (e *Engine) waitRoute(i int, island uint64, hops int) {
	r := &e.reqs.list[i]
	r.track.Outcome = game.OutcomeWaiting
	r.letter = 0
	for _, w := range e.reqs.waits {
		if w.req == i && w.island == island {
			return
		}
	}
	e.reqs.waits = append(e.reqs.waits, routeWait{req: i, island: island, hops: hops})
}

// armRetry は、依頼元が送ったので、再送のタイマーを積み直す（古いタイマーは世代で無効にする）。
// DurationMs より後でも積む（ラウンドが先に終わるので処理されない）。
func (e *Engine) armRetry(i int) {
	r := &e.reqs.list[i]
	r.gen++
	e.push(e.Now()+e.reqs.retryMs, ClassRequestRetry, kindRequestRetry, retryTimer{req: i, gen: r.gen})
}

// deliverRequest は依頼書 l が宛先に届いたとき、届いた島の役場に渡す（DesignDoc §8.1）。
// 帳簿にない reqId（テストが直接作った依頼書など）・まだ発生していない依頼は何もしない。
func (e *Engine) deliverRequest(l ferry.Letter) {
	i, ok := e.reqs.byID[l.Req.ReqID]
	if !ok || !e.reqs.list[i].created {
		return
	}
	e.route(i, l.To, l.Req.Hops+1)
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
	}
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
			r.track.OnFerry = ok && l.Location.Kind == ferry.LocFerry
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
