package server

import (
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// SV-R1: standalone のセッションで cmd.relay.load は Engine に渡り（not implemented ではない）、
// snapshot.stage.relayCap にステージの値（alpha は 12）が入る。
func TestSessionRelayLoad(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha","seed":3}`)
	c := ts.dialSession(t, cr)
	c.send(protocol.MessageTypeHello, "", protocol.HelloPayload{})
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Stage.RelayCap != 12 {
		t.Errorf("stage.relayCap = %d, want 12", snap.Stage.RelayCap)
	}

	// 開始の前は Engine が phase_invalid を返す。
	payload := protocol.FerryLettersPayload{FerryID: "f1", LetterIDs: []uint64{99999}}
	c.send(protocol.MessageTypeCmdRelayLoad, "r-1", payload)
	c.expectAckFail("r-1", protocol.AckCodePhaseInvalid)
	c.expect(protocol.MessageTypeEvents, nil) // 失敗したコマンドの後も events を送る（T-303）

	c.send(protocol.MessageTypeCmdGameStart, "c-1", struct{}{})
	c.expect(protocol.MessageTypeAck, nil)
	c.expect(protocol.MessageTypeEvents, nil)
	c.send(protocol.MessageTypeCmdRelayLoad, "r-2", payload)
	c.expectAckFail("r-2", protocol.AckCodeLetterNotHere)
	c.expect(protocol.MessageTypeEvents, nil)

	// payload の形の誤りは bad_request。
	c.send(protocol.MessageTypeCmdRelayLoad, "r-3", protocol.FerryLettersPayload{FerryID: "f1", LetterIDs: []uint64{}})
	c.expectAckFail("r-3", protocol.AckCodeBadRequest)
}

// 積み替えを含む入力ログから internal.restore で復元すると、倉庫の手紙を含めて参照の Engine と一致する
// （Durable Object の復元で再現できる）。続けて送った cmd.relay.load の結果も一致する。
func TestRestoreWithRelay(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)
	ref := newRefEngine(t, ts.srv.cat)
	ref.apply(sim.Command{Type: sim.CmdGameStart})
	ref.e.RunUntil(1000)
	var to3 []uint64
	for _, l := range ref.e.Snapshot().Letters {
		if l.Loc.Type == "port" && l.Loc.IslandID == 1 && l.To == 3 {
			to3 = append(to3, l.ID)
		}
	}
	if len(to3) == 0 {
		t.Fatal("島 1 の港に 3 宛ての手紙がない")
	}
	to3 = to3[:1]
	// 島 2 で預け、積み直し、もう一度預ける（入力ログに cmd.relay.load が入り、最後は倉庫にある）。
	ref.apply(sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: to3})
	ref.apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2})
	ref.e.RunUntil(2500)
	ref.apply(sim.Command{Type: sim.CmdFerryUnload, FerryID: "f1", LetterIDs: to3})
	ref.apply(sim.Command{Type: sim.CmdRelayLoad, FerryID: "f1", LetterIDs: to3})
	ref.apply(sim.Command{Type: sim.CmdFerryUnload, FerryID: "f1", LetterIDs: to3})
	ref.e.DrainEvents()

	c := ts.dialSingle(t)
	c.send(protocol.MessageTypeInternalRestore, "", restorePayload("s_relay.1", ref.inputLog("git:old0000"), 2500))
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	want := simconv.SnapshotFromSim(ref.e.Snapshot())
	if got, w := snapshotParts(t, snap), snapshotParts(t, want); got != w {
		t.Fatalf("snapshot =\n%s\nwant\n%s", got, w)
	}
	relayed := 0
	for _, l := range snap.Letters {
		if l.Loc.Type == protocol.LocTypeRelay {
			relayed++
			if l.ID != to3[0] || l.Loc.IslandID != 2 {
				t.Errorf("relay letter = %+v, want %d at 2", l, to3[0])
			}
		}
	}
	if relayed != 1 {
		t.Errorf("relay letters = %d, want 1", relayed)
	}

	cmd := sim.Command{Type: sim.CmdRelayLoad, FerryID: "f1", LetterIDs: to3}
	cc, err := simconv.EncodeEngineCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	c.send(cc.Type, "r-1", cc.Payload)
	wantAck := simconv.AckFromSim("r-1", ref.e.Apply(cmd))
	var ack protocol.Ack
	c.expect(protocol.MessageTypeAck, &ack)
	if !ack.OK || ack != wantAck {
		t.Errorf("ack = %+v, want %+v", ack, wantAck)
	}
	var evs protocol.EventsPayload
	c.expect(protocol.MessageTypeEvents, &evs)
	if got, w := mustJSON(t, evs.Events), mustJSON(t, ref.e.DrainEvents()); got != w {
		t.Errorf("events = %s, want %s", got, w)
	}
}
