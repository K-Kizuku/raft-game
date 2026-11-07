package sim

import (
	"container/heap"
	"errors"
	"fmt"
)

// EventClass は同じ時刻のイベントの処理順（§4.2）。小さいものから処理する。
type EventClass uint8

const (
	ClassCommandApplied EventClass = iota // 0 プレイヤーのコマンド（記録用）
	ClassFerryArrive                      // 1
	ClassFerryReady                       // 2
	ClassRaftTick                         // 3
	ClassWorldEvent                       // 4
	ClassRequestSpawn                     // 5
	ClassRequestRetry                     // 6
	ClassGameTimer                        // 7
	numEventClasses                       // 非公開。検査に使う
)

// EventKind はイベントの種類。値は Engine（T-106）以降で定義する。スケジューラは中身を見ない。
type EventKind uint16

// Event はスケジューラに積むイベント。
type Event struct {
	At    SimTime
	Class EventClass
	Seq   uint64 // 登録順（1 から）。完全に同じ順序にするための最後の比較キー
	Kind  EventKind
	Data  any
}

// スケジューラのエラー。errors.Is で判定する。
var (
	ErrPastEvent     = errors.New("sim: event is before Now")
	ErrUnknownClass  = errors.New("sim: unknown event class")
	ErrTimeBackwards = errors.New("sim: time goes backwards")
	ErrPendingEvents = errors.New("sim: events remain before the target time")
)

// Scheduler はイベントを (At, Class, Seq) の昇順に取り出す優先度キュー（container/heap）。
// ゼロ値のまま使える（Now() = 0、空）。1 つのゴルーチンから使う（ADR-0003）。並行に使うのは安全でない。
//
// Engine.RunUntil(t)（T-106）は次の形で使う想定。
//
//	for {
//		ev, ok := s.PopUntil(t)
//		if !ok {
//			break
//		}
//		handle(ev) // この中で Push してよい（at >= ev.At）
//	}
//	if err := s.AdvanceTo(t); err != nil {
//		// …
//	}
type Scheduler struct {
	q   eventHeap
	seq uint64 // 最後に振った Seq
	now SimTime
}

// Now は、最後に取り出したイベントの At、または AdvanceTo で進めた時刻を返す。初期値は 0。
func (s *Scheduler) Now() SimTime { return s.now }

// Len は残っているイベントの数を返す。
func (s *Scheduler) Len() int { return len(s.q) }

// Push はイベントを積み、振った Seq を返す。
// at < Now() なら ErrPastEvent、範囲外の class なら ErrUnknownClass を返し、キューも Seq も変えない。
// at == Now() は受け付ける。
func (s *Scheduler) Push(at SimTime, class EventClass, kind EventKind, data any) (uint64, error) {
	if at < s.now {
		return 0, fmt.Errorf("%w: at=%d now=%d", ErrPastEvent, at, s.now)
	}
	if class >= numEventClasses {
		return 0, fmt.Errorf("%w: class=%d", ErrUnknownClass, class)
	}
	s.seq++
	heap.Push(&s.q, Event{At: at, Class: class, Seq: s.seq, Kind: kind, Data: data})
	return s.seq, nil
}

// Peek は次に出てくるイベントを返す。キューも Now() も変えない。空なら Event{}, false。
func (s *Scheduler) Peek() (Event, bool) {
	if len(s.q) == 0 {
		return Event{}, false
	}
	return s.q[0], true
}

// Pop は (At, Class, Seq) が最小のイベントを取り出し、Now() をその At にする。空なら Event{}, false。
func (s *Scheduler) Pop() (Event, bool) {
	if len(s.q) == 0 {
		return Event{}, false
	}
	ev := heap.Pop(&s.q).(Event)
	s.now = ev.At
	return ev, true
}

// PopUntil は、次のイベントの At <= t なら Pop と同じ。
// そうでなければ（空を含む）Event{}, false を返し、Now() を変えない。
func (s *Scheduler) PopUntil(t SimTime) (Event, bool) {
	if len(s.q) == 0 || s.q[0].At > t {
		return Event{}, false
	}
	return s.Pop()
}

// AdvanceTo は Now() を t にする。
// t < Now() なら ErrTimeBackwards、At < t のイベントが残っていれば ErrPendingEvents を返す
// （At == t のイベントは残っていてよい）。
func (s *Scheduler) AdvanceTo(t SimTime) error {
	if t < s.now {
		return fmt.Errorf("%w: t=%d now=%d", ErrTimeBackwards, t, s.now)
	}
	if len(s.q) > 0 && s.q[0].At < t {
		return fmt.Errorf("%w: t=%d next=%d", ErrPendingEvents, t, s.q[0].At)
	}
	s.now = t
	return nil
}

// eventHeap は container/heap 用の最小ヒープ。
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := &h[i], &h[j]
	if a.At != b.At {
		return a.At < b.At
	}
	if a.Class != b.Class {
		return a.Class < b.Class
	}
	return a.Seq < b.Seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) { *h = append(*h, x.(Event)) }

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = Event{} // Data の参照を残さない
	*h = old[:n-1]
	return ev
}
