package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// スコア・満足度のイベント（プロトコル §6）。
const (
	EvIslandUpdated EventType = "island.updated"
	EvScoreUpdated  EventType = "score.updated"
)

// スケジューラのイベントの種類（採点）。
// engine.go の iota（1〜）と重ならないよう 300 から振る（T-204 は 400 から）。重なれば handleEvent の switch がコンパイルエラーになる。
const (
	kindRequestDeadline EventKind = 300 + iota // ClassGameTimer。Data は帳簿の添字 int
	kindSilenceCheck                           // ClassGameTimer。Data は silenceTimer
)

// IslandState は島の状態（プロトコル §5.1 の islandsState[]）。
type IslandState struct {
	ID              uint64  `json:"id"`
	Satisfaction    int64   `json:"satisfaction"`
	LastDeliveredAt SimTime `json:"lastDeliveredAt"` // 最後に手紙が配達された時刻。まだなら 0（開会式）
}

// IslandUpdated は island.updated（満足度か最後の配達の時刻が変わった）。
type IslandUpdated struct {
	ID              uint64  `json:"id"`
	Satisfaction    int64   `json:"satisfaction"`
	LastDeliveredAt SimTime `json:"lastDeliveredAt"`
}

// ScoreUpdated は score.updated。
type ScoreUpdated struct {
	Total           int64    `json:"total"`
	Successes       int64    `json:"successes"`
	CriticalSecured []string `json:"criticalSecured"` // 資源の定義の順。nil にしない
}

// ScoreState はスコアの状態（プロトコル §5.1 の score）。
type ScoreState struct {
	Total           int64    `json:"total"`
	Successes       int64    `json:"successes"`
	Target          int64    `json:"target"` // ScoreConfig.TargetSuccesses
	CriticalSecured []string `json:"criticalSecured"`
}

func (IslandUpdated) EventType() EventType { return EvIslandUpdated }
func (ScoreUpdated) EventType() EventType  { return EvScoreUpdated }

// scoreBook は採点の帳簿（ゲームデザイン §5）。on が false なら、採点の処理はすべて何もしない。
type scoreBook struct {
	on        bool
	total     int64
	breakdown ScoreBreakdown // 合計は常に total と等しい
	successes int64          // 確定（succeeded）した依頼の数（期限を過ぎてからのものを含む）
	rejected  int64
	expired   int64         // 期限切れのフラグが立った依頼の数
	critical  []string      // 重要な資源の ID（e.cfg.Requests.Critical の順）
	secured   []bool        // critical と同じ添字
	settled   []bool        // e.reqs.list と同じ添字。精算済み
	islands   []islandScore // 添字 = ID - 1
	last      ScoreUpdated  // 最後に出した（まだなら初期の）score.updated の値
	endQueued bool          // ラウンドの終わりを積んだ（★2 で終える。demo_end.go）か、終わりの精算を始めた
}

// islandScore は島 1 つの満足度と配達の記録。
type islandScore struct {
	sat           int64
	lastDelivered SimTime
	gen           uint64      // 音信不通のタイマーの世代
	last          IslandState // 最後に出した（まだなら初期の）island.updated の値
}

// silenceTimer は kindSilenceCheck の Data。
type silenceTimer struct {
	island uint64
	gen    uint64
}

// initScore は採点の設定を検査し、帳簿を作る（New の中。依頼の帳簿を作った後）。
// 設定がゼロ値なら帳簿をゼロ値のまま（on == false）にする。
func (e *Engine) initScore() error {
	sc := e.cfg.Score
	if err := sc.Validate(); err != nil {
		return fmt.Errorf("sim: score: %w", err)
	}
	if !sc.Enabled() {
		return nil
	}
	b := scoreBook{
		on:       true,
		critical: slices.Clone(e.cfg.Requests.Critical),
		settled:  make([]bool, len(e.reqs.list)),
		islands:  make([]islandScore, e.cfg.Islands),
		last:     ScoreUpdated{CriticalSecured: []string{}},
	}
	if b.critical == nil {
		b.critical = []string{}
	}
	b.secured = make([]bool, len(b.critical))
	for i := range b.islands {
		id := uint64(i + 1)
		b.islands[i] = islandScore{
			sat:  sc.SatInitial,
			last: IslandState{ID: id, Satisfaction: sc.SatInitial},
		}
	}
	e.score = b
	return nil
}

