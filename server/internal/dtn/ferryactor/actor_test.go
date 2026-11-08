package ferryactor_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

func TestFerryInit(t *testing.T) {
	st := repoStage(t, "stage1")
	a := newFerry(t)
	if got := a.Cursor(); got.Horizon != (dtnproto.Mark{}) || got.StateDigest != a.StateDigest() {
		t.Fatalf("cursor = %+v", got)
	}
	raw, err := a.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		At          uint64 `json:"at"`
		Capacity    int    `json:"capacity"`
		StateDigest string `json:"stateDigest"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.ID != "f1" || v.State != "docked" || v.At != 1 || v.Capacity != 6 || v.StateDigest != a.StateDigest() {
		t.Fatalf("inspect = %s", raw)
	}
	if _, err := a.Init(initPayload(st, "f1")); errCode(err) != dtnproto.CodeInvalid {
		t.Fatalf("2nd init: %v", err)
	}

	bad := func(mut func(p *dtnproto.InitPayload)) dtnproto.InitPayload {
		p := initPayload(st, "f1")
		mut(&p)
		return p
	}
	for name, tc := range map[string]struct {
		p    dtnproto.InitPayload
		want dtnproto.Code
	}{
		"unknown stage": {bad(func(p *dtnproto.InitPayload) { p.Stage = "nope" }), dtnproto.CodeInvalid},
		"stage hash":    {bad(func(p *dtnproto.InitPayload) { p.StageHash = "sha256:" + strings.Repeat("0", 64) }), dtnproto.CodeInvalid},
		"unknown ferry": {bad(func(p *dtnproto.InitPayload) { p.Actor = "ferry:f9" }), dtnproto.CodeUnknownActor},
		"island actor":  {bad(func(p *dtnproto.InitPayload) { p.Actor = "island:1" }), dtnproto.CodeUnknownActor},
		"plan":          {bad(func(p *dtnproto.InitPayload) { p.Plan = []dtnproto.PlannedRequest{{ID: "r1"}} }), dtnproto.CodeInvalid},
		"other stage ok": {bad(func(p *dtnproto.InitPayload) {
			*p = initPayload(repoStage(t, "stage2"), repoStage(t, "stage2").Ferries[0].ID)
		}), ""},
	} {
		a := ferryactor.New(repoStages(t))
		before := a.StateDigest()
		_, err := a.Init(tc.p)
		if errCode(err) != tc.want {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if err != nil && a.StateDigest() != before {
			t.Errorf("%s: failed init changed the state", name)
		}
	}
}

func TestFerryTransitions(t *testing.T) {
	st := repoStage(t, "stage1") // f1: home 1・capacity 6・dockMs 1500。航路 1-2（3750）・2-3（5250）
	codec := codecFor(st)
	// L(n) は島 1 の n 通目（宛先 3）、M(n) は島 1 の n 通目（宛先 2）
	L := func(n uint64) dtnproto.Letter { return raftLetter(t, codec, letterID(1, n), 1, 3, 0) }
	M := func(n uint64) dtnproto.Letter { return raftLetter(t, codec, letterID(1, n), 1, 2, 0) }
	id := func(n uint64) ferry.LetterID { return letterID(1, n) }

	big := func() dtnproto.Letter { // bpv7 の Encode は 16 KiB を超える Bundle を作らないので、bytes を直に作る
		b := bytes.Repeat([]byte{7}, 16<<10+1)
		return dtnproto.Letter{LetterID: id(50), Digest: dtnproto.Digest(b), Bundle: b}
	}
	badDigest := func() dtnproto.Letter { l := L(1); l.Digest = M(2).Digest; return l }
	wrongID := func() dtnproto.Letter { l := L(2); l.LetterID = id(1); return l }
	garbage := func() dtnproto.Letter {
		b := []byte("not a bundle")
		return dtnproto.Letter{LetterID: id(1), Digest: dtnproto.Digest(b), Bundle: b}
	}
	six := []dtnproto.Letter{L(1), L(2), L(3), L(4), L(5), L(6)}
	toTeshima := []step{depart(0, 2, 3750), arrive(3750, 2)} // 島 2 に停泊（readyAt 5250）

	docked := func(at uint64, readyAt int64, cargo ...ferry.LetterID) *ferryactor.StateData {
		if cargo == nil {
			cargo = []ferry.LetterID{}
		}
		return &ferryactor.StateData{State: "docked", At: at, ReadyAt: readyAt, Cargo: cargo}
	}
	sailing := func(to uint64, arriveAt, readyAt int64, cargo ...ferry.LetterID) *ferryactor.StateData {
		if cargo == nil {
			cargo = []ferry.LetterID{}
		}
		return &ferryactor.StateData{State: "sailing", To: to, ArriveAt: arriveAt, ReadyAt: readyAt, Cargo: cargo}
	}

	cases := []struct {
		name  string
		setup []step
		step  step
		want  dtnproto.Code           // "" = 成功
		state *ferryactor.StateData   // 成功したときの ferry.state
		class uint8                   // 成功したときの class
		t     int64                   // 成功したときの t
		extra func(*ferryactor.Actor) // 追加の確認
	}{
		// 許される遷移
		{name: "depart empty", step: depart(0, 2, 3750), state: sailing(2, 3750, 0), class: 8},
		{name: "depart with cargo", setup: []step{cargoAdd(0, L(1))}, step: depart(10, 2, 3760), state: sailing(2, 3760, 0, id(1)), class: 8, t: 10},
		{name: "arrive", setup: []step{depart(0, 2, 3750)}, step: arrive(3750, 2), state: docked(2, 5250), class: 1, t: 3750},
		{name: "depart at readyAt", setup: toTeshima, step: depart(5250, 3, 10500), state: sailing(3, 10500, 5250), class: 8, t: 5250},
		{name: "depart back home", setup: toTeshima, step: depart(6000, 1, 9750), state: sailing(1, 9750, 5250), class: 8, t: 6000},
		{name: "cargo.add up to capacity", step: cargoAdd(0, six...), state: docked(1, 0, id(1), id(2), id(3), id(4), id(5), id(6)), class: 8},
		{name: "cargo.add appends in order", setup: []step{cargoAdd(0, L(2), L(1))}, step: cargoAdd(5, L(9)), state: docked(1, 0, id(2), id(1), id(9)), class: 8, t: 5},
		{name: "cargo.remove keeps order", setup: []step{cargoAdd(7, L(1), L(2), L(3), L(4))}, step: cargoRemove(id(3), id(1)), state: docked(1, 0, id(2), id(4)), class: 8, t: 7},
		{name: "cargo.remove all", setup: []step{cargoAdd(7, L(1))}, step: cargoRemove(id(1)), state: docked(1, 0), class: 8, t: 7},
		{name: "cargo.remove uses the arrival t", setup: []step{cargoAdd(0, M(1)), depart(0, 2, 3750), arrive(3750, 2)}, step: cargoRemove(id(1)), state: docked(2, 5250), class: 8, t: 3750},
		{name: "arrive keeps cargo (transit)", setup: []step{cargoAdd(0, L(1), M(2), L(3)), depart(0, 2, 3750)}, step: arrive(3750, 2),
			state: docked(2, 5250, id(1), id(2), id(3)), class: 1, t: 3750},
		{name: "transit letters stay in order", setup: []step{cargoAdd(0, L(1), M(2), L(3)), depart(0, 2, 3750), arrive(3750, 2)}, step: cargoRemove(id(2)),
			state: docked(2, 5250, id(1), id(3)), class: 8, t: 3750},
		{name: "re-add after remove", setup: []step{cargoAdd(0, L(1)), cargoRemove(id(1))}, step: cargoAdd(1, L(1)), state: docked(1, 0, id(1)), class: 8, t: 1},

		// 積載
		{name: "cargo.add over capacity", setup: []step{cargoAdd(0, six...)}, step: cargoAdd(0, L(7)), want: dtnproto.CodeInvalid},
		{name: "cargo.add 7 at once", step: cargoAdd(0, append(six[:6:6], L(7))...), want: dtnproto.CodeInvalid},
		{name: "cargo.add empty", step: cargoAdd(0), want: dtnproto.CodeInvalid},
		{name: "cargo.add duplicate on board", setup: []step{cargoAdd(0, L(1))}, step: cargoAdd(0, L(2), L(1)), want: dtnproto.CodeInvalid},
		{name: "cargo.add duplicate in request", step: cargoAdd(0, L(1), L(1)), want: dtnproto.CodeInvalid},
		{name: "cargo.add bundle over 16 KiB", step: cargoAdd(0, big()), want: dtnproto.CodeTooLarge},
		{name: "cargo.add digest mismatch", step: cargoAdd(0, badDigest()), want: dtnproto.CodeInvalid},
		{name: "cargo.add letterId mismatch", step: cargoAdd(0, wrongID()), want: dtnproto.CodeInvalid},
		{name: "cargo.add not a bundle", step: cargoAdd(0, garbage()), want: dtnproto.CodeInvalid},
		{name: "cargo.add createdAt after t", step: cargoAdd(0, raftLetter(t, codec, id(1), 1, 3, 5)), want: dtnproto.CodeInvalid},
		{name: "cargo.remove unknown", setup: []step{cargoAdd(0, L(1))}, step: cargoRemove(id(2)), want: dtnproto.CodeInvalid},
		{name: "cargo.remove partly unknown", setup: []step{cargoAdd(0, L(1), L(2))}, step: cargoRemove(id(1), id(3)), want: dtnproto.CodeInvalid},
		{name: "cargo.remove empty", setup: []step{cargoAdd(0, L(1))}, step: cargoRemove(), want: dtnproto.CodeInvalid},

		// 時刻・行き先
		{name: "depart before readyAt", setup: toTeshima, step: depart(5249, 3, 10499), want: dtnproto.CodeInvalid},
		{name: "depart to the current island", step: depart(0, 1, 100), want: dtnproto.CodeInvalid},
		{name: "depart arriveAt == t", step: depart(10, 2, 10), want: dtnproto.CodeInvalid},
		{name: "depart arriveAt < t", step: depart(10, 2, 9), want: dtnproto.CodeInvalid},
		{name: "depart unknown island", step: depart(0, 4, 100), want: dtnproto.CodeInvalid},
		{name: "depart while sailing", setup: []step{depart(0, 2, 3750)}, step: depart(1, 3, 5000), want: dtnproto.CodeInvalid},
		{name: "depart before the last t", setup: []step{cargoAdd(100, L(1))}, step: depart(50, 2, 3800), want: dtnproto.CodeInvalid},
		{name: "arrive while docked", step: arrive(0, 1), want: dtnproto.CodeInvalid},
		{name: "arrive at a wrong island", setup: []step{depart(0, 2, 3750)}, step: arrive(3750, 3), want: dtnproto.CodeInvalid},
		{name: "arrive early", setup: []step{depart(0, 2, 3750)}, step: arrive(3749, 2), want: dtnproto.CodeInvalid},
		{name: "arrive late", setup: []step{depart(0, 2, 3750)}, step: arrive(3751, 2), want: dtnproto.CodeInvalid},
		{name: "second arrive", setup: toTeshima, step: arrive(3750, 2), want: dtnproto.CodeInvalid},
		{name: "cargo.add before the last t", setup: toTeshima, step: cargoAdd(3000, L(1)), want: dtnproto.CodeInvalid},

		// 種類
		{name: "advance is not for a ferry", step: step{dtnproto.KindAdvance, dtnproto.AdvancePayload{Horizon: dtnproto.Mark{T: 1}}}, want: dtnproto.CodeInvalid},
		{name: "deliver is not for a ferry", step: step{dtnproto.KindDeliver, dtnproto.LettersPayload{T: 0, Letters: []dtnproto.Letter{L(1)}}}, want: dtnproto.CodeInvalid},
		{name: "export unknown letter", setup: []step{cargoAdd(0, L(1))}, step: export(10, id(2)), want: dtnproto.CodeInvalid},
		{name: "export before createdAt", setup: []step{cargoAdd(10, raftLetter(t, codec, id(1), 1, 3, 10))}, step: export(5, id(1)), want: dtnproto.CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newFerry(t)
			for _, s := range tc.setup {
				mustDo(t, a, s)
			}
			beforeDigest := a.StateDigest()
			beforeInspect, _ := a.Inspect()
			beforeCursor := a.Cursor()
			outs, err := tc.step.do(t, a)
			if errCode(err) != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if err != nil {
				afterInspect, _ := a.Inspect()
				if a.StateDigest() != beforeDigest || !bytes.Equal(afterInspect, beforeInspect) || a.Cursor() != beforeCursor || outs != nil {
					t.Fatalf("failed request changed the state or returned outputs: %s → %s", beforeInspect, afterInspect)
				}
				return
			}
			if tc.extra != nil {
				tc.extra(a)
			}
			if tc.state == nil {
				return
			}
			out, got := stateOf(t, outs)
			if !reflect.DeepEqual(got, *tc.state) {
				t.Fatalf("ferry.state = %+v, want %+v", got, *tc.state)
			}
			if out.T != tc.t || out.Class != tc.class || out.Seq != uint64(len(tc.setup)+2) {
				t.Fatalf("output (t, class, seq) = (%d, %d, %d), want (%d, %d, %d)", out.T, out.Class, out.Seq, tc.t, tc.class, len(tc.setup)+2)
			}
			if c := a.Cursor(); c.Horizon != (dtnproto.Mark{T: tc.t, C: tc.class}) || c.StateDigest != a.StateDigest() {
				t.Fatalf("cursor = %+v", c)
			}
			if a.StateDigest() == beforeDigest {
				t.Fatal("stateDigest did not change")
			}
		})
	}

	if sim.ClassFerryArrive != 1 {
		t.Fatalf("sim.ClassFerryArrive = %d; the arrive output class (1) must follow it", sim.ClassFerryArrive)
	}
}

func TestFerryRejectsWhileSailing(t *testing.T) {
	st := repoStage(t, "stage1")
	codec := codecFor(st)
	a := newFerry(t)
	l1 := raftLetter(t, codec, letterID(1, 1), 1, 3, 0)
	mustDo(t, a, cargoAdd(0, l1))
	mustDo(t, a, depart(0, 2, 3750))
	before := a.StateDigest()
	for _, s := range []step{
		cargoAdd(10, raftLetter(t, codec, letterID(1, 2), 1, 3, 0)),
		cargoRemove(letterID(1, 1)),
		export(10, letterID(1, 1)),
	} {
		outs, err := s.do(t, a)
		if errCode(err) != dtnproto.CodeInvalid || outs != nil {
			t.Errorf("%s while sailing: outs=%v err=%v, want invalid", s.kind, outs, err)
		}
	}
	if a.StateDigest() != before {
		t.Fatal("rejected requests changed the state")
	}
	// 着いた後は積み下ろしできる
	mustDo(t, a, arrive(3750, 2))
	mustDo(t, a, export(3750, letterID(1, 1)))
	mustDo(t, a, cargoRemove(letterID(1, 1)))
}

func TestFerryExportAge(t *testing.T) {
	st := repoStage(t, "stage1")
	codec := codecFor(st)
	a := newFerry(t)
	l := raftLetter(t, codec, letterID(1, 1), 1, 3, 700)
	l2 := raftLetter(t, codec, letterID(1, 2), 1, 3, 1000)
	mustDo(t, a, cargoAdd(1200, l, l2))
	before, beforeCursor := a.StateDigest(), a.Cursor()
	beforeInspect, _ := a.Inspect()

	outs := mustDo(t, a, export(5000, letterID(1, 2), letterID(1, 1)))
	if len(outs) != 2 {
		t.Fatalf("outputs = %+v", outs)
	}
	for i, want := range []struct {
		id  ferry.LetterID
		age int64
	}{{letterID(1, 2), 4000}, {letterID(1, 1), 4300}} {
		o := outs[i]
		if o.Type != ferryactor.OutputBundleExported || o.T != 5000 || o.Class != 8 || o.Seq != 0 {
			t.Fatalf("output %d = %+v", i, o)
		}
		var d struct {
			LetterID ferry.LetterID `json:"letterId"`
			Digest   string         `json:"digest"`
			Size     int            `json:"size"`
		}
		if err := json.Unmarshal(o.Data, &d); err != nil {
			t.Fatal(err)
		}
		if d.LetterID != want.id || d.Digest != dtnproto.Digest(o.Bundle) || d.Size != len(o.Bundle) {
			t.Fatalf("output %d data = %s (bundle %d bytes)", i, o.Data, len(o.Bundle))
		}
		dl, _, age, err := codec.Decode(o.Bundle)
		if err != nil {
			t.Fatal(err)
		}
		if dl.ID != want.id || age != want.age {
			t.Fatalf("output %d: letter %d age %d, want %d age %d", i, dl.ID, age, want.id, want.age)
		}
	}
	afterInspect, _ := a.Inspect()
	if a.StateDigest() != before || a.Cursor() != beforeCursor || !bytes.Equal(afterInspect, beforeInspect) {
		t.Fatal("bundle.export changed the state")
	}
	// 同じ export は同じ bytes（決定的）。積んだ bytes は変わらない（もう一度 Bundle Age を変えられる）
	again := mustDo(t, a, export(5000, letterID(1, 2), letterID(1, 1)))
	if !bytes.Equal(again[1].Bundle, outs[1].Bundle) {
		t.Fatal("export is not deterministic")
	}
	later := mustDo(t, a, export(9000, letterID(1, 1)))
	if _, _, age, _ := codec.Decode(later[0].Bundle); age != 8300 {
		t.Fatalf("age at 9000 = %d, want 8300", age)
	}
	// createdAt より前の t は断る
	if _, err := export(699, letterID(1, 1)).do(t, a); errCode(err) != dtnproto.CodeInvalid {
		t.Fatalf("export before createdAt: %v", err)
	}
	// 次の ferry.state の seq は、export で進まない（init 1・cargo.add 2 → 3）
	out, _ := stateOf(t, mustDo(t, a, cargoRemove(letterID(1, 2))))
	if out.Seq != 3 {
		t.Fatalf("seq after export = %d, want 3", out.Seq)
	}
}

// FakeCodec でも動く（DTN-06 の前の構成。WithCodec）。
func TestFerryWithFakeCodec(t *testing.T) {
	st := repoStage(t, "stage1")
	a := ferryactor.New(repoStages(t), ferryactor.WithCodec(dtnproto.FakeCodec{}))
	if _, err := a.Init(initPayload(st, "f1")); err != nil {
		t.Fatal(err)
	}
	fake := raftLetter(t, dtnproto.FakeCodec{}, 5, 1, 2, 0)
	mustDo(t, a, cargoAdd(0, fake))
	// bpv7 の bytes は FakeCodec では読めない
	if _, err := cargoAdd(0, raftLetter(t, codecFor(st), letterID(1, 1), 1, 2, 0)).do(t, a); errCode(err) != dtnproto.CodeInvalid {
		t.Fatalf("bpv7 bundle with FakeCodec: %v", err)
	}
	outs := mustDo(t, a, export(40, 5))
	if _, _, age, err := (dtnproto.FakeCodec{}).Decode(outs[0].Bundle); err != nil || age != 40 {
		t.Fatalf("age = %d, err = %v", age, err)
	}
}

// Cargo の bytes は、要求の slice と共有しない（呼び出し側が後で書き換えても変わらない）。
func TestFerryCopiesBundles(t *testing.T) {
	st := repoStage(t, "stage1")
	codec := codecFor(st)
	a := newFerry(t)
	l := raftLetter(t, codec, letterID(1, 1), 1, 3, 0)
	payload := mustJSON(t, dtnproto.LettersPayload{T: 0, Letters: []dtnproto.Letter{l}})
	if _, err := a.Handle(dtnproto.KindCargoAdd, payload); err != nil {
		t.Fatal(err)
	}
	for i := range payload {
		payload[i] = 'x'
	}
	outs := mustDo(t, a, export(0, letterID(1, 1)))
	if !bytes.Equal(outs[0].Bundle, l.Bundle) {
		t.Fatal("exported bytes differ from the loaded bytes (age 0)")
	}
}
