package loadmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime/debug"
	"slices"
	"syscall"
	"time"
)

// 引数の範囲（spike/worker/limits.ts と同じ値）。
const (
	MinNodes, MaxNodes                     = 1, 9
	MinMinutes, MaxMinutes                 = 1, 60
	MinProposalsPerSec, MaxProposalsPerSec = 0, 20
	MinSeconds, MaxSeconds                 = 1, 60
	MinEventsPerFlush, MaxEventsPerFlush   = 0, 1000

	DefaultNodes           = 7
	DefaultMinutes         = 30
	DefaultProposalsPerSec = 2
	DefaultSpeed           = "8"
	DefaultSeconds         = 20
	DefaultEventsPerFlush  = 20

	// LoopStep はセッションループの 1 ステップの実時間（DesignDoc §12.4）。
	LoopStep = 50 * time.Millisecond
	// FlushEvery はイベントをまとめて直列化する間隔（DesignDoc §12.4）。
	FlushEvery = 100 * time.Millisecond
)

// Speeds は受け付ける倍速と、50ms のステップで進めるゲーム内の時間（ミリ秒）。
var Speeds = map[string]int64{"0.5": 25, "1": 50, "2": 100, "4": 200, "8": 400}

// RaftConfig は /bench/raft の引数。
type RaftConfig struct {
	Nodes           int
	Minutes         int
	ProposalsPerSec int
}

// Validate は範囲を確かめる。
func (c RaftConfig) Validate() error {
	if err := inRange("nodes", c.Nodes, MinNodes, MaxNodes); err != nil {
		return err
	}
	if err := inRange("minutes", c.Minutes, MinMinutes, MaxMinutes); err != nil {
		return err
	}
	return inRange("proposalsPerSec", c.ProposalsPerSec, MinProposalsPerSec, MaxProposalsPerSec)
}

// LoopConfig は /bench/loop の引数。
type LoopConfig struct {
	Nodes           int
	Speed           string
	Seconds         int
	ProposalsPerSec int
	EventsPerFlush  int
}

// Validate は範囲を確かめる。
func (c LoopConfig) Validate() error {
	if err := inRange("nodes", c.Nodes, MinNodes, MaxNodes); err != nil {
		return err
	}
	if _, ok := Speeds[c.Speed]; !ok {
		return fmt.Errorf("speed must be one of 0.5, 1, 2, 4, 8, got %q", c.Speed)
	}
	if err := inRange("seconds", c.Seconds, MinSeconds, MaxSeconds); err != nil {
		return err
	}
	if err := inRange("proposalsPerSec", c.ProposalsPerSec, MinProposalsPerSec, MaxProposalsPerSec); err != nil {
		return err
	}
	return inRange("eventsPerFlush", c.EventsPerFlush, MinEventsPerFlush, MaxEventsPerFlush)
}

func inRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s must be in %d..%d, got %d", name, lo, hi, v)
	}
	return nil
}

// RaftResult は /bench/raft の結果（M-2）。
type RaftResult struct {
	Nodes                   int     `json:"nodes"`
	Minutes                 int     `json:"minutes"`
	WallMs                  float64 `json:"wallMs"`
	CPUUserMs               float64 `json:"cpuUserMs"`
	CPUSysMs                float64 `json:"cpuSysMs"`
	VirtualMinutesPerSecond float64 `json:"virtualMinutesPerSecond"`
	Committed               int     `json:"committed"`
	LeaderChanges           int     `json:"leaderChanges"`

	Proposed int `json:"-"` // テスト用（JSON には出さない）
}

// RunRaft は、ゲーム内 Minutes 分を実時間を待たずに一気に進める（復元の再計算の模型）。
func RunRaft(cfg RaftConfig) (RaftResult, error) {
	if err := cfg.Validate(); err != nil {
		return RaftResult{}, err
	}
	c, err := NewCluster(cfg.Nodes, cfg.ProposalsPerSec)
	if err != nil {
		return RaftResult{}, err
	}
	u0, s0 := rusage()
	t0 := time.Now()
	total := int64(cfg.Minutes) * 60_000
	for done := int64(0); done < total; done += TickMs {
		if err := c.Advance(TickMs); err != nil {
			return RaftResult{}, err
		}
	}
	wall := time.Since(t0)
	u1, s1 := rusage()
	wallMs := ms(wall)
	vmps := 0.0
	if wall > 0 {
		vmps = round3(float64(cfg.Minutes) / wall.Seconds())
	}
	return RaftResult{
		Nodes:                   cfg.Nodes,
		Minutes:                 cfg.Minutes,
		WallMs:                  wallMs,
		CPUUserMs:               ms(u1 - u0),
		CPUSysMs:                ms(s1 - s0),
		VirtualMinutesPerSecond: vmps,
		Committed:               c.Committed(),
		LeaderChanges:           c.LeaderChanges(),
		Proposed:                c.Proposed(),
	}, nil
}

