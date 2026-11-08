package simconv_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

const testSeed = 20261101

// testConfig は sim の engine_test.go の testConfig と同じ値（3 島・航路 1-2・2-3・船 f1・資源 2 つ）。
func testConfig() sim.Config {
	return sim.Config{
		Islands:       3,
		InitialLeader: 1,
		DurationMs:    120000,
		Raft: sim.RaftConfig{
			TickMs:          500,
			HeartbeatTick:   2,
			ElectionTick:    40,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 4,
		},
		Routes: []ferry.RouteConfig{
			{A: 1, B: 2, TravelMs: 3750},
			{A: 2, B: 3, TravelMs: 5250},
		},
		Ferries: []ferry.FerryConfig{
			{ID: "f1", Home: 1, Capacity: 4, SpeedPermille: 1000},
		},
		DockMs:        1500,
		PortCap:       24,
		PerLinkAppCap: 4,
		Resources: []ledger.ResourceDef{
			{ID: "firework:a", Kind: ledger.ResourceFirework},
			{ID: "stall:main", Kind: ledger.ResourceStall, Capacity: 2},
		},
	}
}

// snapAt は、ある時点の sim.Snapshot と、そのときの Now()。
type snapAt struct {
	name string
	snap sim.Snapshot
	now  sim.SimTime
}

