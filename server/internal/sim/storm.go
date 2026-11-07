package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// 嵐のイベント（プロトコル §6）。
const (
	EvForecast       EventType = "forecast"
	EvRouteBlocked   EventType = "route.blocked"
	EvRouteUnblocked EventType = "route.unblocked"
)

// Forecast は forecast（嵐の予報）。
type Forecast struct {
	Event    string   `json:"event"`    // "storm"（P1 の rush などもこの形にする）
	At       SimTime  `json:"at"`       // 嵐の始まる時刻
	RouteIDs []string `json:"routeIds"` // 嵐の航路（定義の順）。nil にしない
}

// RouteBlocked は route.blocked（遮断が始まった・延びた）。
type RouteBlocked struct {
	RouteIDs []string `json:"routeIds"` // 遮断が延びた航路（定義の順）
	Until    SimTime  `json:"until"`    // この時刻から出航できる
}

// RouteUnblocked は route.unblocked（遮断が終わった）。
type RouteUnblocked struct {
	RouteIDs []string `json:"routeIds"`
	Until    SimTime  `json:"until"` // 遮断が終わった時刻（= このイベントの t）
}

func (Forecast) EventType() EventType       { return EvForecast }
func (RouteBlocked) EventType() EventType   { return EvRouteBlocked }
func (RouteUnblocked) EventType() EventType { return EvRouteUnblocked }

// forecastStorm は Forecast.Event の嵐の値。
const forecastStorm = "storm"

// 嵐のスケジューラのイベントの種類。
// engine.go の iota（1〜）と重ならないよう 400 から振る（T-203 は 300 から）。重なれば handleEvent の switch がコンパイルエラーになる。
const (
	kindStormForecast EventKind = 400 + iota // ClassWorldEvent。Data は帳簿の添字 int
	kindStormStart                           // ClassWorldEvent。Data は帳簿の添字 int
	kindStormEnd                             // ClassWorldEvent。Data は帳簿の添字 int
)

// stormBook は嵐の帳簿。台本の嵐は New で全部を決める。
type stormBook struct {
	list        []stormEntry
	unblockedAt []SimTime // 添字 = e.tr.Routes() の添字。その航路の route.unblocked を出した時刻。初期値 -1
}

// stormEntry は嵐 1 つの帳簿の行。
type stormEntry struct {
	s            game.Storm
	ids          []string // routeIds（s.Routes の順。ferry.Route の ID の形 "A-B"、A < B）
	idx          []int    // s.Routes の各組の、e.tr.Routes() の添字
	forecastDone bool     // HasForecast でなければ最初から true
	startDone    bool
	endDone      bool
}

// initStorms は台本の嵐の時刻を決めて帳簿に入れる（New の中）。嵐がなければ乱数を引かない。
func (e *Engine) initStorms(seed uint64) error {
	e.cfg.Storms = game.CloneStormDefs(e.cfg.Storms)
	if len(e.cfg.Storms) == 0 {
		return nil
	}
	plan, err := game.PlanStorms(e.cfg.Storms, e.cfg.Islands, int64(e.cfg.DurationMs), Sub(seed, StreamEvents))
	if err != nil {
		return fmt.Errorf("sim: storms: %w", err)
	}
	for i, s := range plan {
		if _, err := e.addStorm(s); err != nil {
			return fmt.Errorf("sim: storms[%d]: %w", i, err)
		}
	}
	return nil
}

// addStorm は s を帳簿の末尾に足して添字を返す。航路は向きを問わずに e.tr.Routes() から線形に探す。
// 見つからない組があれば ferry.ErrNoRoute を包んで返し、何も変えない。
func (e *Engine) addStorm(s game.Storm) (int, error) {
	routes := e.tr.Routes()
	ids := make([]string, 0, len(s.Routes))
	idx := make([]int, 0, len(s.Routes))
	for _, p := range s.Routes {
		a, b := min(p[0], p[1]), max(p[0], p[1])
		k := slices.IndexFunc(routes, func(r ferry.Route) bool { return r.A == a && r.B == b })
		if k < 0 {
			return 0, fmt.Errorf("%w: %d-%d", ferry.ErrNoRoute, p[0], p[1])
		}
		ids = append(ids, routes[k].ID())
		idx = append(idx, k)
	}
	if e.storms.unblockedAt == nil {
		e.storms.unblockedAt = make([]SimTime, len(routes))
		for k := range e.storms.unblockedAt {
			e.storms.unblockedAt[k] = -1
		}
	}
	s.Routes = slices.Clone(s.Routes)
	e.storms.list = append(e.storms.list, stormEntry{
		s:            s,
		ids:          ids,
		idx:          idx,
		forecastDone: !s.HasForecast,
	})
	return len(e.storms.list) - 1, nil
}

// startStorms は帳簿の嵐をスケジューラに積む（cmd.game.start）。
// 予報・開始・終わりの 3 回に分けて積むので、同じ時刻では予報 → 開始 → 終わり（それぞれ定義の順）になる。
func (e *Engine) startStorms() {
	for i, st := range e.storms.list {
		if st.s.HasForecast {
			e.push(SimTime(st.s.ForecastAt), ClassWorldEvent, kindStormForecast, i)
		}
	}
	for i, st := range e.storms.list {
		e.push(SimTime(st.s.Start), ClassWorldEvent, kindStormStart, i)
	}
	// DurationMs より後でも積む（ラウンドが先に終わるので処理されない）。
	for i, st := range e.storms.list {
		e.push(SimTime(st.s.End), ClassWorldEvent, kindStormEnd, i)
	}
}