// startScore は期限切れと音信不通のタイマーを積む（cmd.game.start の最後）。
// 同じ時刻の ClassGameTimer は Seq の順なので、ラウンドの終わり → 期限切れ（ID の順）→ 音信不通（島の順）になる。
func (e *Engine) startScore() {
	if !e.score.on {
		return
	}
	// DurationMs 以降でも積む（ラウンドが先に終わるので処理されない）。
	for i, r := range e.reqs.list {
		e.push(SimTime(r.req.Deadline), ClassGameTimer, kindRequestDeadline, i)
	}
	for i := range e.score.islands {
		id := uint64(i + 1)
		e.push(SimTime(e.cfg.Score.SilenceAfterMs), ClassGameTimer, kindSilenceCheck, silenceTimer{island: id})
	}
}

// handleScoreTimer は採点のタイマー（kindRequestDeadline・kindSilenceCheck）を処理する。
func (e *Engine) handleScoreTimer(ev Event) {
	switch ev.Kind {
	case kindRequestDeadline:
		e.expireRequest(ev.Data.(int))
	case kindSilenceCheck:
		e.checkSilence(ev.Data.(silenceTimer))
	default:
		panic(fmt.Sprintf("sim: unknown score timer kind %d", ev.Kind))
	}
}

// expireRequest は依頼 i の期限（ゲームデザイン §4.3）。依頼元でまだ適用されていなければ、期限切れのフラグを立てる。
// request.updated は同じステップの後処理で refreshRequests が出す。
func (e *Engine) expireRequest(i int) {
	if !e.score.on {
		return
	}
	entry := &e.reqs.list[i]
	if entry.track.Result != nil || entry.expired {
		return
	}
	entry.expired = true
	p := e.cfg.Score.ExpiredPenalty
	e.score.total -= p
	e.score.breakdown.Expired -= p
	e.score.expired++
	e.addSatisfaction(entry.req.Origin, -e.cfg.Score.SatExpiredPenalty)
}

// checkSilence は音信不通の判定。その後に配達があった（世代が違う）なら何もしない。
func (e *Engine) checkSilence(t silenceTimer) {
	if !e.score.on {
		return
	}
	is := &e.score.islands[t.island-1]
	if t.gen != is.gen {
		return
	}
	e.addSatisfaction(t.island, -e.cfg.Score.SilenceAmount)
	e.push(e.Now()+SimTime(e.cfg.Score.SilenceEveryMs), ClassGameTimer, kindSilenceCheck, t)
}

// noteDelivered は、船が island でその島宛ての手紙を n 通降ろしたことを記録する（arrive の中）。
// n == 0（その島宛ての手紙がなかった）は配達ではない。
func (e *Engine) noteDelivered(island uint64, n int) {
	if !e.score.on || n == 0 {
		return
	}
	is := &e.score.islands[island-1]
	is.lastDelivered = e.Now()
	is.gen++
	e.push(e.Now()+SimTime(e.cfg.Score.SilenceAfterMs), ClassGameTimer, kindSilenceCheck, silenceTimer{island: island, gen: is.gen})
}

// chargeVoyage は 1 回の航行の運航コストを引く（cmd.ferry.depart が成功したとき）。travel は船の速度を入れた航行時間。
func (e *Engine) chargeVoyage(travel SimTime) {
	if !e.score.on {
		return
	}
	c := game.VoyageCost(e.cfg.Score, int64(travel))
	e.score.total -= c
	e.score.breakdown.FerryCost -= c
}

