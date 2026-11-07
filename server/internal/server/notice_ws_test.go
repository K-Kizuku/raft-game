package server

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// noticeSeed は NW のテストのシード。
const noticeSeed = 1

// noticeRun は、single のセッションでブラウザと同じく WebSocket で受けた記録。
type noticeRun struct {
	t      *testing.T
	ts     *testServer
	c      *client
	lt     *loopTickers
	sess   *session
	snap   protocol.Snapshot // internal.init の snapshot
	events []json.RawMessage // 受けたすべてのイベント（受けた順）
}

// wsEvent は受けたイベントの、通知の確認に使う欄。
type wsEvent struct {
	Seq      uint64          `json:"seq"`
	T        int64           `json:"t"`
	Type     string          `json:"type"`
	ID       json.RawMessage `json:"id"`       // node.leader_elected・island.updated は数、request.updated は文字列
	IslandID uint64          `json:"islandId"` // notice
	Key      string          `json:"key"`      // notice
	Request  *struct {
		ID     string `json:"id"`
		Origin uint64 `json:"origin"`
	} `json:"request"` // request.created
}

// startNoticeSingle は single のセッションを internal.init で作り、cmd.game.start の events まで受ける。
func startNoticeSingle(t *testing.T, id string) *noticeRun {
	t.Helper()
	ts := newTestServer(t, ModeSingle, nil)
	c := ts.dialSingle(t)
	r := &noticeRun{t: t, ts: ts, c: c}
	r.snap = c.initSingle(id, noticeSeed)
	r.lt = ts.tickers(t)
	r.sess = ts.singleSession(t)
	_, ev := c.control(protocol.MessageTypeCmdGameStart, "start", struct{}{})
	r.events = append(r.events, ev.Events...)
	return r
}

// advance はゲーム内の時刻 to まで 1 秒ずつ進め、そのたびにまとめ送りをして events を受ける。
func (r *noticeRun) advance(to sim.SimTime) {
	r.t.Helper()
	for at := simNow(r.t, r.sess) + sim.Second; ; at += sim.Second {
		at = min(at, to)
		probe(r.t, r.sess, func(s *session) { s.advanceTo(at) })
		r.lt.flushAfter(0)
		envs, _ := r.c.collect()
		for _, env := range envs {
			if env.Type != protocol.MessageTypeEvents {
				r.t.Fatalf("unexpected %s at %d", env.Type, at)
			}
		}
		for _, ev := range eventsOf(r.t, envs) {
			r.events = append(r.events, ev.Events...)
		}
		if at >= to {
			return
		}
	}
}

// node は id が島 ID の数で、want と等しいか。
func (ev wsEvent) node(want uint64) bool {
	return string(ev.ID) == strconv.FormatUint(want, 10)
}

// decoded は受けたイベントを読み、notice は protocol.NoticeEvent でも読んで key が 9 個のどれかであることを確かめる。
func (r *noticeRun) decoded() []wsEvent {
	r.t.Helper()
	return decodeWSEvents(r.t, r.events)
}

func decodeWSEvents(t *testing.T, raws []json.RawMessage) []wsEvent {
	t.Helper()
	out := make([]wsEvent, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			t.Fatal(err)
		}
		if out[i].Type != string(protocol.EventTypeNotice) {
			continue
		}
		v, _ := protocol.NewEvent(protocol.EventTypeNotice)
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("notice %s: %v", raw, err)
		}
		if n := v.(*protocol.NoticeEvent); !noticeKeys[n.Key] || n.Params == nil {
			t.Errorf("notice %s: key %q params %v", raw, n.Key, n.Params)
		}
	}
	return out
}

// findAfter は、cond を満たすイベントの直後にある notice を返す（見つからなければ -1 と nil）。
func findAfter(evs []wsEvent, cond func(wsEvent) bool) (int, *wsEvent) {
	for i, ev := range evs {
		if cond(ev) && i+1 < len(evs) && evs[i+1].Type == string(protocol.EventTypeNotice) {
			return i, &evs[i+1]
		}
	}
	return -1, nil
}

// logNotice は i 番目の生のイベントをログに出す（PR に載せる例）。
func (r *noticeRun) logNotice(i int) {
	r.t.Helper()
	r.t.Logf("notice: %s", r.events[i])
}