// Stats は p50・p99・最大（ミリ秒）。
type Stats struct {
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// LateStats は遅れの p99・最大（ミリ秒）。
type LateStats struct {
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// GCStats は GC の回数と停止の最大（ミリ秒）。
type GCStats struct {
	Num        int64   `json:"num"`
	PauseMaxMs float64 `json:"pauseMaxMs"`
}

// LoopResult は /bench/loop の結果（M-3）。
type LoopResult struct {
	Speed   string    `json:"speed"`
	Seconds int       `json:"seconds"`
	Steps   int       `json:"steps"`
	StepMs  Stats     `json:"stepMs"`
	LateMs  LateStats `json:"lateMs"`
	CPUUtil float64   `json:"cpuUtil"`
	GC      GCStats   `json:"gc"`
}

// fakeEvent は直列化の負荷のための偽のイベント（DesignDoc §12.4 の形を真似る）。
type fakeEvent struct {
	Type  string `json:"type"`
	Seq   int    `json:"seq"`
	At    int64  `json:"at"`
	Node  int    `json:"node"`
	Term  int    `json:"term"`
	Index int    `json:"index"`
	Note  string `json:"note"`
}

// RunLoop は、50ms の Ticker ごとにゲーム内 50 × speed ms を進め、100ms ごとに eventsPerFlush 件の
// 偽のイベントを encoding/json で直列化するループを、Seconds 秒だけ実時間で動かす（8 倍速の余裕の模型）。
func RunLoop(ctx context.Context, cfg LoopConfig) (LoopResult, error) {
	if err := cfg.Validate(); err != nil {
		return LoopResult{}, err
	}
	c, err := NewCluster(cfg.Nodes, cfg.ProposalsPerSec)
	if err != nil {
		return LoopResult{}, err
	}
	virtualStep := Speeds[cfg.Speed]
	events := make([]fakeEvent, cfg.EventsPerFlush)

	var gc0 debug.GCStats
	debug.ReadGCStats(&gc0)
	u0, s0 := rusage()
	start := time.Now()
	deadline := start.Add(time.Duration(cfg.Seconds) * time.Second)
	ticker := time.NewTicker(LoopStep)
	defer ticker.Stop()

	var stepMs, lateMs []float64
	lastFlush := start
	seq := 0
	for k := 1; ; k++ {
		select {
		case <-ctx.Done():
			return LoopResult{}, ctx.Err()
		case <-ticker.C:
		}
		t := time.Now()
		if !t.Before(deadline) {
			break
		}
		late := max(t.Sub(start.Add(time.Duration(k)*LoopStep)), 0)
		if err := c.Advance(virtualStep); err != nil {
			return LoopResult{}, err
		}
		if t.Sub(lastFlush) >= FlushEvery {
			lastFlush = t
			for i := range events {
				seq++
				events[i] = fakeEvent{Type: "letter.delivered", Seq: seq, At: t.UnixMilli(), Node: i%cfg.Nodes + 1, Term: 3, Index: seq, Note: "spike"}
			}
			if _, err := json.Marshal(events); err != nil {
				return LoopResult{}, err
			}
		}
		stepMs = append(stepMs, ms(time.Since(t)))
		lateMs = append(lateMs, ms(late))
	}
	wall := time.Since(start)
	u1, s1 := rusage()
	var gc1 debug.GCStats
	debug.ReadGCStats(&gc1)

	newGC := gc1.NumGC - gc0.NumGC
	var pauseMax time.Duration
	for i := 0; int64(i) < newGC && i < len(gc1.Pause); i++ {
		pauseMax = max(pauseMax, gc1.Pause[i])
	}
	util := 0.0
	if wall > 0 {
		util = round3(float64(u1-u0+s1-s0) / float64(wall))
	}
	sorted := slices.Clone(stepMs)
	slices.Sort(sorted)
	lateSorted := slices.Clone(lateMs)
	slices.Sort(lateSorted)
	return LoopResult{
		Speed:   cfg.Speed,
		Seconds: cfg.Seconds,
		Steps:   len(stepMs),
		StepMs:  Stats{P50: Percentile(sorted, 50), P99: Percentile(sorted, 99), Max: Percentile(sorted, 100)},
		LateMs:  LateStats{P99: Percentile(lateSorted, 99), Max: Percentile(lateSorted, 100)},
		CPUUtil: util,
		GC:      GCStats{Num: newGC, PauseMaxMs: ms(pauseMax)},
	}, nil
}

// Percentile は、昇順に並んだ値の p パーセンタイル（最近傍の順位）を返す。空なら 0。
func Percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(float64(p) / 100 * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	return sorted[rank-1]
}

func rusage() (user, sys time.Duration) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	return time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano())
}

func ms(d time.Duration) float64 { return round3(float64(d) / float64(time.Millisecond)) }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
