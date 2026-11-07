package sim

import (
	"bytes"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// 役場の再送で二重に消費しないことのシナリオテスト（T-110b・SC-4、FR-REQ-09・FR-REQ-06・ADR-0015）。
//
// SC-1（scenario_partition_test.go）と同じ 5 島の分断で、依頼の再送をテストからの提案ではなく、
// 役場の再送のタイマー（kindRequestRetry）に任せる。テストは提案せず、船を動かし、分断を作るだけ。
//
//  1. 島 1（開会式の Leader）に、花火の依頼 t-0001 を t = 0 に出す
//  2. 島 1 が少数派 {1, 2} に残る分断の間に、島 1 が提案したエントリは過半数に届かずに失われる。
//     RetryMs の後に、島 1 の役場がもう一度提案する（これも失われる）
//  3. 復旧の後、島 1 の役場は新しい代表の島 3 へ依頼書を送る。島 3 が提案し、確定する
//  4. 島 1 がその確定を知る前に、RetryMs の後の依頼書がもう一度島 3 に届き（船 fb を島 3 に留め、
//     空の船で往復させて確実に起こす）、同じ依頼がもう 1 回確定する
//  5. 依頼元の島 1 では 1 回だけ ok で適用され、後のエントリは全島で duplicate になる。
//     依頼は succeeded で終わり、花火の予約者は 1 件だけ

// scnReqRetryMs は SC-4 の再送の間隔（台本の定数で、ゲームの数値ではない）。
// 選挙のタイムアウトの最小（ElectionTick 40 × TickMs 500 = 20 秒）より短くして、島 1 が Leader のうちに 1 回は
// 再送が起き（CheckQuorum で退くのは早くて 20 秒）、手順 6. の間も島 1 が代表（島 3）を知ったままでいるようにする。
const scnReqRetryMs SimTime = 10000

// scnReqID は SC-4 の依頼の ID（reqConfig の StageID "t" の 1 件目）。
const scnReqID = "t-0001"

// scnReqFmStops は SC-4 の船 fm の巡回（台本の値）。3 → 5 → 4 → 3 → 4 → 5 の 8 の字で、島 3 に 3 区間ごとに寄り、
// 島 4・5 の間の手紙も両方の向きに運ぶ。SC-1 の巡回 [3, 4, 5] は、分断を t = 0 に始めると島 3・4・5 のログが同じ長さに
// なるので、シード 1〜6 のうち 1・5・6 で多数派の Leader が島 3 にならない（[3, 4, 3, 5] ではシード 1 で Leader が
// 360 秒の間に決まらない）。このテストでは島 3 が多数派の Leader であることが前提なので、この巡回にする。
func scnReqFmStops() []uint64 { return []uint64{3, 5, 4, 3, 4, 5} }

// scnReqConfig は SC-4 の設定。scnConfig5（5 島。1-3 が少数派 {1, 2} と多数派 {3, 4, 5} をつなぐ唯一の航路）に依頼を入れる。
func scnReqConfig() Config {
	cfg := scnConfig5()
	cfg.Requests = reqConfig(fireworkReq(0, 1)) // reqId は "t-0001"
	cfg.Requests.RetryMs = int64(scnReqRetryMs)
	return cfg
}

// scnReqData は依頼のコマンドのバイト列（game.Request.Command() と同じ値）。
func scnReqData(t *testing.T) []byte {
	t.Helper()
	data, err := ledger.Encode(scnFirework(scnReqID))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

// scnReqEntries は島 id のログのうち、依頼のエントリを返す。
func scnReqEntries(t *testing.T, e *Engine, id uint64) []cluster.LogEntry {
	t.Helper()
	data := scnReqData(t)
	l, err := e.nodes[id-1].Log()
	if err != nil {
		t.Fatalf("node %d: Log: %v", id, err)
	}
	var out []cluster.LogEntry
	for _, ent := range l {
		if bytes.Equal(ent.Data, data) {
			out = append(out, ent)
		}
	}
	return out
}

// scnReqApplied は evs に、島 id での依頼の ledger.applied があるかを返す。
func scnReqApplied(evs []DomainEvent, id uint64) bool {
	return len(scnResults(evs, id, scnReqID)) > 0
}

// scnReqUpdates は evs のうち依頼の request.updated を出た順に返す。
func scnReqUpdates(evs []DomainEvent) []RequestUpdated {
	var out []RequestUpdated
	for _, d := range ofType[RequestUpdated](evs) {
		if d.ID == scnReqID {
			out = append(out, d)
		}
	}
	return out
}

// scnReqLetterOK は l が島 1 から島 3 への依頼の依頼書かを返す。
func scnReqLetterOK(l LetterView) bool {
	return l.Kind == "request" && l.Label == "request" && l.From == 1 && l.To == 3 &&
		l.Request != nil && l.Request.ReqID == scnReqID
}

// scnReqDelivered は evs[from:] の letter.delivered のうち、島 island に届いた、want を満たす手紙を返す（届いた順）。
func scnReqDelivered(evs []DomainEvent, from int, island uint64, want func(LetterView) bool) []LetterView {
	ls := scnLetters(evs)
	var out []LetterView
	for _, d := range ofType[LetterDelivered](evs[from:]) {
		if d.IslandID != island {
			continue
		}
		if l := scnLetter(ls, d.LetterID); want(l) {
			out = append(out, l)
		}
	}
	return out
}

// scnReqDeliveredAt は evs の中で、手紙 id が島 island に届いた時刻を返す。
func scnReqDeliveredAt(evs []DomainEvent, id, island uint64) (SimTime, bool) {
	for _, ev := range evs {
		if d, ok := ev.Data.(LetterDelivered); ok && d.LetterID == id && d.IslandID == island {
			return ev.T, true
		}
	}
	return 0, false
}

// scnReqDocked は船 tour が島 island に停泊しているかを返す。
func scnReqDocked(e *Engine, tour *scnTour, island uint64) bool {
	f, _ := e.tr.Ferry(ferry.FerryID(tour.ferry))
	return f.State == ferry.Docked && f.At == island
}

// scnReqDockedAt は船 tour が島 island に停泊していて、出航できる（ReadyAt <= Now）かを返す。
func scnReqDockedAt(e *Engine, tour *scnTour, island uint64) bool {
	f, _ := e.tr.Ferry(ferry.FerryID(tour.ferry))
	return scnReqDocked(e, tour, island) && SimTime(f.ReadyAt) <= e.Now()
}

func TestScenarioRequestRetryNoDoubleSpend(t *testing.T) { // SC-4
	t.Parallel()
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run("seed="+strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			scnReqRetry(t, seed)
		})
	}
}

