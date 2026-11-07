package sim

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"testing"
)

type key struct {
	At    SimTime
	Class EventClass
	Seq   uint64
}

func keyOf(ev Event) key { return key{ev.At, ev.Class, ev.Seq} }

func mustPush(t *testing.T, s *Scheduler, at SimTime, class EventClass) uint64 {
	t.Helper()
	seq, err := s.Push(at, class, 0, nil)
	if err != nil {
		t.Fatalf("Push(%d, %d): %v", at, class, err)
	}
	return seq
}

func drain(s *Scheduler) []key {
	var out []key
	for {
		ev, ok := s.Pop()
		if !ok {
			return out
		}
		out = append(out, keyOf(ev))
	}
}

// S-1
func TestSchedulerOrder(t *testing.T) {
	t.Parallel()
	type in struct {
		At    SimTime
		Class EventClass
	}
	tests := []struct {
		name string
		in   []in
		want []key
	}{
		{
			name: "同じ At は Class の昇順",
			in:   []in{{10, ClassGameTimer}, {10, ClassCommandApplied}, {10, ClassRaftTick}},
			want: []key{{10, ClassCommandApplied, 2}, {10, ClassRaftTick, 3}, {10, ClassGameTimer, 1}},
		},
		{
			name: "同じ At と Class は積んだ順",
			in:   []in{{5, ClassWorldEvent}, {5, ClassWorldEvent}, {5, ClassWorldEvent}},
			want: []key{{5, ClassWorldEvent, 1}, {5, ClassWorldEvent, 2}, {5, ClassWorldEvent, 3}},
		},
		{
			name: "At は Class より優先",
			in:   []in{{20, ClassCommandApplied}, {10, ClassGameTimer}},
			want: []key{{10, ClassGameTimer, 2}, {20, ClassCommandApplied, 1}},
		},
		{
			name: "混在",
			in: []in{
				{30, ClassFerryArrive}, {10, ClassRequestRetry}, {10, ClassFerryReady},
				{30, ClassFerryArrive}, {0, ClassGameTimer}, {10, ClassFerryReady}, {30, ClassCommandApplied},
			},
			want: []key{
				{0, ClassGameTimer, 5},
				{10, ClassFerryReady, 3}, {10, ClassFerryReady, 6}, {10, ClassRequestRetry, 2},
				{30, ClassCommandApplied, 7}, {30, ClassFerryArrive, 1}, {30, ClassFerryArrive, 4},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s Scheduler
			for _, e := range tt.in {
				mustPush(t, &s, e.At, e.Class)
			}
			if got := drain(&s); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// S-2
func TestSchedulerRandom1000(t *testing.T) {
	t.Parallel()
	r := Sub(1, "test")
	var s Scheduler
	pushed := make([]key, 0, 1000)
	for range 1000 {
		at := SimTime(r.IntN(10000))
		class := EventClass(r.IntN(int(numEventClasses)))
		seq := mustPush(t, &s, at, class)
		pushed = append(pushed, key{at, class, seq})
	}
	want := slices.Clone(pushed)
	slices.SortStableFunc(want, func(a, b key) int {
		if c := cmp.Compare(a.At, b.At); c != 0 {
			return c
		}
		return cmp.Compare(a.Class, b.Class)
	})
	got := drain(&s)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("取り出した列が (At, Class) の安定ソートと一致しない")
	}
	for i, k := range pushed {
		if k.Seq != uint64(i+1) {
			t.Fatalf("pushed[%d].Seq = %d, want %d", i, k.Seq, i+1)
		}
	}
}

// S-3
func TestSchedulerNow(t *testing.T) {
	t.Parallel()
	var s Scheduler
	if s.Now() != 0 {
		t.Fatalf("初期の Now() = %d", s.Now())
	}
	mustPush(t, &s, 50, ClassRaftTick)
	mustPush(t, &s, 200, ClassRaftTick)
	if ev, ok := s.Peek(); !ok || ev.At != 50 || s.Now() != 0 || s.Len() != 2 {
		t.Fatalf("Peek = %v %v, Now() = %d, Len() = %d", ev, ok, s.Now(), s.Len())
	}
	if ev, ok := s.Pop(); !ok || ev.At != 50 || s.Now() != 50 {
		t.Fatalf("Pop = %v %v, Now() = %d", ev, ok, s.Now())
	}
	if _, ok := s.PopUntil(199); ok || s.Now() != 50 || s.Len() != 1 {
		t.Fatalf("PopUntil(199) = %v, Now() = %d, Len() = %d", ok, s.Now(), s.Len())
	}
	if ev, ok := s.Pop(); !ok || ev.At != 200 || s.Now() != 200 {
		t.Fatalf("Pop = %v %v, Now() = %d", ev, ok, s.Now())
	}
	if ev, ok := s.Pop(); ok || !reflect.DeepEqual(ev, Event{}) {
		t.Fatalf("空の Pop = %v %v", ev, ok)
	}
	if ev, ok := s.Peek(); ok || !reflect.DeepEqual(ev, Event{}) {
		t.Fatalf("空の Peek = %v %v", ev, ok)
	}
}

// S-4
func TestSchedulerPushPast(t *testing.T) {
	t.Parallel()
	var s Scheduler
	mustPush(t, &s, 100, ClassRaftTick)
	mustPush(t, &s, 300, ClassRaftTick)
	if _, ok := s.Pop(); !ok || s.Now() != 100 {
		t.Fatalf("Now() = %d", s.Now())
	}
	if _, err := s.Push(99, ClassRaftTick, 0, nil); !errors.Is(err, ErrPastEvent) {
		t.Fatalf("Push(99) err = %v", err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", s.Len())
	}
	if seq := mustPush(t, &s, 100, ClassRaftTick); seq != 3 {
		t.Fatalf("Seq = %d, want 3", seq)
	}

	var z Scheduler
	if _, err := z.Push(-1, ClassRaftTick, 0, nil); !errors.Is(err, ErrPastEvent) {
		t.Fatalf("Push(-1) err = %v", err)
	}
	if z.Len() != 0 {
		t.Fatalf("Len() = %d", z.Len())
	}
	if seq := mustPush(t, &z, 0, ClassRaftTick); seq != 1 {
		t.Fatalf("Seq = %d, want 1", seq)
	}
}

// S-5
func TestSchedulerUnknownClass(t *testing.T) {
	t.Parallel()
	for _, c := range []EventClass{8, 255} {
		var s Scheduler
		if _, err := s.Push(0, c, 0, nil); !errors.Is(err, ErrUnknownClass) {
			t.Fatalf("class %d: err = %v", c, err)
		}
		if s.Len() != 0 {
			t.Fatalf("class %d: Len() = %d", c, s.Len())
		}
		if seq := mustPush(t, &s, 0, ClassGameTimer); seq != 1 {
			t.Fatalf("class %d: 次の Seq = %d, want 1", c, seq)
		}
	}
}

// S-6
func TestSchedulerPopUntilBoundary(t *testing.T) {
	t.Parallel()
	var s Scheduler
	if ev, ok := s.PopUntil(100); ok || !reflect.DeepEqual(ev, Event{}) {
		t.Fatalf("空の PopUntil = %v %v", ev, ok)
	}
	mustPush(t, &s, 100, ClassRaftTick)
	mustPush(t, &s, 101, ClassRaftTick)
	if ev, ok := s.PopUntil(100); !ok || ev.At != 100 {
		t.Fatalf("PopUntil(100) = %v %v", ev, ok)
	}
	if _, ok := s.PopUntil(100); ok {
		t.Fatal("At == t+1 のイベントを取り出した")
	}
	if s.Now() != 100 || s.Len() != 1 {
		t.Fatalf("Now() = %d, Len() = %d", s.Now(), s.Len())
	}
}

// S-7
func TestSchedulerAdvanceTo(t *testing.T) {
	t.Parallel()
	var s Scheduler
	mustPush(t, &s, 100, ClassRaftTick)
	if err := s.AdvanceTo(101); !errors.Is(err, ErrPendingEvents) {
		t.Fatalf("AdvanceTo(101) err = %v", err)
	}
	if s.Now() != 0 {
		t.Fatalf("Now() = %d", s.Now())
	}
	if err := s.AdvanceTo(100); err != nil {
		t.Fatalf("AdvanceTo(100) err = %v", err)
	}
	if s.Now() != 100 || s.Len() != 1 {
		t.Fatalf("Now() = %d, Len() = %d", s.Now(), s.Len())
	}
	if err := s.AdvanceTo(99); !errors.Is(err, ErrTimeBackwards) {
		t.Fatalf("AdvanceTo(99) err = %v", err)
	}
	if _, err := s.Push(99, ClassRaftTick, 0, nil); !errors.Is(err, ErrPastEvent) {
		t.Fatalf("Push(99) err = %v", err)
	}
	s.Pop()
	if err := s.AdvanceTo(500); err != nil {
		t.Fatalf("AdvanceTo(500) err = %v", err)
	}
	if s.Now() != 500 {
		t.Fatalf("Now() = %d", s.Now())
	}
	if _, err := s.Push(499, ClassRaftTick, 0, nil); !errors.Is(err, ErrPastEvent) {
		t.Fatalf("Push(499) err = %v", err)
	}
}

// S-8
func TestSchedulerPushDuringProcessing(t *testing.T) {
	t.Parallel()
	var s Scheduler
	mustPush(t, &s, 100, ClassRaftTick)
	mustPush(t, &s, 200, ClassCommandApplied)
	if ev, ok := s.Pop(); !ok || ev.Class != ClassRaftTick {
		t.Fatalf("Pop = %v %v", ev, ok)
	}
	mustPush(t, &s, 100, ClassGameTimer)
	mustPush(t, &s, 100, ClassFerryArrive)
	want := []key{{100, ClassFerryArrive, 4}, {100, ClassGameTimer, 3}, {200, ClassCommandApplied, 2}}
	if got := drain(&s); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// S-9
func TestSchedulerDataPointer(t *testing.T) {
	t.Parallel()
	type payload struct{ n int }
	p := &payload{n: 1}
	var s Scheduler
	if _, err := s.Push(0, ClassWorldEvent, 7, p); err != nil {
		t.Fatal(err)
	}
	ev, ok := s.Pop()
	if !ok || ev.Kind != 7 {
		t.Fatalf("Pop = %v %v", ev, ok)
	}
	if got, _ := ev.Data.(*payload); got != p {
		t.Fatalf("Data = %v, want 同じポインタ %p", ev.Data, p)
	}
}

// S-10
func TestSchedulerDeterminism(t *testing.T) {
	t.Parallel()
	type trace struct {
		Events []Event
		Nows   []SimTime
		Errs   []bool
	}
	run := func() trace {
		r := Sub(42, "test/sched")
		var s Scheduler
		var tr trace
		for i := range 2000 {
			switch r.IntN(4) {
			case 0, 1:
				at := s.Now() + SimTime(r.IntN(500))
				_, err := s.Push(at, EventClass(r.IntN(int(numEventClasses))), EventKind(i), i)
				tr.Errs = append(tr.Errs, err != nil)
			case 2:
				if ev, ok := s.Pop(); ok {
					tr.Events = append(tr.Events, ev)
				}
			case 3:
				target := s.Now() + SimTime(r.IntN(300))
				for {
					ev, ok := s.PopUntil(target)
					if !ok {
						break
					}
					tr.Events = append(tr.Events, ev)
				}
				err := s.AdvanceTo(target)
				tr.Errs = append(tr.Errs, err != nil)
			}
			tr.Nows = append(tr.Nows, s.Now())
		}
		return tr
	}
	a, b := run(), run()
	if len(a.Events) == 0 {
		t.Fatal("イベントが 1 つも取り出されていない")
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("同じ操作の列で結果が一致しない")
	}
}
