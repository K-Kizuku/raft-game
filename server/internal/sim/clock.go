package sim

import "github.com/K-Kizuku/raft-game/server/internal/coordcore"

// 仮想時計とスケジューラは CoordinatorCore（coordcore）に置いた（DTN-00 #363）。ここでは同じ型を別名で見せる。

// SimTime はゲーム内時間（ミリ秒）。0 = 航行開始（§3.1）。coordcore.SimTime の別名。
type SimTime = coordcore.SimTime

const (
	// Millisecond はゲーム内時間の 1 ミリ秒。
	Millisecond = coordcore.Millisecond
	// Second はゲーム内時間の 1 秒。
	Second = coordcore.Second
)

// EventClass は同じ時刻のイベントの処理順（§4.2）。coordcore.EventClass の別名。
type EventClass = coordcore.EventClass

const (
	ClassCommandApplied = coordcore.ClassCommandApplied // 0 プレイヤーのコマンド（記録用）
	ClassFerryArrive    = coordcore.ClassFerryArrive    // 1
	ClassFerryReady     = coordcore.ClassFerryReady     // 2
	ClassRaftTick       = coordcore.ClassRaftTick       // 3
	ClassWorldEvent     = coordcore.ClassWorldEvent     // 4
	ClassRequestSpawn   = coordcore.ClassRequestSpawn   // 5
	ClassRequestRetry   = coordcore.ClassRequestRetry   // 6
	ClassGameTimer      = coordcore.ClassGameTimer      // 7
)

// EventKind・Event・Scheduler は coordcore の別名。
type (
	EventKind = coordcore.EventKind
	Event     = coordcore.Event
	Scheduler = coordcore.Scheduler
)

// スケジューラのエラー（coordcore と同じ値）。
var (
	ErrPastEvent     = coordcore.ErrPastEvent
	ErrUnknownClass  = coordcore.ErrUnknownClass
	ErrTimeBackwards = coordcore.ErrTimeBackwards
	ErrPendingEvents = coordcore.ErrPendingEvents
)
