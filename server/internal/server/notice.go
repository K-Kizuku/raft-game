package server

import (
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// 通知のキー（ゲームデザイン §7.3「通知のキー」）。
const (
	noticeRequestCreated    = "request.created"
	noticeRequestSucceeded  = "request.succeeded"
	noticeRequestRejected   = "request.rejected"
	noticeRequestExpired    = "request.expired"
	noticeIslandSilent      = "island.silent"
	noticeNodeCampaign      = "node.campaign"
	noticeLeaderElected     = "leader.elected"
	noticeLeaderSteppedDown = "leader.stepped_down"
	noticeStormForecast     = "storm.forecast"
	noticeRushForecast      = "rush.forecast"
	noticeFerryBroken       = "ferry.broken"
)

// 通知の判定に使う、Engine のイベントの値（sim のイベントの文字列）。
const (
	noticeRoleFollower      = "follower"
	noticeRoleCandidate     = "candidate"
	noticeReasonCheckQuorum = "check_quorum"
	noticeForecastStorm     = "storm"
	noticeForecastRush      = "rush"
	noticeStatusSucceeded   = "succeeded"
	noticeStatusRejected    = "rejected"
)

// noticeData は notice の中身（プロトコル §6）。sim.EventData を満たすので、sim.DomainEvent の Data に入れて
// ほかのイベントと同じく DomainEvent.MarshalJSON で送る（{"seq":…,"t":…,"type":"notice","islandId":…,"key":…,"params":{…}}）。
type noticeData struct {
	IslandID uint64       `json:"islandId"`
	Key      string       `json:"key"`
	Params   noticeParams `json:"params"`
}

// noticeParams は notice の params。キーごとに使う欄だけを入れ、ほかは出さない（params の無いキーは {}）。
type noticeParams struct {
	ReqID    string   `json:"reqId,omitempty"`
	RouteIDs []string `json:"routeIds,omitempty"`
	At       *int64   `json:"at,omitempty"`
	FerryID  string   `json:"ferryId,omitempty"`
	Until    *int64   `json:"until,omitempty"`
}

func (noticeData) EventType() sim.EventType { return sim.EventType(protocol.EventTypeNotice) }

// noticer は、Engine のドメインイベントから notice を作り、ブラウザに送るイベントの seq を振り直す。
// run の goroutine だけが触れる。状態は次の通知の判定のためだけに持ち、Engine には何も返さない。
type noticer struct {
	silenceAfterMs int64                 // st.Satisfaction.SilenceAfterMs（island.silent の判定）
	off            uint64                // それまでに差し込んだ notice の数（ワイヤの seq = Engine の seq + off）
	opening        bool                  // phase.changed{opening} から phase.changed{sailing} まで
	roles          []string              // 添字 = 島 ID − 1。最後に見た node.state の role
	silentAt       []int64               // 添字 = 島 ID − 1。island.silent を出したときの lastDeliveredAt。−1 = まだ
	reqs           map[string]*noticeReq // 依頼 ID → 依頼元と出した通知（引くだけで、走査しない）
	ferries        []noticeFerry         // 船の今の居場所（ferry.broken の話す島）。ステージの順
}

// noticeFerry は船 1 隻の居場所。at は停泊している島（航行中は 0）。
// noticer は Engine を持たないので、Engine の船の状態を、ステージ・スナップショット・ferry.departed・ferry.arrived から写して持つ。
type noticeFerry struct {
	id string
	at uint64
}

// noticeReq は依頼 1 件の、通知の判定の状態。
type noticeReq struct {
	origin  uint64
	settled bool // request.succeeded か request.rejected を出した（または resync で既に確定・失敗だった）
	expired bool // request.expired を出した（または resync で既に期限切れだった）
}

// newNoticer は st の島の数で noticer を作る。roles は follower、silentAt は −1。
func newNoticer(st *stage.Stage) *noticer {
	n := len(st.Islands)
	nt := &noticer{
		silenceAfterMs: st.Satisfaction.SilenceAfterMs,
		roles:          make([]string, n),
		silentAt:       make([]int64, n),
		reqs:           make(map[string]*noticeReq),
	}
	for i := range n {
		nt.roles[i] = noticeRoleFollower
		nt.silentAt[i] = -1
	}
	for _, f := range st.Ferries {
		nt.ferries = append(nt.ferries, noticeFerry{id: f.ID, at: f.Home})
	}
	return nt
}

// ferry は船 id の居場所を返す。知らない船なら nil。
func (n *noticer) ferry(id string) *noticeFerry {
	for i := range n.ferries {
		if n.ferries[i].id == id {
			return &n.ferries[i]
		}
	}
	return nil
}

// setFerryAt は船 id の居場所を at にする（知らない船は足す）。
func (n *noticer) setFerryAt(id string, at uint64) {
	if f := n.ferry(id); f != nil {
		f.at = at
		return
	}
	n.ferries = append(n.ferries, noticeFerry{id: id, at: at})
}

// island は島 ID が範囲（1〜島の数）にあれば添字と true を返す。
func (n *noticer) island(id uint64) (int, bool) {
	if id == 0 || id > uint64(len(n.roles)) {
		return 0, false
	}
	return int(id - 1), true
}

// resync は通知の判定の状態を Engine の Snapshot から作り直す（off は変えない）。
// つなぎ直したときに、Snapshot に入っている古い出来事の通知を出さないため。
func (n *noticer) resync(snap sim.Snapshot) {
	n.opening = false
	for i := range n.roles {
		n.roles[i] = noticeRoleFollower
		n.silentAt[i] = -1
	}
	for _, ns := range snap.Nodes {
		if i, ok := n.island(ns.ID); ok {
			n.roles[i] = ns.Role
		}
	}
	n.ferries = n.ferries[:0]
	for _, f := range snap.Ferries {
		n.ferries = append(n.ferries, noticeFerry{id: f.ID, at: f.IslandID})
	}
	n.reqs = make(map[string]*noticeReq, len(snap.Requests))
	for _, r := range snap.Requests {
		n.reqs[r.ID] = &noticeReq{
			origin:  r.Origin,
			settled: r.Status == noticeStatusSucceeded || r.Status == noticeStatusRejected,
			expired: r.Expired,
		}
	}
	for _, is := range snap.Islands {
		i, ok := n.island(is.ID)
		if !ok {
			continue
		}
		if n.silent(snap.T, is.LastDeliveredAt) {
			n.silentAt[i] = int64(is.LastDeliveredAt)
		}
	}
}

// silent は、時刻 t に、最後の配達 lastDeliveredAt から音信不通の時間がたっているか。
func (n *noticer) silent(t, lastDeliveredAt sim.SimTime) bool {
	return n.silenceAfterMs > 0 && int64(t-lastDeliveredAt) >= n.silenceAfterMs
}

// wireSeq は Engine の seq をワイヤの seq にする（engineSeq + off）。
func (n *noticer) wireSeq(engineSeq uint64) uint64 { return engineSeq + n.off }

// expand は evs の seq を振り直し、各イベントの直後にそのイベントから作った notice を差し込んだ列を返す。
// 入力のスライスは書き換えない。
func (n *noticer) expand(evs []sim.DomainEvent) []sim.DomainEvent {
	out := make([]sim.DomainEvent, 0, len(evs))
	for _, ev := range evs {
		ev.Seq += n.off
		out = append(out, ev)
		for _, d := range n.observe(ev) {
			out = append(out, sim.DomainEvent{
				Seq:  out[len(out)-1].Seq + 1,
				T:    ev.T,
				Type: d.EventType(),
				Data: d,
			})
			n.off++
		}
	}
	return out
}

// observe は ev から作る notice を返し（0 件以上）、通知の判定の状態を進める（ゲームデザイン §7.3）。
func (n *noticer) observe(ev sim.DomainEvent) []noticeData {
	switch d := ev.Data.(type) {
	case sim.PhaseChanged:
		n.opening = d.Phase == sim.PhaseOpening
	case sim.NodeState:
		i, ok := n.island(d.ID)
		if !ok {
			return nil
		}
		prev := n.roles[i]
		n.roles[i] = d.Role
		if d.Role == noticeRoleCandidate && prev != noticeRoleCandidate && !n.opening {
			return []noticeData{{IslandID: d.ID, Key: noticeNodeCampaign}}
		}
	case sim.LeaderElected:
		if _, ok := n.island(d.ID); ok {
			return []noticeData{{IslandID: d.ID, Key: noticeLeaderElected}}
		}
	case sim.SteppedDown:
		if _, ok := n.island(d.ID); ok && d.Reason == noticeReasonCheckQuorum {
			return []noticeData{{IslandID: d.ID, Key: noticeLeaderSteppedDown}}
		}
	case sim.Forecast:
		return n.forecast(d)
	case sim.FerryDeparted:
		n.setFerryAt(d.FerryID, 0)
	case sim.FerryArrived:
		n.setFerryAt(d.FerryID, d.IslandID)
	case sim.FerryBroken:
		return n.ferryBroken(d)
	case sim.RequestCreated:
		if _, ok := n.island(d.Request.Origin); !ok {
			return nil
		}
		n.reqs[d.Request.ID] = &noticeReq{origin: d.Request.Origin}
		return []noticeData{{IslandID: d.Request.Origin, Key: noticeRequestCreated, Params: noticeParams{ReqID: d.Request.ID}}}
	case sim.RequestUpdated:
		return n.requestUpdated(d)
	case sim.IslandUpdated:
		i, ok := n.island(d.ID)
		if !ok || !n.silent(ev.T, d.LastDeliveredAt) || n.silentAt[i] == int64(d.LastDeliveredAt) {
			return nil
		}
		n.silentAt[i] = int64(d.LastDeliveredAt)
		return []noticeData{{IslandID: d.ID, Key: noticeIslandSilent}}
	}
	return nil
}

// forecast は予報の notice。event が storm なら嵐の予報、rush なら注文ラッシュの予報（rushForecast）。ほかは出さない。
// 嵐の予報で話す島は、読める航路の ID（"A-B"）の A のうち最小のもの。
func (n *noticer) forecast(d sim.Forecast) []noticeData {
	switch d.Event {
	case noticeForecastStorm:
	case noticeForecastRush:
		return n.rushForecast(d)
	default:
		return nil
	}
	var speaker uint64
	for _, id := range d.RouteIDs {
		a, ok := n.routeStart(id)
		if ok && (speaker == 0 || a < speaker) {
			speaker = a
		}
	}
	if speaker == 0 {
		return nil
	}
	at := int64(d.At)
	return []noticeData{{
		IslandID: speaker,
		Key:      noticeStormForecast,
		Params:   noticeParams{RouteIDs: d.RouteIDs, At: &at},
	}}
}

// rushForecast は注文ラッシュの予報の notice。話す島は、islandIds のうち範囲にある最小の島（無ければ出さない）。
func (n *noticer) rushForecast(d sim.Forecast) []noticeData {
	var speaker uint64
	for _, id := range d.IslandIDs {
		if _, ok := n.island(id); ok && (speaker == 0 || id < speaker) {
			speaker = id
		}
	}
	if speaker == 0 {
		return nil
	}
	at := int64(d.At)
	return []noticeData{{
		IslandID: speaker,
		Key:      noticeRushForecast,
		Params:   noticeParams{At: &at},
	}}
}

// ferryBroken は船の故障の notice。話す島は、その船が停泊している島（読めなければ出さない）。
// 延びた故障（同じ船の 2 回目の ferry.broken）でも出す。
func (n *noticer) ferryBroken(d sim.FerryBroken) []noticeData {
	f := n.ferry(d.FerryID)
	if f == nil {
		return nil
	}
	if _, ok := n.island(f.at); !ok {
		return nil
	}
	until := int64(d.Until)
	return []noticeData{{
		IslandID: f.at,
		Key:      noticeFerryBroken,
		Params:   noticeParams{FerryID: d.FerryID, Until: &until},
	}}
}

// routeStart は航路の ID "A-B"（10 進の正の整数 2 つ）の A を返す。読めない・島の範囲の外なら false。
func (n *noticer) routeStart(id string) (uint64, bool) {
	as, bs, ok := strings.Cut(id, "-")
	if !ok {
		return 0, false
	}
	a, errA := strconv.ParseUint(as, 10, 64)
	b, errB := strconv.ParseUint(bs, 10, 64)
	if errA != nil || errB != nil {
		return 0, false
	}
	if _, ok := n.island(a); !ok {
		return 0, false
	}
	if _, ok := n.island(b); !ok {
		return 0, false
	}
	return a, true
}

// requestUpdated は依頼の期限切れ・確定・予約失敗の notice。期限切れを先に出す。
func (n *noticer) requestUpdated(d sim.RequestUpdated) []noticeData {
	r, ok := n.reqs[d.ID]
	if !ok {
		return nil
	}
	var out []noticeData
	if d.Expired && !r.expired {
		r.expired = true
		out = append(out, noticeData{IslandID: r.origin, Key: noticeRequestExpired, Params: noticeParams{ReqID: d.ID}})
	}
	if !r.settled {
		key := ""
		switch d.Status {
		case noticeStatusSucceeded:
			key = noticeRequestSucceeded
		case noticeStatusRejected:
			key = noticeRequestRejected
		}
		if key != "" {
			r.settled = true
			out = append(out, noticeData{IslandID: r.origin, Key: key, Params: noticeParams{ReqID: d.ID}})
		}
	}
	return out
}
