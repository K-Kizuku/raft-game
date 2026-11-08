package islandcore

import (
	"fmt"
	"slices"
)

// EventKind は島のイベントの種類。
type EventKind uint8

const (
	KindRaftTick     EventKind = iota + 1 // ClassRaftTick
	KindRequestSpawn                      // ClassRequestSpawn。Req は依頼の添字
	KindRequestRetry                      // ClassRequestRetry。Req は依頼の添字、Gen はタイマーの世代
)

// Event は島の待ち行列のイベント。(At, Class, Seq) の昇順に処理する。
type Event struct {
	At    int64
	Class Class
	Seq   uint64
	Kind  EventKind
	Req   int
	Gen   uint64
}

// Mark はイベントの時刻の印を返す。
func (e Event) Mark() Mark { return Mark{T: e.At, Class: e.Class} }

func eventLess(a, b Event) int {
	switch {
	case a.At != b.At:
		if a.At < b.At {
			return -1
		}
		return 1
	case a.Class != b.Class:
		if a.Class < b.Class {
			return -1
		}
		return 1
	case a.Seq != b.Seq:
		if a.Seq < b.Seq {
			return -1
		}
		return 1
	}
	return 0
}

// queue は島のイベントの待ち行列（並べたスライス。島のイベントは少ない）。
type queue struct {
	evs []Event
}

func (q *queue) push(ev Event) {
	i, _ := slices.BinarySearchFunc(q.evs, ev, eventLess)
	q.evs = slices.Insert(q.evs, i, ev)
}

func (q *queue) peek() (Event, bool) {
	if len(q.evs) == 0 {
		return Event{}, false
	}
	return q.evs[0], true
}

func (q *queue) pop() Event {
	ev := q.evs[0]
	q.evs = slices.Delete(q.evs, 0, 1)
	return ev
}

// push は島のイベントを積む。at が島の今の時刻より前なら Core の誤りとして panic する。
func (c *Core) push(at int64, class Class, kind EventKind, req int, gen uint64) {
	if at < c.now {
		panic(fmt.Sprintf("islandcore: island %d: push kind %d at %d before now %d", c.id, kind, at, c.now))
	}
	c.q.push(Event{At: at, Class: class, Seq: c.nextSeq(), Kind: kind, Req: req, Gen: gen})
}

// Peek は次に処理する島のイベントを返す。状態は変えない。
func (c *Core) Peek() (Event, bool) { return c.q.peek() }

// Pending は待ち行列のイベントの数を返す。
func (c *Core) Pending() int { return len(c.q.evs) }

// Pop は次のイベントを取り出し、島の今の時刻をその At にする（参照モデルが使う部品。分散版は AdvanceTo）。
// 待ち行列が空なら panic する。
func (c *Core) Pop() Event {
	if len(c.q.evs) == 0 {
		panic(fmt.Sprintf("islandcore: island %d: pop from empty queue", c.id))
	}
	ev := c.q.pop()
	c.setNow(ev.At)
	return ev
}

// Events は待ち行列のイベントの複製を (At, Class, Seq) の順に返す（テスト・観察用）。
func (c *Core) Events() []Event { return slices.Clone(c.q.evs) }
