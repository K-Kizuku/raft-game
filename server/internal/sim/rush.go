package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

// forecastRush は Forecast.Event のラッシュの値。
const forecastRush = "rush"

// ラッシュのスケジューラのイベントの種類。
// engine.go の iota（1〜）・score.go（300〜）・storm.go（400〜）・breakdown.go（500〜）と重ならないよう 600 から振る。
// 重なれば handleEvent の switch がコンパイルエラーになる。
const (
	kindRushForecast EventKind = 600 + iota // ClassWorldEvent。Data は帳簿の添字 int
)

// rushBook はラッシュの帳簿。台本のラッシュは New で全部を決める。
// 依頼そのものは依頼の帳簿（request.go）に台本の依頼として入るので、ここには予報だけを持つ。
type rushBook struct {
	list []rushEntry
}

// rushEntry はラッシュ 1 つの帳簿の行。
type rushEntry struct {
	r            game.Rush
	forecastDone bool // HasForecast でなければ最初から true
}

// planRushes は台本のラッシュの時刻・島・種類を決めて帳簿に入れ、PlanRequests に渡す依頼の設定を返す（New の中）。
// ラッシュがなければ乱数列を作らず、e.cfg.Requests をそのまま返す。
// ラッシュがあれば、e.cfg.Requests の複製の Script の後ろに、各ラッシュの依頼を定義の順に足して返す（e.cfg.Requests は変えない）。
func (e *Engine) planRushes(seed uint64) (game.RequestConfig, error) {
	e.cfg.Rushes = slices.Clone(e.cfg.Rushes)
	if len(e.cfg.Rushes) == 0 {
		return e.cfg.Requests, nil
	}
	rnd := e.cfg.Requests.Random
	plan, err := game.PlanRush(e.cfg.Rushes, rnd.Weights, rnd.Kinds, int64(e.cfg.DurationMs), Sub(seed, StreamRush))
	if err != nil {
		return game.RequestConfig{}, fmt.Errorf("sim: rushes: %w", err)
	}
	rc := e.cfg.Requests.Clone()
	e.rushes.list = make([]rushEntry, len(plan))
	for i, r := range plan {
		e.rushes.list[i] = rushEntry{r: r, forecastDone: !r.HasForecast}
		rc.Script = append(rc.Script, r.Requests...)
	}
	return rc, nil
}

// startRushes は帳簿のラッシュの予報を定義の順にスケジューラに積む（cmd.game.start）。
func (e *Engine) startRushes() {
	for i, en := range e.rushes.list {
		if en.r.HasForecast {
			e.push(SimTime(en.r.ForecastAt), ClassWorldEvent, kindRushForecast, i)
		}
	}
}

// rushForecast はラッシュ i の予報。
func (e *Engine) rushForecast(i int) {
	en := &e.rushes.list[i]
	en.forecastDone = true
	e.emit(Forecast{Event: forecastRush, At: SimTime(en.r.At), RouteIDs: []string{}, IslandIDs: slices.Clone(en.r.Islands)})
}

// nextRushAt は、まだ出していないラッシュの予報のうち、DurationMs 以下でいちばん早い時刻を返す（NextNotableAt が使う）。
// ラッシュの依頼の発生は nextRequestAt が含む。
func (e *Engine) nextRushAt() (SimTime, bool) {
	var best SimTime
	found := false
	for _, en := range e.rushes.list {
		t := SimTime(en.r.ForecastAt)
		if en.forecastDone || t > e.cfg.DurationMs {
			continue
		}
		if !found || t < best {
			best, found = t, true
		}
	}
	return best, found
}