// handleStorm は嵐のスケジューラのイベントを処理する。
func (e *Engine) handleStorm(ev Event) {
	i := ev.Data.(int)
	switch ev.Kind {
	case kindStormForecast:
		e.stormForecast(i)
	case kindStormStart:
		e.stormStart(i)
	case kindStormEnd:
		e.stormEnd(i)
	default:
		panic(fmt.Sprintf("sim: unknown storm event kind %d", ev.Kind))
	}
}

// stormForecast は嵐 i の予報。
func (e *Engine) stormForecast(i int) {
	st := &e.storms.list[i]
	st.forecastDone = true
	e.emit(Forecast{Event: forecastStorm, At: SimTime(st.s.Start), RouteIDs: slices.Clone(st.ids)})
}

// stormStart は嵐 i の開始。航路を遮断し、遮断が延びた航路だけを route.blocked にする。
// 航行中の船はそのまま進む（Block は出航の条件だけを変える）。
func (e *Engine) stormStart(i int) {
	st := &e.storms.list[i]
	st.startDone = true
	before := e.tr.Routes()
	if err := e.tr.Block(st.s.Routes, st.s.End); err != nil {
		panic(fmt.Sprintf("sim: storm %d: block: %v", i, err))
	}
	after := e.tr.Routes()
	var ids []string
	for j, k := range st.idx {
		if after[k].BlockedUntil != before[k].BlockedUntil {
			ids = append(ids, st.ids[j])
		}
	}
	if len(ids) > 0 {
		e.emit(RouteBlocked{RouteIDs: ids, Until: SimTime(st.s.End)})
	}
}

// stormEnd は嵐 i の終わり。遮断が今ちょうど終わった航路だけを、1 回だけ route.unblocked にする。
// 出航できるようになるのは ferry の判定（now < BlockedUntil）のとおり End の時刻ちょうどで、これは知らせるだけ。
func (e *Engine) stormEnd(i int) {
	st := &e.storms.list[i]
	st.endDone = true
	now := e.Now()
	routes := e.tr.Routes()
	var ids []string
	for j, k := range st.idx {
		if SimTime(routes[k].BlockedUntil) == now && e.storms.unblockedAt[k] != now {
			e.storms.unblockedAt[k] = now
			ids = append(ids, st.ids[j])
		}
	}
	if len(ids) > 0 {
		e.emit(RouteUnblocked{RouteIDs: ids, Until: now})
	}
}

// raiseStorm は時刻の決まった嵐 s を加える。s.Start == Now() なら、その場で遮断して route.blocked を出し、
// 終わりを積む（予報は出さない）。s.Start > Now() なら、予報（HasForecast のとき）・開始・終わりを積む。
// s.Start < Now()・s.End <= s.Start・s.Routes が空なら game.ErrInvalidStorm、航路がなければ ferry.ErrNoRoute を包んで返し、何も変えない。
// 予報の時刻が Now() より前か s.Start より後のときも game.ErrInvalidStorm（積めないため）。
// 航行中でなければ誤り（cmd.game.start の startStorms と二重に積まないため）。
// 後処理（afterStep）は呼び出し側。
func (e *Engine) raiseStorm(s game.Storm) (int, error) {
	now := e.Now()
	switch {
	case e.phase != PhaseSailing:
		return 0, fmt.Errorf("sim: raiseStorm in phase %s", e.phase)
	case SimTime(s.Start) < now:
		return 0, fmt.Errorf("%w: start %d < now %d", game.ErrInvalidStorm, s.Start, now)
	case s.End <= s.Start:
		return 0, fmt.Errorf("%w: end %d <= start %d", game.ErrInvalidStorm, s.End, s.Start)
	case len(s.Routes) == 0:
		return 0, fmt.Errorf("%w: routes: empty", game.ErrInvalidStorm)
	case s.HasForecast && SimTime(s.Start) > now && (SimTime(s.ForecastAt) < now || s.ForecastAt > s.Start):
		return 0, fmt.Errorf("%w: forecastAt %d is not in %d..%d", game.ErrInvalidStorm, s.ForecastAt, now, s.Start)
	}
	i, err := e.addStorm(s)
	if err != nil {
		return 0, fmt.Errorf("sim: raiseStorm: %w", err)
	}
	st := &e.storms.list[i]
	if SimTime(s.Start) == now {
		st.forecastDone = true
		e.stormStart(i)
	} else {
		if s.HasForecast {
			e.push(SimTime(s.ForecastAt), ClassWorldEvent, kindStormForecast, i)
		}
		e.push(SimTime(s.Start), ClassWorldEvent, kindStormStart, i)
	}
	e.push(SimTime(s.End), ClassWorldEvent, kindStormEnd, i)
	return i, nil
}

// nextStormAt は、帳簿の嵐のまだ処理していない予報・開始・終わりのうち、DurationMs 以下でいちばん早い時刻を返す（NextNotableAt が使う）。
func (e *Engine) nextStormAt() (SimTime, bool) {
	var best SimTime
	found := false
	consider := func(done bool, at int64) {
		t := SimTime(at)
		if done || t > e.cfg.DurationMs {
			return
		}
		if !found || t < best {
			best, found = t, true
		}
	}
	for _, st := range e.storms.list {
		consider(st.forecastDone, st.s.ForecastAt)
		consider(st.startDone, st.s.Start)
		consider(st.endDone, st.s.End)
	}
	return best, found
}
