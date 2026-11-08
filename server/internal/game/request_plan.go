package game

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// RequestConfig は依頼の設定（ステージ定義の requests.*）。値はすべてステージ定義から渡す。既定値を持たない。
// ゼロ値は「依頼なし」（Script が空で RateMilliPerMin が 0）として受け付ける。
type RequestConfig struct {
	StageID            string // 依頼の ID の接頭辞（プロトコル §1 の "<stageId>-<連番4桁>"）
	DeadlineNormalMs   int64  // requests.deadlineMs.normal
	DeadlineCriticalMs int64  // requests.deadlineMs.critical
	RetryMs            int64  // requests.retryMs
	Script             []ScriptRequest
	Random             RandomConfig
	Critical           []string // 重要な資源の ID（resources[].critical が true のもの。resources の順）
}

// ScriptRequest は台本の依頼（requests.script[]）。
type ScriptRequest struct {
	At       int64  // 発生の時刻（ゲーム内時間のミリ秒）
	Island   uint64 // 依頼元（ノード ID）
	Kind     string // ledger.KindFireworkReserve か ledger.KindStallReserve
	Resource string // 資源の ID
}

// RandomConfig はランダムな依頼の設定（requests.random、データ仕様 §4.2）。
type RandomConfig struct {
	RateMilliPerMin int64 // 0 ならランダムな依頼なし
	SlotMs          int64
	Weights         []int64 // 島ごとの重み w_i（千分率、合計 1000。添字 = ノード ID - 1。データ仕様 §4.1 で計算済み）
	Kinds           []RandomKind
}

// RandomKind はランダムな依頼の種類と重み（requests.random.kinds[]）。
type RandomKind struct {
	Kind     string
	Resource string
	Weight   int64
}

// ErrInvalidConfig は RequestConfig の誤り。PlanRequests のエラーはこれを包む。
var ErrInvalidConfig = errors.New("game: invalid request config")

// Clone はスライスを複製した値を返す。
func (c RequestConfig) Clone() RequestConfig {
	c.Script = slices.Clone(c.Script)
	c.Random.Weights = slices.Clone(c.Random.Weights)
	c.Random.Kinds = slices.Clone(c.Random.Kinds)
	c.Critical = slices.Clone(c.Critical)
	return c
}

// maxRateTimesSlot は rateMilliPerMin × slotMs の上限（p_i が 1,000,000 ppm を超えないため）。
const maxRateTimesSlot = 60_000_000