// runEngine は C-6 の流れで Engine を動かす。出たイベント・途中の Snapshot・失敗したコマンドの ack を返す。
func runEngine(t *testing.T) (*sim.Engine, []sim.DomainEvent, []snapAt, protocol.Ack, sim.SimTime) {
	t.Helper()
	cfg := testConfig()
	e, err := sim.New(cfg, testSeed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	var events []sim.DomainEvent
	var snaps []snapAt
	snap := func(name string) { snaps = append(snaps, snapAt{name, e.Snapshot(), e.Now()}) }
	apply := func(typ protocol.MessageType, payload any) sim.Ack {
		t.Helper()
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		cmd, err := simconv.DecodeEngineCommand(typ, b)
		if err != nil {
			t.Fatalf("DecodeEngineCommand(%s, %s): %v", typ, b, err)
		}
		a := e.Apply(cmd)
		events = append(events, e.DrainEvents()...)
		return a
	}

	snap("intro")
	if a := apply(protocol.MessageTypeCmdGameStart, struct{}{}); !a.OK {
		t.Fatalf("cmd.game.start: %+v", a)
	}
	e.RunUntil(1000)
	events = append(events, e.DrainEvents()...)

	// 失敗するコマンド（島 1 から 3 への直接の航路はない）。
	failed := simconv.AckFromSim("c-fail", apply(protocol.MessageTypeCmdFerryDepart, protocol.FerryToPayload{FerryID: "f1", To: 3}))
	failedAt := e.Now()

	var ids []uint64
	for _, l := range e.Snapshot().Letters {
		if l.Loc.Type == "port" && l.Loc.IslandID == 1 && l.To == 2 && len(ids) < cfg.Ferries[0].Capacity {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("no letter for island 2 at the port of island 1")
	}
	if a := apply(protocol.MessageTypeCmdFerryLoad, protocol.FerryLettersPayload{FerryID: "f1", LetterIDs: ids}); !a.OK {
		t.Fatalf("cmd.ferry.load: %+v", a)
	}
	if a := apply(protocol.MessageTypeCmdFerryDepart, protocol.FerryToPayload{FerryID: "f1", To: 2}); !a.OK {
		t.Fatalf("cmd.ferry.depart: %+v", a)
	}
	e.RunUntil(2000)
	events = append(events, e.DrainEvents()...)
	snap("sailing")

	e.RunUntil(cfg.DurationMs)
	events = append(events, e.DrainEvents()...)
	snap("ended")
	if e.Phase() != sim.PhaseEnded {
		t.Fatalf("Phase() = %s, want ended", e.Phase())
	}
	return e, events, snaps, failed, failedAt
}

// C-6
func TestEngineEventsMatchProtocol(t *testing.T) {
	t.Parallel()
	e, events, _, failed, failedAt := runEngine(t)

	seen := map[protocol.EventType]bool{}
	for _, ev := range events {
		if !slices.Contains(protocol.EventTypes(), protocol.EventType(ev.Type)) {
			t.Errorf("event type %q is not in protocol.EventTypes()", ev.Type)
		}
		assertSameEventJSON(t, ev)
		seen[protocol.EventType(ev.Type)] = true
	}
	for _, want := range []protocol.EventType{
		protocol.EventTypePhaseChanged,
		protocol.EventTypeNodeState,
		protocol.EventTypeLeaderElected,
		protocol.EventTypeLetterCreated,
		protocol.EventTypeLetterMoved,
		protocol.EventTypeFerryDeparted,
		protocol.EventTypeFerryArrived,
		protocol.EventTypeLetterDelivered,
	} {
		if !seen[want] {
			t.Errorf("no %q event", want)
		}
	}

	if failed.OK || failed.Code != protocol.AckCodeNoRoute || !slices.Contains(protocol.AckCodes(), failed.Code) {
		t.Errorf("failed ack = %+v, want code no_route", failed)
	}
	if failed.T != int64(failedAt) || failed.CmdID != "c-fail" {
		t.Errorf("failed ack = %+v, want t %d", failed, failedAt)
	}

	log := e.InputLog()
	cmds := make([]protocol.InputCommand, 0, len(log))
	for _, in := range log {
		cc, err := simconv.EncodeEngineCommand(in.Cmd)
		if err != nil {
			t.Fatalf("EncodeEngineCommand(%+v): %v", in.Cmd, err)
		}
		cmds = append(cmds, protocol.InputCommand{T: int64(in.T), Cmd: cc})
	}
	back, err := simconv.InputEntriesFromLog(cmds)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, log) {
		t.Errorf("InputEntriesFromLog(Encode(InputLog())) = %+v, want %+v", back, log)
	}
	if len(log) != 3 {
		t.Errorf("len(InputLog()) = %d, want 3 (start, load, depart)", len(log))
	}
}

// C-7
func TestSnapshotFromSimMatchesJSON(t *testing.T) {
	t.Parallel()
	_, _, snaps, _, _ := runEngine(t)
	for _, s := range snaps {
		t.Run(s.name, func(t *testing.T) {
			p := simconv.SnapshotFromSim(s.snap)
			pairs := []struct {
				name     string
				got, src any
			}{
				{"ferries", p.Ferries, s.snap.Ferries},
				{"routesState", p.RoutesState, s.snap.Routes},
				{"letters", p.Letters, s.snap.Letters},
				{"nodes", p.Nodes, s.snap.Nodes},
			}
			for _, pr := range pairs {
				got, err := json.Marshal(pr.got)
				if err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(pr.src)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s JSON differs\nprotocol: %s\n     sim: %s", pr.name, got, want)
				}
			}
			if p.Protocol != 1 || p.Phase != protocol.Phase(s.snap.Phase) || p.Clock.T != int64(s.now) ||
				p.Clock.EndsAt != int64(testConfig().DurationMs) || p.LastSeq != s.snap.LastSeq {
				t.Errorf("SnapshotFromSim = protocol %d phase %s t %d endsAt %d lastSeq %d, want phase %s t %d lastSeq %d",
					p.Protocol, p.Phase, p.Clock.T, p.Clock.EndsAt, p.LastSeq, s.snap.Phase, s.now, s.snap.LastSeq)
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			assertNoNullArrays(t, b)
		})
	}
	if len(snaps) != 3 || snaps[0].snap.Phase != sim.PhaseIntro || snaps[1].snap.Ferries[0].State != "sailing" {
		t.Errorf("unexpected snapshots: %+v", snaps)
	}
}

// CV-D4 入力ログの JSON からの再生（T-308b の復元の道筋）で、デモの嵐が同じように起きる。
func TestReplayFromInputLogDemoTrigger(t *testing.T) {
	t.Parallel()
	const stageHash = "sha256:cv-d4"
	cfg := testConfig()
	cfg.DemoStorm = &sim.DemoStorm{Routes: [][2]uint64{{3, 2}}, DurationMs: 20000}
	e, err := sim.New(cfg, testSeed, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	apply := func(cmd sim.Command) {
		t.Helper()
		if ack := e.Apply(cmd); !ack.OK {
			t.Fatalf("t=%d: Apply(%+v) = %+v", e.Now(), cmd, ack)
		}
	}
	apply(sim.Command{Type: sim.CmdGameStart})
	e.RunUntil(3000)
	apply(sim.Command{Type: sim.CmdDemoTrigger, Event: sim.DemoEventStorm})
	e.RunUntil(8000)
	apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(14000)
	if ack := e.Apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 3}); ack.Code != sim.CodeRouteBlocked {
		t.Fatalf("遮断された航路への出航 = %+v, want route_blocked", ack)
	}
	apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 1})
	e.RunUntil(30000)
	if until := e.Snapshot().Routes[1].BlockedUntil; until != nil {
		t.Fatalf("30000 で航路 2-3 が遮断のまま（%d）", *until)
	}

	// Durable Object が保存する形（t・cmd の列）にし、セッションのコマンドを混ぜて JSON で往復する。
	var log []protocol.InputCommand
	for i, en := range e.InputLog() {
		cc, err := simconv.EncodeEngineCommand(en.Cmd)
		if err != nil {
			t.Fatalf("EncodeEngineCommand(%+v): %v", en.Cmd, err)
		}
		log = append(log, protocol.InputCommand{T: int64(en.T), Cmd: cc})
		if i == 1 {
			log = append(log,
				inputCmd(int64(en.T), "cmd.game.pause", `{"paused":true}`),
				inputCmd(int64(en.T), "cmd.game.speed", `{"speed":8}`),
				inputCmd(int64(en.T), "cmd.game.skip", `{"until":"next_notable"}`),
				inputCmd(int64(en.T), "cmd.sync", `{}`),
			)
		}
	}
	b, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	var back []protocol.InputCommand
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	entries, err := simconv.InputEntriesFromLog(back)
	if err != nil {
		t.Fatalf("InputEntriesFromLog: %v", err)
	}
	if !reflect.DeepEqual(entries, e.InputLog()) {
		t.Fatalf("InputEntriesFromLog = %+v, want %+v", entries, e.InputLog())
	}
	r, err := sim.Replay(cfg, stageHash, sim.ReplayLog{
		Format: sim.ReplayFormat, StageHash: stageHash, Seed: testSeed, Entries: entries, Until: e.Now(),
	}, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if r.TraceHash() != e.TraceHash() {
		t.Error("TraceHash が元と違う")
	}
	if got, want := r.Snapshot(), e.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot が元と違う:\n got  %+v\n want %+v", got, want)
	}

	// 遮断の途中（10000）まで再生しても、航路の遮断の時刻が一致する。
	r2, err := sim.Replay(cfg, stageHash, sim.ReplayLog{
		Format: sim.ReplayFormat, StageHash: stageHash, Seed: testSeed, Entries: entries[:2], Until: 10000,
	}, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatalf("Replay（途中）: %v", err)
	}
	if until := r2.Snapshot().Routes[1].BlockedUntil; until == nil || *until != 23000 {
		t.Errorf("途中の航路 2-3 の遮断 = %v, want 23000", until)
	}
}
