package ferryactor_test

import (
	"bytes"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

const f1 = dtnproto.ActorRef("ferry:f1")

// TestFerryIdempotentArrive は、Runtime を通して同じ eventSeq の arrive を 2 回送っても状態が 1 回分で、
// 同じ応答の bytes が返ることを確かめる。別の eventSeq の 2 回目は Docked なので invalid。
func TestFerryIdempotentArrive(t *testing.T) {
	st := repoStage(t, "stage1")
	a := ferryactor.New(repoStages(t))
	rt := runtime.New(a)
	defer rt.Close()

	mustOK := func(raw []byte) dtnproto.Response {
		t.Helper()
		resp := rt.Handle(raw)
		if resp.Status != dtnproto.StatusOK {
			t.Fatalf("%s: %+v", raw, resp.Error)
		}
		return resp
	}
	mustOK(request(t, f1, 1, dtnproto.KindInit, initPayload(st, "f1"), false))
	mustOK(request(t, f1, 2, dtnproto.KindDepart, dtnproto.DepartPayload{T: 0, To: 2, ArriveAt: 3750}, false))

	arriveReq := request(t, f1, 3, dtnproto.KindArrive, dtnproto.ArrivePayload{T: 3750, Island: 2}, false)
	first := rt.HandleRaw(arriveReq)
	digest := a.StateDigest()
	second := rt.HandleRaw(arriveReq)
	if !bytes.Equal(first, second) {
		t.Fatalf("resend returned another response:\n%s\n%s", first, second)
	}
	if a.StateDigest() != digest {
		t.Fatal("resend changed the state")
	}
	resp := mustOK(arriveReq)
	if len(resp.Outputs) != 1 || resp.Outputs[0].Seq != 3 || resp.Cursor == nil || resp.Cursor.StateDigest != digest {
		t.Fatalf("arrive response = %+v", resp)
	}

	// 同じ eventSeq で中身が違う → conflict。別の eventSeq の同じ到着 → invalid（状態は変わらない）
	if r := rt.Handle(request(t, f1, 3, dtnproto.KindArrive, dtnproto.ArrivePayload{T: 3750, Island: 3}, false)); r.Error == nil || r.Error.Code != dtnproto.CodeConflict {
		t.Fatalf("conflict: %+v", r)
	}
	if r := rt.Handle(request(t, f1, 4, dtnproto.KindArrive, dtnproto.ArrivePayload{T: 3750, Island: 2}, false)); r.Error == nil || r.Error.Code != dtnproto.CodeInvalid {
		t.Fatalf("2nd arrive with another eventSeq: %+v", r)
	}
	if a.StateDigest() != digest {
		t.Fatal("rejected arrive changed the state")
	}
	// 次の要求の seq は 4（失敗した要求は seq を進めない）
	resp = mustOK(request(t, f1, 5, dtnproto.KindDepart, dtnproto.DepartPayload{T: 5250, To: 3, ArriveAt: 10500}, false))
	if resp.Outputs[0].Seq != 4 || resp.Outputs[0].Class != 8 || resp.Outputs[0].T != 5250 {
		t.Fatalf("depart output = %+v", resp.Outputs[0])
	}
}

// TestFerryReplayDeterministic は、要求の列を新しい Actor に init から再生して、応答の digest と
// stateDigest が一致することを確かめる（persistence-and-recovery.md §4.1。bundle.export・inspect は journal に
// 入らないので再生しない）。
func TestFerryReplayDeterministic(t *testing.T) {
	st := repoStage(t, "stage1")
	codec := codecFor(st)
	L := func(n uint64, to uint64, createdAt int64) dtnproto.Letter {
		return raftLetter(t, codec, letterID(1, n), 1, to, createdAt)
	}
	type call struct {
		kind    dtnproto.Kind
		payload any
	}
	calls := []call{
		{dtnproto.KindInit, initPayload(st, "f1")},
		{dtnproto.KindCargoAdd, dtnproto.LettersPayload{T: 100, Letters: []dtnproto.Letter{L(1, 3, 10), L(2, 2, 20), L(3, 3, 30)}}},
		{dtnproto.KindBundleExport, dtnproto.ExportPayload{T: 150, LetterIDs: []ferry.LetterID{letterID(1, 2)}}},
		{dtnproto.KindDepart, dtnproto.DepartPayload{T: 200, To: 2, ArriveAt: 3950}},
		{dtnproto.KindCargoAdd, dtnproto.LettersPayload{T: 300, Letters: []dtnproto.Letter{L(4, 3, 40)}}}, // 航行中 → invalid（応答は記録される）
		{dtnproto.KindArrive, dtnproto.ArrivePayload{T: 3950, Island: 2}},
		{dtnproto.KindInspect, dtnproto.EmptyPayload{}},
		{dtnproto.KindCargoRemove, dtnproto.LetterIDsPayload{LetterIDs: []ferry.LetterID{letterID(1, 2)}}},
		{dtnproto.KindCargoAdd, dtnproto.LettersPayload{T: 4000, Letters: []dtnproto.Letter{raftLetter(t, codec, letterID(2, 1), 2, 3, 3990)}}},
		{dtnproto.KindDepart, dtnproto.DepartPayload{T: 5450, To: 3, ArriveAt: 10700}},
		{dtnproto.KindArrive, dtnproto.ArrivePayload{T: 10700, Island: 3}},
		{dtnproto.KindBundleExport, dtnproto.ExportPayload{T: 10700, LetterIDs: []ferry.LetterID{letterID(1, 1), letterID(2, 1), letterID(1, 3)}}},
		{dtnproto.KindCargoRemove, dtnproto.LetterIDsPayload{LetterIDs: []ferry.LetterID{letterID(1, 1), letterID(2, 1), letterID(1, 3)}}},
	}

	type journalRow struct {
		raw    func(replay bool) []byte
		digest string
	}
	run := func(rt *runtime.Runtime, rows []journalRow) []journalRow {
		var journal []journalRow
		seq := uint64(0)
		for i, c := range calls {
			ki, _ := dtnproto.LookupKind(c.kind)
			s := uint64(0)
			if ki.Journaled {
				seq++
				s = seq
			}
			c := c
			mk := func(replay bool) []byte { return request(t, f1, s, c.kind, c.payload, replay) }
			resp := rt.Handle(mk(rows != nil))
			if i == 4 {
				if resp.Error == nil || resp.Error.Code != dtnproto.CodeInvalid {
					t.Fatalf("call %d (%s): %+v, want invalid", i, c.kind, resp)
				}
			} else if resp.Status != dtnproto.StatusOK {
				t.Fatalf("call %d (%s): %+v", i, c.kind, resp.Error)
			}
			if !ki.Journaled {
				continue
			}
			d, err := dtnproto.ResponseDigest(resp)
			if err != nil {
				t.Fatal(err)
			}
			journal = append(journal, journalRow{raw: mk, digest: d})
		}
		return journal
	}

	a1 := ferryactor.New(repoStages(t))
	rt1 := runtime.New(a1)
	defer rt1.Close()
	journal := run(rt1, nil)

	// 新しい Actor に、journal の行だけを replay: true で再生する
	a2 := ferryactor.New(repoStages(t))
	rt2 := runtime.New(a2)
	defer rt2.Close()
	for i, row := range journal {
		resp := rt2.Handle(row.raw(true))
		d, err := dtnproto.ResponseDigest(resp)
		if err != nil {
			t.Fatal(err)
		}
		if d != row.digest {
			t.Fatalf("journal row %d: response digest %s, want %s (%+v)", i, d, row.digest, resp)
		}
	}
	if a1.StateDigest() != a2.StateDigest() || a1.Cursor() != a2.Cursor() {
		t.Fatalf("stateDigest after replay %s, want %s", a2.StateDigest(), a1.StateDigest())
	}
	i1, _ := a1.Inspect()
	i2, _ := a2.Inspect()
	if !bytes.Equal(i1, i2) {
		t.Fatalf("inspect after replay:\n%s\n%s", i2, i1)
	}

	// 3 つ目の Actor（すべての要求をもう一度。export・inspect を混ぜても同じ）
	a3 := ferryactor.New(repoStages(t))
	rt3 := runtime.New(a3)
	defer rt3.Close()
	again := run(rt3, journal)
	for i := range journal {
		if again[i].digest != journal[i].digest {
			t.Fatalf("row %d differs on a fresh run", i)
		}
	}
	if a3.StateDigest() != a1.StateDigest() {
		t.Fatal("stateDigest differs on a fresh run")
	}
}