// PlanRequests は、ラウンドのすべての依頼を発生の前に決める（台本とランダム）。
// islands は島の数 N、durationMs はラウンドの長さ、resources は台帳の資源の定義。
// 返す列は発生の順で、ID は列の順に 1 から振る。依頼がなければ nil。
// rng は RateMilliPerMin > 0 のときだけ使う。
//
// 並べ方: 台本の依頼（書いた順）、その後ろにランダムな依頼（引いた順）を並べ、At で安定に並べ替える。
// 同じ時刻なら台本が先、ランダムは引いた順。
//
// ランダムな依頼の引く順序（データ仕様 §4.2）: 区間 k = [k×SlotMs, (k+1)×SlotMs) の順、島の ID の昇順に、
// 発生の判定（Int64N(1_000_000) < p）を必ず 1 回引く。発生するときだけ、続けて時刻（Int64N(SlotMs)）と
// 種類（Int64N(ΣWeight)）を引く。時刻が durationMs より後なら捨てる（引いた乱数はそのまま）。
func PlanRequests(c RequestConfig, islands int, durationMs int64, resources []ledger.ResourceDef, rng *rand.Rand) ([]Request, error) {
	if c.Random.RateMilliPerMin < 0 {
		return nil, invalid("random.rateMilliPerMin %d < 0", c.Random.RateMilliPerMin)
	}
	if len(c.Script) == 0 && c.Random.RateMilliPerMin == 0 {
		return nil, nil
	}
	if err := validate(c, islands, durationMs, resources); err != nil {
		return nil, err
	}

	type planned struct {
		at       int64
		island   uint64
		kind     string
		resource string
	}
	var all []planned
	for _, s := range c.Script {
		all = append(all, planned{at: s.At, island: s.Island, kind: s.Kind, resource: s.Resource})
	}
	if r := c.Random; r.RateMilliPerMin > 0 {
		var total int64
		for _, k := range r.Kinds {
			total += k.Weight
		}
		for k := int64(0); k*r.SlotMs < durationMs; k++ {
			for island := 1; island <= islands; island++ {
				p := spawnPPM(r.Weights[island-1], r.RateMilliPerMin, r.SlotMs)
				if rng.Int64N(1_000_000) >= p {
					continue
				}
				at := k*r.SlotMs + rng.Int64N(r.SlotMs)
				kind := r.Kinds[pickKind(r.Kinds, rng.Int64N(total))]
				if at > durationMs {
					continue
				}
				all = append(all, planned{at: at, island: uint64(island), kind: kind.Kind, resource: kind.Resource})
			}
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	slices.SortStableFunc(all, func(a, b planned) int { return cmp.Compare(a.at, b.at) })

	out := make([]Request, len(all))
	for i, p := range all {
		critical := slices.Contains(c.Critical, p.resource)
		deadline := p.at + c.DeadlineNormalMs
		if critical {
			deadline = p.at + c.DeadlineCriticalMs
		}
		out[i] = Request{
			ID:        ReqID(c.StageID, i+1),
			Origin:    p.island,
			Kind:      p.kind,
			Resource:  p.resource,
			Critical:  critical,
			CreatedAt: p.at,
			Deadline:  deadline,
		}
	}
	return out, nil
}

// validate は PlanRequests の検査（rateMilliPerMin の符号と「依頼なし」の判定の後）を、決まった順に行う。
func validate(c RequestConfig, islands int, durationMs int64, resources []ledger.ResourceDef) error {
	switch {
	case c.StageID == "":
		return invalid("stageId is empty")
	case c.DeadlineNormalMs < 1:
		return invalid("deadlineMs.normal %d < 1", c.DeadlineNormalMs)
	case c.DeadlineCriticalMs < 1:
		return invalid("deadlineMs.critical %d < 1", c.DeadlineCriticalMs)
	case c.RetryMs < 1:
		return invalid("retryMs %d < 1", c.RetryMs)
	}
	for i, id := range c.Critical {
		if _, ok := findResource(resources, id); !ok {
			return invalid("critical[%d] %q is not a resource", i, id)
		}
		if slices.Index(c.Critical, id) != i {
			return invalid("critical[%d] %q is duplicated", i, id)
		}
	}
	for i, s := range c.Script {
		path := fmt.Sprintf("script[%d]", i)
		switch {
		case s.At < 0 || s.At > durationMs:
			return invalid("%s.at %d is not in 0..%d", path, s.At, durationMs)
		case s.Island < 1 || s.Island > uint64(islands):
			return invalid("%s.island %d is not in 1..%d", path, s.Island, islands)
		}
		if err := checkKind(path, s.Kind, s.Resource, resources); err != nil {
			return err
		}
	}
	r := c.Random
	if r.RateMilliPerMin == 0 {
		return nil
	}
	switch {
	case r.SlotMs < 1:
		return invalid("random.slotMs %d < 1", r.SlotMs)
	case r.RateMilliPerMin > maxRateTimesSlot/r.SlotMs:
		return invalid("random.rateMilliPerMin %d × slotMs %d > %d", r.RateMilliPerMin, r.SlotMs, maxRateTimesSlot)
	case len(r.Weights) != islands:
		return invalid("random.weights has %d elements, want %d", len(r.Weights), islands)
	}
	var sum int64
	for i, w := range r.Weights {
		if w < 0 || w > 1000 {
			return invalid("random.weights[%d] %d is not in 0..1000", i, w)
		}
		sum += w
	}
	if sum != 1000 {
		return invalid("random.weights sum to %d, want 1000", sum)
	}
	if len(r.Kinds) == 0 {
		return invalid("random.kinds is empty")
	}
	for i, k := range r.Kinds {
		path := fmt.Sprintf("random.kinds[%d]", i)
		if err := checkKind(path, k.Kind, k.Resource, resources); err != nil {
			return err
		}
		if k.Weight < 1 {
			return invalid("%s.weight %d < 1", path, k.Weight)
		}
	}
	return nil
}

// checkKind は、種類が予約の 2 つのどちらかで、資源が定義にあり、種類が合うかを確かめる。
func checkKind(path, kind, resource string, resources []ledger.ResourceDef) error {
	var want ledger.ResourceKind
	switch kind {
	case ledger.KindFireworkReserve:
		want = ledger.ResourceFirework
	case ledger.KindStallReserve:
		want = ledger.ResourceStall
	default:
		return invalid("%s.kind %q is not %s or %s", path, kind, ledger.KindFireworkReserve, ledger.KindStallReserve)
	}
	def, ok := findResource(resources, resource)
	if !ok {
		return invalid("%s.resource %q is not a resource", path, resource)
	}
	if def.Kind != want {
		return invalid("%s.resource %q is %s, but kind %s needs %s", path, resource, def.Kind, kind, want)
	}
	return nil
}

func findResource(resources []ledger.ResourceDef, id string) (ledger.ResourceDef, bool) {
	i := slices.IndexFunc(resources, func(d ledger.ResourceDef) bool { return d.ID == id })
	if i < 0 {
		return ledger.ResourceDef{}, false
	}
	return resources[i], true
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}

// spawnPPM は、区間 1 つ・島 1 つで依頼が発生する確率（ppm）を返す（データ仕様 §4.2）。
// w × rate × slotMs / 60000（整数の割り算で切り捨て）。
func spawnPPM(w, rate, slotMs int64) int64 {
	return w * rate * slotMs / 60000
}

// pickKind は、x（0 ≤ x < ΣWeight）から種類の添字を選ぶ。
// kinds を順に見て、重みの累積が x より大きくなった最初の 1 つ。
func pickKind(kinds []RandomKind, x int64) int {
	var acc int64
	for i, k := range kinds {
		acc += k.Weight
		if acc > x {
			return i
		}
	}
	return len(kinds) - 1
}
