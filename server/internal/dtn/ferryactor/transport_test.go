package ferryactor_test

import (
	"encoding/json"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// transportFor は st の ferry.Transport（参照モデル）を作る。
func transportFor(t *testing.T, st *stage.Stage) *ferry.Transport {
	t.Helper()
	cfg := ferry.Config{DockMs: st.Transport.DockMs, PortCap: st.Transport.PortCap, PerLinkAppCap: st.Transport.PerLinkAppCap, RelayCap: st.Transport.RelayCap}
	for i := range st.Islands {
		cfg.Islands = append(cfg.Islands, uint64(i+1))
	}
	for _, r := range st.Routes {
		cfg.Routes = append(cfg.Routes, ferry.RouteConfig{A: r.A, B: r.B, TravelMs: r.TravelMs})
	}
	for _, f := range st.Ferries {
		cfg.Ferries = append(cfg.Ferries, ferry.FerryConfig{ID: ferry.FerryID(f.ID), Name: f.Name, Home: f.Home, Capacity: f.Capacity, SpeedPermille: f.SpeedPermille})
	}
	tr, err := ferry.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestFerryTransportScenario は、ferry.Transport と Ferry Actor に同じ操作をして、各段の船の状態
// （状態・位置・時刻・Cargo の順）が一致することを確かめる。経由港で宛先でない手紙が Cargo に残り、順が変わらない。
func TestFerryTransportScenario(t *testing.T) {
	st := repoStage(t, "stage1") // 航路 1-2・2-3
	codec := codecFor(st)
	tr := transportFor(t, st)
	a := newFerry(t)
	const fid = ferry.FerryID("f1")

	// 島 1 の港に、島 3 宛て・島 2 宛て・島 3 宛ての手紙を入れる
	u := func(v uint64) *uint64 { return &v }
	msgs := []*pb.Message{
		{Type: pb.MsgHeartbeat.Enum(), From: u(1), To: u(3), Term: u(2)},
		{Type: pb.MsgHeartbeat.Enum(), From: u(1), To: u(2), Term: u(2)},
		{Type: pb.MsgApp.Enum(), From: u(1), To: u(3), Term: u(2)},
	}
	created, dropped, err := tr.PostRaft(100, 1, msgs)
	if err != nil || len(dropped) != 0 || len(created) != 3 {
		t.Fatalf("post: %v %v %v", created, dropped, err)
	}
	// Transport の ID（全体の連番）→ BPv7 の letterId（島 1 の n 通目）
	ids := map[ferry.LetterID]ferry.LetterID{}
	letter := func(id ferry.LetterID) dtnproto.Letter {
		l, _ := tr.Letter(id)
		ids[id] = letterID(l.From, uint64(id))
		l.ID = ids[id]
		return encodeLetter(t, codec, l)
	}
	mapIDs := func(in []ferry.LetterID) []ferry.LetterID {
		out := make([]ferry.LetterID, len(in))
		for i, id := range in {
			out[i] = ids[id]
		}
		return out
	}
	check := func(stepName string) {
		t.Helper()
		f, _ := tr.Ferry(fid)
		raw, err := a.Inspect()
		if err != nil {
			t.Fatal(err)
		}
		var iv struct {
			State    string `json:"state"`
			At       uint64 `json:"at"`
			From     uint64 `json:"from"`
			To       uint64 `json:"to"`
			DepartAt int64  `json:"departAt"`
			ArriveAt int64  `json:"arriveAt"`
			ReadyAt  int64  `json:"readyAt"`
			Cargo    []struct {
				LetterID ferry.LetterID `json:"letterId"`
			} `json:"cargo"`
		}
		if err := json.Unmarshal(raw, &iv); err != nil {
			t.Fatal(err)
		}
		cargo := make([]ferry.LetterID, len(iv.Cargo))
		for i, c := range iv.Cargo {
			cargo[i] = c.LetterID
		}
		state := map[ferry.FerryState]string{ferry.Docked: "docked", ferry.Sailing: "sailing"}[f.State]
		if iv.State != state || iv.At != f.At || iv.From != f.From || iv.To != f.To || iv.DepartAt != f.DepartAt ||
			iv.ArriveAt != f.ArriveAt || iv.ReadyAt != f.ReadyAt || !slices.Equal(cargo, mapIDs(f.Cargo)) {
			t.Fatalf("%s: actor %s, transport %+v", stepName, raw, f)
		}
	}
	check("init")

	// 積む（渡した順）
	order := []ferry.LetterID{created[2], created[0], created[1]}
	if err := tr.Load(fid, order); err != nil {
		t.Fatal(err)
	}
	mustDo(t, a, cargoAdd(200, letter(order[0]), letter(order[1]), letter(order[2])))
	check("load")

	// 出航・島 2 に到着。Transport は島 2 宛てを一度に配達し、Actor は arrive の後の cargo.remove で除く
	arriveAt, err := tr.Depart(300, fid, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustDo(t, a, depart(300, 2, arriveAt))
	check("depart 1→2")
	delivered, err := tr.Arrive(arriveAt, fid)
	if err != nil || !slices.Equal(delivered, []ferry.LetterID{created[1]}) {
		t.Fatalf("arrive: %v %v", delivered, err)
	}
	mustDo(t, a, arrive(arriveAt, 2))
	mustDo(t, a, cargoRemove(mapIDs(delivered)...))
	check("arrive at 2")

	// 島 3 宛ての 2 通は Cargo に残り、順は変わらない。もう 1 通積んで島 3 へ
	f, _ := tr.Ferry(fid)
	if !slices.Equal(f.Cargo, []ferry.LetterID{created[2], created[0]}) {
		t.Fatalf("transit cargo = %v", f.Cargo)
	}
	more, _, err := tr.PostRaft(arriveAt, 2, []*pb.Message{{Type: pb.MsgHeartbeatResp.Enum(), From: u(2), To: u(3), Term: u(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Load(fid, more); err != nil {
		t.Fatal(err)
	}
	mustDo(t, a, cargoAdd(arriveAt, letter(more[0])))
	check("load at 2")
	ready := f.ReadyAt
	arriveAt3, err := tr.Depart(ready, fid, 3)
	if err != nil {
		t.Fatal(err)
	}
	mustDo(t, a, depart(ready, 3, arriveAt3))
	check("depart 2→3")
	delivered, err = tr.Arrive(arriveAt3, fid)
	if err != nil || len(delivered) != 3 {
		t.Fatalf("arrive at 3: %v %v", delivered, err)
	}
	mustDo(t, a, arrive(arriveAt3, 3))
	mustDo(t, a, cargoRemove(mapIDs(delivered)...))
	check("arrive at 3")

	// 断る操作も同じ: 停泊時間の前の出航
	if _, err := tr.Depart(arriveAt3+1, fid, 2); err == nil {
		t.Fatal("transport departed before readyAt")
	}
	if _, err := depart(arriveAt3+1, 2, arriveAt3+5252).do(t, a); errCode(err) != dtnproto.CodeInvalid {
		t.Fatalf("actor departed before readyAt: %v", err)
	}
	check("rejected depart")
}
