package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/simconv"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// alpha（testdata/stages/alpha.yaml）の採点の値。
const (
	scoreTestTarget     = 2     // scoring.target.successes
	scoreTestSatInitial = 60    // satisfaction.initial
	scoreTestSilenceMs  = 30000 // satisfaction.silenceAfterMs
)

func TestSnapshotScoreBeforeStart(t *testing.T) { // SV-1
	t.Parallel()
	s, e, _ := resultTestSession(t)
	if e.Phase() != sim.PhaseIntro {
		t.Fatalf("phase = %s, want intro", e.Phase())
	}
	p := s.buildSnapshot()
	if len(p.IslandsState) != 3 {
		t.Fatalf("islandsState = %s, want 3 islands", mustJSON(t, p.IslandsState))
	}
	for i, is := range p.IslandsState {
		if is.ID != uint64(i+1) || is.Satisfaction != scoreTestSatInitial || is.LastDeliveredAt == nil || *is.LastDeliveredAt != 0 {
			t.Errorf("islandsState[%d] = %s, want id %d satisfaction %d lastDeliveredAt 0",
				i, mustJSON(t, is), i+1, scoreTestSatInitial)
		}
	}
	if want := (protocol.Score{Total: 0, Successes: 0, Target: scoreTestTarget, CriticalSecured: []string{}}); !reflect.DeepEqual(p.Score, want) {
		t.Errorf("score = %s, want %s", mustJSON(t, p.Score), mustJSON(t, want))
	}
	assertNoNullArrays(t, []byte(mustJSON(t, p)))
}

func TestSnapshotScoreMatchesSim(t *testing.T) { // SV-2
	t.Parallel()
	s, e, _ := resultTestSession(t)
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	if ack := e.Apply(sim.Command{Type: sim.CmdFerryDepart, FerryID: "f1", To: 2}); !ack.OK {
		t.Fatalf("depart: %+v", ack)
	}
	e.RunUntil(scoreTestSilenceMs)
	p := s.buildSnapshot()
	t.Logf("islandsState: %s", mustJSON(t, p.IslandsState))
	t.Logf("score: %s", mustJSON(t, p.Score))

	snap := e.Snapshot()
	if got, want := mustJSON(t, p.IslandsState), mustJSON(t, simconv.IslandStatesFromSim(snap.Islands)); got != want {
		t.Errorf("islandsState = %s, want %s", got, want)
	}
	if got, want := mustJSON(t, p.Score), mustJSON(t, simconv.ScoreFromSim(snap.Score, scoreTestTarget)); got != want {
		t.Errorf("score = %s, want %s", got, want)
	}
	if p.Score.Total == 0 {
		t.Error("score.total = 0 after a voyage")
	}
	changed := false
	for _, is := range p.IslandsState {
		changed = changed || is.Satisfaction != scoreTestSatInitial
	}
	if !changed {
		t.Errorf("every satisfaction is still %d: %s", scoreTestSatInitial, mustJSON(t, p.IslandsState))
	}
	assertNoNullArrays(t, []byte(mustJSON(t, p)))
}

// decodeEventStrict は、events の要素 raw を protocol.NewEvent(typ) の型に、知らないキーを許さずに読む。
func decodeEventStrict(t *testing.T, typ protocol.EventType, raw json.RawMessage) any {
	t.Helper()
	v, ok := protocol.NewEvent(typ)
	if !ok {
		t.Fatalf("NewEvent(%s) = false", typ)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s as %T: %v", raw, v, err)
	}
	return v
}

// eventKeys は、events の要素 raw のキーの集合。
func eventKeys(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(m))
	for k, v := range m {
		if string(v) == "null" {
			t.Errorf("%s: %s is null", raw, k)
		}
		out[k] = true
	}
	return out
}

func TestSnapshotScoreEventsRelay(t *testing.T) { // SV-3
	t.Parallel()
	_, c, lt, sess := startSingle(t, "s_sv.3", 1)
	_, ev := c.control(protocol.MessageTypeCmdFerryDepart, "d-1", protocol.FerryToPayload{FerryID: "f1", To: 2})
	raws := append([]json.RawMessage{}, ev.Events...)
	probe(t, sess, func(s *session) { s.advanceTo(scoreTestSilenceMs) })
	lt.flushAfter(0)
	envs, now := c.collect()
	if now != scoreTestSilenceMs {
		t.Fatalf("simTime = %d, want %d", now, scoreTestSilenceMs)
	}
	for _, p := range eventsOf(t, envs) {
		raws = append(raws, p.Events...)
	}

	var lastScore *protocol.ScoreUpdatedEvent
	lastIsland := map[uint64]*protocol.IslandUpdatedEvent{}
	var scoreExample, islandExample json.RawMessage
	for i, h := range heads(t, raws) {
		switch protocol.EventType(h.Type) {
		case protocol.EventTypeScoreUpdated:
			keys := eventKeys(t, raws[i])
			for _, k := range []string{"seq", "t", "type", "total", "successes", "criticalSecured"} {
				if !keys[k] {
					t.Errorf("score.updated %s has no %s", raws[i], k)
				}
			}
			lastScore = decodeEventStrict(t, protocol.EventTypeScoreUpdated, raws[i]).(*protocol.ScoreUpdatedEvent)
			scoreExample = raws[i]
		case protocol.EventTypeIslandUpdated:
			keys := eventKeys(t, raws[i])
			for _, k := range []string{"seq", "t", "type", "id", "satisfaction", "lastDeliveredAt"} {
				if !keys[k] {
					t.Errorf("island.updated %s has no %s", raws[i], k)
				}
			}
			is := decodeEventStrict(t, protocol.EventTypeIslandUpdated, raws[i]).(*protocol.IslandUpdatedEvent)
			lastIsland[is.ID] = is
			islandExample = raws[i]
		}
	}
	if lastScore == nil {
		t.Fatal("no score.updated in events")
	}
	if len(lastIsland) == 0 {
		t.Fatal("no island.updated in events")
	}
	t.Logf("score.updated: %s", scoreExample)
	t.Logf("island.updated: %s", islandExample)

	c.send(protocol.MessageTypeCmdSync, "sync", struct{}{})
	c.expectAck("sync")
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.Clock.T != scoreTestSilenceMs {
		t.Fatalf("snapshot clock = %+v", snap.Clock)
	}

	if lastScore.Total != snap.Score.Total || lastScore.Successes != snap.Score.Successes ||
		!reflect.DeepEqual(lastScore.CriticalSecured, snap.Score.CriticalSecured) {
		t.Errorf("last score.updated %s, snapshot score %s", mustJSON(t, lastScore), mustJSON(t, snap.Score))
	}
	if snap.Score.Total == 0 {
		t.Error("snapshot score.total = 0 after a voyage")
	}
	for _, is := range snap.IslandsState {
		u, ok := lastIsland[is.ID]
		if !ok {
			continue // まだ変わっていない島（初期の値）
		}
		if u.Satisfaction != is.Satisfaction || u.LastDeliveredAt == nil || is.LastDeliveredAt == nil ||
			*u.LastDeliveredAt != *is.LastDeliveredAt {
			t.Errorf("island %d: last island.updated %s, snapshot %s", is.ID, mustJSON(t, u), mustJSON(t, is))
		}
	}
	for id := range lastIsland {
		if id < 1 || id > uint64(len(snap.IslandsState)) {
			t.Errorf("island.updated for unknown island %d", id)
		}
	}
}
