package sim

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

func reserve(reqID string) ledger.Command {
	return ledger.Command{V: ledger.Version, ReqID: reqID, Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main"}
}

func TestCommandErrors(t *testing.T) { // A-1
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, e *Engine)
		cmd   Command
		code  string
	}{
		{"unknown type", nil, Command{Type: "cmd.x"}, CodeBadRequest},
		{"empty ferry id", nil, Command{Type: CmdFerryDepart, To: 2}, CodeBadRequest},
		{"unknown ferry", nil, Command{Type: CmdFerryDepart, FerryID: "zz", To: 2}, CodeBadRequest},
		{"load no letters", nil, Command{Type: CmdFerryLoad, FerryID: "f1"}, CodeBadRequest},
		{"load unknown letter", nil, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{999}}, CodeLetterNotHere},
		{"load over capacity", func(t *testing.T, e *Engine) {
			mustPropose(t, e, 1, reserve("s-0001"))
			mustPropose(t, e, 1, reserve("s-0002"))
			e.RunUntil(1000)
			if n := len(e.tr.Port(1)); n != 6 {
				t.Fatalf("port 1 has %d letters, want 6", n)
			}
		}, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1, 2, 3, 4, 5)}, CodeCapacityExceeded},
		{"depart no route", nil, Command{Type: CmdFerryDepart, FerryID: "f1", To: 3}, CodeNoRoute},
		{"depart to here", nil, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1}, CodeNoRoute},
		{"load while sailing", func(t *testing.T, e *Engine) {
			mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		}, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1)}, CodeFerryNotDocked},
		{"depart right after arrival", func(t *testing.T, e *Engine) {
			mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
			e.RunUntil(3750)
		}, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1}, CodeFerryNotReady},
		{"depart blocked", func(t *testing.T, e *Engine) {
			if err := e.tr.Block([][2]uint64{{1, 2}}, 10000); err != nil {
				t.Fatal(err)
			}
		}, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2}, CodeRouteBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := startedEngine(t)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			e.DrainEvents()
			now, hash, inputs := e.Now(), e.TraceHash(), len(e.InputLog())
			ack := e.Apply(tt.cmd)
			if ack.OK || ack.Code != tt.code || ack.T != now || ack.Message == "" {
				t.Errorf("Apply = %+v, want code %s at %d", ack, tt.code, now)
			}
			if evs := e.DrainEvents(); len(evs) != 0 {
				t.Errorf("events after failure: %+v", datas(evs))
			}
			if e.Now() != now || e.TraceHash() != hash || len(e.InputLog()) != inputs {
				t.Error("failed command changed Now, TraceHash or InputLog")
			}
		})
	}
}

func TestLoadUnload(t *testing.T) { // A-2
	t.Parallel()
	e := startedEngine(t)
	e.RunUntil(1000) // Heartbeat 1（1→2）と 2（1→3）
	e.DrainEvents()

	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1)})
	checkEvents(t, e.DrainEvents(), []EventData{LetterMoved{LetterID: lid(1, 1), Loc: LocView{Type: "ferry", FerryID: "f1"}}})

	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: lids(1, 1)})
	checkEvents(t, e.DrainEvents(), []EventData{LetterMoved{LetterID: lid(1, 1), Loc: LocView{Type: "port", IslandID: 1}}})

	// 積んでいる間に新しい Heartbeat（1→2）が港に置かれてから、古い方を降ろす。
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1)})
	e.RunUntil(2000)
	e.DrainEvents()
	if l, ok := e.tr.Letter(flid(1, 3)); !ok || l.To != 2 || l.Location.Kind != ferry.LocPort {
		t.Fatalf("letter 3 = %+v, %v; want a heartbeat to 2 in port", l, ok)
	}
	mustApply(t, e, Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: lids(1, 1)})
	checkEvents(t, e.DrainEvents(), []EventData{
		LetterMoved{LetterID: lid(1, 1), Loc: LocView{Type: "port", IslandID: 1}},
		LetterDropped{LetterID: lid(1, 1), Reason: "superseded"},
	})
}

func TestDepart(t *testing.T) { // A-3
	t.Parallel()
	e := startedEngine(t)
	ack := mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	if ack.T != 0 {
		t.Errorf("ack.T = %d, want 0", ack.T)
	}
	checkEvents(t, e.DrainEvents(), []EventData{
		FerryDeparted{FerryID: "f1", From: 1, To: 2, DepartAt: 0, ArriveAt: 3750},
	})
	found := false
	for _, ev := range e.coord.Sched.Events() {
		if ev.Kind == kindFerryArrive && ev.At == 3750 && ev.Class == ClassFerryArrive && ev.Data == ferry.FerryID("f1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no arrival at 3750 in the scheduler: %+v", e.coord.Sched.Events())
	}
}

func TestInputLog(t *testing.T) { // A-4
	t.Parallel()
	e := newEngine(t, testConfig(), Options{CheckInvariants: true})
	mustApply(t, e, Command{Type: CmdGameStart})
	e.Apply(Command{Type: CmdGameStart}) // 失敗
	e.RunUntil(1000)
	load := Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1, 2)}
	mustApply(t, e, load)
	load.LetterIDs[0] = 99                                       // 呼び出し側の書き換えが入力ログに及ばない
	e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 3}) // 失敗
	e.RunUntil(1200)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})

	want := []InputEntry{
		{T: 0, Cmd: Command{Type: CmdGameStart}},
		{T: 1000, Cmd: Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: lids(1, 1, 2)}},
		{T: 1200, Cmd: Command{Type: CmdFerryDepart, FerryID: "f1", To: 2}},
	}
	got := e.InputLog()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InputLog = %+v, want %+v", got, want)
	}
	got[1].Cmd.LetterIDs[0] = 42
	if again := e.InputLog(); !reflect.DeepEqual(again, want) {
		t.Errorf("InputLog changed after modifying the returned copy: %+v", again)
	}
}