// scnReqRetry は SC-4 の台本。
func scnReqRetry(t *testing.T, seed uint64) {
	t.Helper()

	// 1. 始め
	e := scnStart(t, scnReqConfig(), seed)
	r := &scnRec{}
	r.drain(e)
	fb := &scnTour{ferry: "fb", stops: []uint64{1, 2}}
	fm := &scnTour{ferry: "fm", stops: scnReqFmStops()}
	tours := []*scnTour{fb, fm}

	// 2. 分断（依頼の発生の前）
	if e.Now() != 0 {
		scnFatal(t, e, r, "Now = %d before the partition, want 0", e.Now())
	}
	tH := scnPartitionMs
	if err := e.tr.Block([][2]uint64{{1, 3}}, int64(tH)); err != nil {
		t.Fatalf("Block: %v", err)
	}

	// 3. 分断の間の再送
	scnRun(t, e, r, scnReqRetryMs, tours, nil)
	if got := scnReqEntries(t, e, 1); len(got) != 2 || got[0].Term != 2 || got[1].Term != 2 {
		scnFatal(t, e, r, "island 1 at %d: entries of %s = %+v, want 2 with Term 2 (proposal and retry)", scnReqRetryMs, scnReqID, got)
	}
	var lead, termL uint64
	var tL SimTime
	watch := func() bool {
		if lead != 0 {
			return false
		}
		if l := scnLeaderAmong(e, []uint64{3, 4, 5}); l != 0 {
			lead, termL, tL = l, e.nodes[l-1].Status().Term, e.Now()
		}
		return false
	}
	scnRun(t, e, r, tH, tours, watch)

	// 4. 分断の間の検査
	part := scnBetween(r.evs, 0, tH)
	for _, a := range ofType[LedgerApplied](part) {
		if a.ReqID == scnReqID {
			t.Errorf("ledger.applied of %s during partition: %+v", scnReqID, a)
		}
	}
	for _, u := range scnReqUpdates(part) {
		if u.Status != string(game.StatusProposed) && u.Status != string(game.StatusWaiting) {
			t.Errorf("request.updated during partition = %+v, want proposed or waiting", u)
		}
	}
	if !slices.Contains(ofType[SteppedDown](part), SteppedDown{ID: 1, Term: 2, Reason: "check_quorum"}) {
		t.Errorf("CheckQuorum: stepped_down = %+v, want {1 2 check_quorum}", ofType[SteppedDown](part))
	}
	if lead != 3 {
		scnFatal(t, e, r, "leader in majority = %d, want 3", lead)
	}
	if termL <= 2 {
		t.Errorf("term of leader 3 = %d, want > 2", termL)
	}
	for _, id := range []uint64{3, 4, 5} {
		if got := scnReqEntries(t, e, id); len(got) != 0 {
			t.Errorf("island %d at tH: entries of %s = %+v, want none", id, scnReqID, got)
		}
	}

	// 5. 復旧と 1 回目の依頼書
	// fb は [1, 2] のまま島 1 まで進め、そこから島 1・3 の往復にする。巡回 [1, 3, 1, 2] では、島 1 が代表を知るのは
	// fb が島 3 から着いたときで、次は島 2 へ回るので、1 回目の依頼書が島 3 に着く前（または港に残っている間）に
	// RetryMs の後の依頼書ができ、2 通が同じ船で同時に届く（シード 2・4 など）。往復なら、島 1 で積んだ依頼書は
	// 次に島 3 で必ず降ろされるので、1 回目の依頼書だけが先に届く。
	nH := len(r.evs)
	if !scnRun(t, e, r, tH+scnConvergeMs, tours, func() bool { return scnReqDocked(e, fb, 1) }) {
		scnFatal(t, e, r, "fb did not reach island 1 after recovery")
	}
	fb.setStops([]uint64{1, 3})
	var c1 SimTime
	hook := func() bool {
		got := scnReqDelivered(r.evs, nH, 3, scnReqLetterOK)
		if len(got) == 0 {
			return false
		}
		c1 = got[0].CreatedAt
		return true
	}
	if !scnRun(t, e, r, tH+scnConvergeMs, tours, hook) {
		scnFatal(t, e, r, "no request letter from 1 delivered to 3 by tH + %d", scnConvergeMs)
	}
	if scnReqApplied(r.evs, 1) {
		scnFatal(t, e, r, "island 1 applied %s before the second request letter", scnReqID)
	}
	if got := scnReqEntries(t, e, 3); len(got) != 1 || got[0].Term < termL {
		scnFatal(t, e, r, "island 3 after the first letter: entries = %+v, want 1 with Term >= %d", got, termL)
	}
	if us := scnReqUpdates(r.evs); len(us) == 0 || us[len(us)-1].Status != string(game.StatusProposed) {
		scnFatal(t, e, r, "request.updated after the first letter = %+v, want last proposed", us)
	}

	// 6. 2 回目の依頼書を待つ（船 fb を島 3 に留める）
	hold := []*scnTour{fm}
	var l2 LetterView
	hook2 := func() bool {
		for _, d := range ofType[LetterCreated](r.evs) {
			if scnReqLetterOK(d.Letter) && d.Letter.CreatedAt > c1 {
				l2 = d.Letter
				return true
			}
		}
		return false
	}
	if !scnRun(t, e, r, c1+2*scnReqRetryMs, hold, hook2) {
		scnFatal(t, e, r, "no second request letter from 1 to 3 by c1 + %d (c1 = %d, island 1 = %+v)",
			2*scnReqRetryMs, c1, e.nodes[0].Status())
	}
	id2 := l2.ID
	if l2.CreatedAt != c1+scnReqRetryMs {
		scnFatal(t, e, r, "FR-REQ-09: second request letter created at %d, want c1 + %d = %d (island 1 = %+v)",
			l2.CreatedAt, scnReqRetryMs, c1+scnReqRetryMs, e.nodes[0].Status())
	}
	if scnReqApplied(r.evs, 1) {
		scnFatal(t, e, r, "island 1 applied %s before the second request letter was carried", scnReqID)
	}

	// 7. 2 回目の依頼書を島 3 へ運ぶ（島 1 へは何も届けない）
	if f, _ := e.tr.Ferry("fb"); f.State == ferry.Docked && SimTime(f.ReadyAt) > e.Now() {
		scnRun(t, e, r, SimTime(f.ReadyAt), hold, nil)
	}
	if !scnReqDockedAt(e, fb, 3) {
		f, _ := e.tr.Ferry("fb")
		scnFatal(t, e, r, "fb = %+v, want docked at 3 and ready", f)
	}
	f, _ := e.tr.Ferry("fb")
	for _, lid := range f.Cargo {
		if l, ok := e.tr.Letter(lid); !ok || l.To == 1 {
			scnFatal(t, e, r, "fb at 3 carries letter %d to island 1 (%+v)", lid, l)
		}
	}
	n7 := len(r.evs)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "fb", To: 1})
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, hold, func() bool { return scnReqDockedAt(e, fb, 1) }) {
		scnFatal(t, e, r, "fb did not reach island 1 from 3")
	}
	if got := ofType[LetterDelivered](r.evs[n7:]); slices.ContainsFunc(got, func(d LetterDelivered) bool { return d.IslandID == 1 }) {
		scnFatal(t, e, r, "letters delivered to island 1 while carrying the second request letter: %+v", got)
	}
	if scnReqApplied(r.evs, 1) {
		scnFatal(t, e, r, "island 1 applied %s before the second request letter reached 3", scnReqID)
	}
	if l, ok := e.tr.Letter(ferry.LetterID(id2)); !ok || l.Location.Kind != ferry.LocPort || l.Location.Island != 1 {
		scnFatal(t, e, r, "second request letter %d = %+v, want in port 1", id2, l)
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "fb", LetterIDs: []uint64{id2}})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "fb", To: 3})
	delivered2 := func() bool { _, ok := scnReqDeliveredAt(r.evs, id2, 3); return ok }
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, hold, delivered2) {
		scnFatal(t, e, r, "second request letter %d not delivered to 3", id2)
	}
	t2, _ := scnReqDeliveredAt(r.evs, id2, 3)
	if got := scnReqEntries(t, e, 3); len(got) != 2 || got[0].Term != got[1].Term {
		scnFatal(t, e, r, "island 3 after the second letter: entries = %+v, want 2 with the same Term", got)
	}
	if scnReqApplied(r.evs, 1) {
		scnFatal(t, e, r, "island 1 applied %s before the second proposal", scnReqID)
	}

	// 8. 再開と収束
	fb.setStops([]uint64{1, 3, 1, 2})
	hook3 := func() bool {
		return scnConverged(t, e) && len(scnResults(r.evs, 1, scnReqID)) >= 2
	}
	if !scnRun(t, e, r, e.Now()+scnConvergeMs, tours, hook3) {
		scnFatal(t, e, r, "not converged with 2 results of %s at island 1", scnReqID)
	}

	// 9. 最後の検査
	first := scnResults(r.evs, 1, scnReqID)
	counts := make([]int, e.cfg.Islands)
	for id := uint64(1); id <= uint64(e.cfg.Islands); id++ {
		got := scnResults(r.evs, id, scnReqID)
		counts[id-1] = len(got)
		if len(got) < 2 || got[0] != ledger.StatusOK {
			t.Errorf("FR-REQ-06: island %d: %s = %v, want ok then duplicate", id, scnReqID, got)
		}
		for i := 1; i < len(got); i++ {
			if got[i] != ledger.StatusDuplicate {
				t.Errorf("FR-REQ-06: island %d: %s[%d] = %s, want duplicate (%v)", id, scnReqID, i, got[i], got)
			}
		}
		if !reflect.DeepEqual(got, first) {
			t.Errorf("island %d: %s = %v, want the same as island 1 %v", id, scnReqID, got, first)
		}
		ents := scnReqEntries(t, e, id)
		if len(ents) != len(got) {
			t.Errorf("island %d: %d entries of %s, want %d (results)", id, len(ents), scnReqID, len(got))
		}
		for _, ent := range ents {
			if ent.Term == 2 {
				t.Errorf("island %d: entry %+v of term 2 (lost during partition) remains", id, ent)
			}
		}
		s := e.ledgers[id-1].Snapshot()
		i := slices.IndexFunc(s.Resources, func(rs ledger.ResourceState) bool { return rs.ID == "firework:a" })
		if i < 0 || !reflect.DeepEqual(s.Resources[i].Holders, []string{scnReqID}) {
			t.Errorf("island %d: firework:a = %+v, want holders [%s]", id, s.Resources, scnReqID)
		}
	}
	us := scnReqUpdates(r.evs)
	var succeeded int
	for _, u := range us {
		switch u.Status {
		case string(game.StatusSucceeded):
			succeeded++
			if u.Result == nil || u.Result.Status != ledger.StatusOK {
				t.Errorf("ADR-0015: succeeded with result %+v, want ok", u.Result)
			}
		case string(game.StatusRejected):
			t.Errorf("request.updated = %+v, want no rejected", u)
		}
	}
	if succeeded != 1 || len(us) == 0 || us[len(us)-1].Status != string(game.StatusSucceeded) {
		t.Errorf("request.updated = %+v, want exactly 1 succeeded as the last", us)
	}
	if e.Phase() != PhaseSailing {
		t.Errorf("Phase = %s, want sailing", e.Phase())
	}
	t.Logf("seed %d: leader=%d elected at %d (term %d) c1=%d id2=%d delivered at %d results=%v",
		seed, lead, tL, termL, c1, id2, t2, counts)
}