// NW-1: 開会式。
func TestNoticeWSOpening(t *testing.T) {
	t.Parallel()
	r := startNoticeSingle(t, "s_nw.1")
	evs := r.decoded()
	i, n := findAfter(evs, func(ev wsEvent) bool { return ev.Type == string(sim.EvLeaderElected) && ev.node(1) })
	if n == nil || n.Key != noticeLeaderElected || n.IslandID != 1 {
		t.Fatalf("no leader.elected notice after node.leader_elected{1}: %s", mustJSON(t, r.events))
	}
	r.logNotice(i + 1)
	for _, ev := range evs {
		if ev.Key == noticeNodeCampaign {
			t.Errorf("node.campaign in the opening: %+v", ev)
		}
	}
}

// NW-2・NW-3: 依頼と seq。
func TestNoticeWSRequestAndSeq(t *testing.T) {
	t.Parallel()
	r := startNoticeSingle(t, "s_nw.2")
	r.advance(5000)
	evs := r.decoded()

	// NW-2
	i, n := findAfter(evs, func(ev wsEvent) bool {
		return ev.Type == string(sim.EvRequestCreated) && ev.Request != nil && ev.Request.Origin == 3
	})
	if n == nil || n.Key != noticeRequestCreated || n.IslandID != 3 {
		t.Fatalf("no request.created notice after request.created{origin 3}: %s", mustJSON(t, r.events))
	}
	var params struct {
		Params struct {
			ReqID string `json:"reqId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(r.events[i+1], &params); err != nil {
		t.Fatal(err)
	}
	if params.Params.ReqID != evs[i].Request.ID || params.Params.ReqID == "" {
		t.Errorf("params.reqId = %q, want %q", params.Params.ReqID, evs[i].Request.ID)
	}
	r.logNotice(i + 1)

	// NW-3
	for k, ev := range evs {
		if want := r.snap.LastSeq + 1 + uint64(k); ev.Seq != want {
			t.Fatalf("events[%d].seq = %d, want %d", k, ev.Seq, want)
		}
	}
	last := evs[len(evs)-1].Seq
	r.c.send(protocol.MessageTypeCmdSync, "sync", struct{}{})
	r.c.expectAck("sync")
	var snap protocol.Snapshot
	r.c.expect(protocol.MessageTypeSnapshot, &snap)
	if snap.LastSeq < last {
		t.Fatalf("snapshot lastSeq %d < last received seq %d", snap.LastSeq, last)
	}
	before := len(r.events)
	r.advance(simNow(t, r.sess) + 20*sim.Second)
	after := decodeWSEvents(t, r.events[before:])
	if len(after) == 0 {
		t.Fatal("no events after cmd.sync")
	}
	for k, ev := range after {
		if want := snap.LastSeq + 1 + uint64(k); ev.Seq != want {
			t.Fatalf("after cmd.sync: events[%d].seq = %d, want %d", k, ev.Seq, want)
		}
	}
}

// NW-4: 音信不通。
func TestNoticeWSSilent(t *testing.T) {
	t.Parallel()
	r := startNoticeSingle(t, "s_nw.4")
	r.advance(30000)
	evs := r.decoded()
	for id := uint64(1); id <= 3; id++ {
		i, n := findAfter(evs, func(ev wsEvent) bool { return ev.Type == string(sim.EvIslandUpdated) && ev.node(id) })
		if n == nil || n.Key != noticeIslandSilent || n.IslandID != id || n.T != 30000 {
			t.Fatalf("island %d: no island.silent notice after island.updated: %+v", id, n)
		}
		if id == 1 {
			r.logNotice(i + 1)
		}
	}
}

// NW-5: トレース。
func TestNoticeWSTrace(t *testing.T) {
	t.Parallel()
	r := startNoticeSingle(t, "s_nw.5")
	r.advance(5000)

	ref, _ := refStageEngine(t, noticeSeed)
	ref.Apply(sim.Command{Type: sim.CmdGameStart})
	for at := sim.Second; at <= 5000; at += sim.Second {
		ref.RunUntil(at)
	}
	var want []string
	for _, ev := range ref.DrainEvents() {
		want = append(want, string(ev.Type))
	}

	var got []string
	var seq uint64
	for _, ev := range r.decoded() {
		if ev.Type == string(protocol.EventTypeNotice) {
			continue
		}
		seq++
		if ev.Seq-r.snap.LastSeq < seq {
			t.Fatalf("seq %d is smaller than the engine seq %d", ev.Seq, seq)
		}
		got = append(got, ev.Type)
	}
	if !slices.Equal(got, want) {
		t.Errorf("types without notice differ:\n got %v\nwant %v", got, want)
	}
	var sessHash [32]byte
	probe(t, r.sess, func(s *session) { sessHash = s.engine.TraceHash() })
	if refHash := ref.TraceHash(); sessHash != refHash {
		t.Errorf("TraceHash differs: session %x, direct %x", sessHash, refHash)
	}
}