// finishScore はラウンドの終わりの精算（kindRoundEnd の最初）。公平ボーナスを足し、
// score.updated を phase.changed{ended} より先に出す。
func (e *Engine) finishScore() {
	if !e.score.on {
		return
	}
	e.score.endQueued = true
	m := int64(game.SatisfactionMax)
	for _, is := range e.score.islands {
		m = min(m, is.sat)
	}
	b := game.FairnessBonus(e.cfg.Score, m)
	e.score.total += b
	e.score.breakdown.Fairness = b
	e.refreshScore()
}

// refreshScore は、依頼元で適用された依頼を精算し、変わった島とスコアをイベントにする（ステップの後処理。
// refreshRequests の直後）。順は island.updated（島の ID の順）→ score.updated。
func (e *Engine) refreshScore() {
	if !e.score.on {
		return
	}
	sc := e.cfg.Score
	b := &e.score
	for i := range e.reqs.list {
		entry := &e.reqs.list[i]
		if b.settled[i] || entry.track.Result == nil {
			continue
		}
		b.settled[i] = true
		ok := entry.track.Result.Status == ledger.StatusOK
		p := game.RequestPoints(sc, entry.req.Critical, !entry.expired, ok)
		b.total += p
		b.breakdown.Requests += p
		if ok {
			b.successes++
			if entry.req.Critical {
				if k := slices.Index(b.critical, entry.req.Resource); k >= 0 {
					b.secured[k] = true
				}
			}
			e.addSatisfaction(entry.req.Origin, sc.SatSucceeded)
		} else {
			b.rejected++
			e.addSatisfaction(entry.req.Origin, -sc.SatRejectedPenalty)
		}
	}
	e.queueStar2End()

	for i := range b.islands {
		is := &b.islands[i]
		cur := IslandState{ID: uint64(i + 1), Satisfaction: is.sat, LastDeliveredAt: is.lastDelivered}
		if cur == is.last {
			continue
		}
		is.last = cur
		e.emit(IslandUpdated(cur))
	}

	cur := ScoreUpdated{Total: b.total, Successes: b.successes, CriticalSecured: e.criticalSecured()}
	if cur.Total == b.last.Total && cur.Successes == b.last.Successes && slices.Equal(cur.CriticalSecured, b.last.CriticalSecured) {
		return
	}
	b.last = cur
	e.emit(ScoreUpdated{Total: cur.Total, Successes: cur.Successes, CriticalSecured: slices.Clone(cur.CriticalSecured)})
}

// addSatisfaction は島の満足度に d を足し、0..SatisfactionMax に収める。
func (e *Engine) addSatisfaction(island uint64, d int64) {
	is := &e.score.islands[island-1]
	is.sat = game.ClampSatisfaction(is.sat + d)
}

// criticalSecured は確定した重要な資源の ID を、資源の定義の順に返す。nil にしない。
func (e *Engine) criticalSecured() []string {
	out := []string{}
	for k, id := range e.score.critical {
		if e.score.secured[k] {
			out = append(out, id)
		}
	}
	return out
}

// islandStates は島の状態を ID の順に返す（Snapshot）。採点しないときは nil。
func (e *Engine) islandStates() []IslandState {
	if !e.score.on {
		return nil
	}
	out := make([]IslandState, len(e.score.islands))
	for i, is := range e.score.islands {
		out[i] = IslandState{ID: uint64(i + 1), Satisfaction: is.sat, LastDeliveredAt: is.lastDelivered}
	}
	return out
}

// scoreState はスコアの状態を返す（Snapshot）。採点しないときは nil。
func (e *Engine) scoreState() *ScoreState {
	if !e.score.on {
		return nil
	}
	return &ScoreState{
		Total:           e.score.total,
		Successes:       e.score.successes,
		Target:          e.cfg.Score.TargetSuccesses,
		CriticalSecured: e.criticalSecured(),
	}
}
