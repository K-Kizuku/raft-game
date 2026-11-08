package island_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/bot"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/island"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/simtrace"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// 全島の Actor を、参照モデルで遊んだラウンドの船の動き（積む・下ろす・配達）に合わせて動かす試験用の小さな
// Coordinator。手紙の bytes は送り元の Actor の letter.created と bundle.export から取り、依頼の発生と結果は
// requests.note で依頼元でない島に知らせる（DTN-05 の Coordinator の手順の最小の写し）。

// action は参照モデルのイベントの列から取り出した、島への要求 1 つ。
type action struct {
	t      int64
	kind   dtnproto.Kind // deliver・unload・port.remove・relay.remove
	island uint64
	ids    []ferry.LetterID
	places []string // unload の place（ids と同じ順）
}

func (x action) mark() islandcore.Mark {
	if x.kind == dtnproto.KindDeliver {
		return islandcore.Mark{T: x.t, Class: islandcore.ClassArrive}
	}
	return islandcore.Mark{T: x.t, Class: islandcore.ClassCommand}
}

// actionsOf は参照モデルのイベントの列 evs から、島への要求の列を作る。
func actionsOf(t testing.TB, evs []sim.DomainEvent) []action {
	t.Helper()
	type loc struct {
		typ    string
		island uint64
	}
	where := map[uint64]loc{} // 手紙の今の位置（検索だけ）
	var out []action
	arriving := -1 // 今の到着の deliver の添字（-1 = なし）
	for _, ev := range evs {
		tt := int64(ev.T)
		switch d := ev.Data.(type) {
		case sim.LetterCreated:
			where[d.Letter.ID] = loc{"port", d.Letter.From}
		case sim.FerryArrived:
			out = append(out, action{t: tt, kind: dtnproto.KindDeliver, island: d.IslandID})
			arriving = len(out) - 1
		case sim.LetterDelivered:
			if arriving < 0 || out[arriving].island != d.IslandID || out[arriving].t != tt {
				t.Fatalf("letter.delivered %d at %d without an arrival", d.LetterID, tt)
			}
			out[arriving].ids = append(out[arriving].ids, ferry.LetterID(d.LetterID))
		case sim.LetterMoved:
			arriving = -1
			id := ferry.LetterID(d.LetterID)
			switch d.Loc.Type {
			case "ferry":
				from := where[d.LetterID]
				kind := dtnproto.KindPortRemove
				if from.typ == "relay" {
					kind = dtnproto.KindRelayRemove
				} else if from.typ != "port" {
					t.Fatalf("letter %d loaded from %+v", d.LetterID, from)
				}
				out = append(out, action{t: tt, kind: kind, island: from.island, ids: []ferry.LetterID{id}})
			case "port", "relay":
				n := len(out)
				if n > 0 && out[n-1].kind == dtnproto.KindUnload && out[n-1].t == tt && out[n-1].island == d.Loc.IslandID &&
					out[n-1].ids[len(out[n-1].ids)-1] < id {
					out[n-1].ids = append(out[n-1].ids, id)
					out[n-1].places = append(out[n-1].places, d.Loc.Type)
				} else {
					out = append(out, action{t: tt, kind: dtnproto.KindUnload, island: d.Loc.IslandID,
						ids: []ferry.LetterID{id}, places: []string{d.Loc.Type}})
				}
			default:
				t.Fatalf("letter %d moved to %+v", d.LetterID, d.Loc)
			}
			where[d.LetterID] = loc{d.Loc.Type, d.Loc.IslandID}
		}
	}
	return slices.DeleteFunc(out, func(x action) bool { return x.kind == dtnproto.KindDeliver && len(x.ids) == 0 })
}

// letterRec は試験の Coordinator が持つ、手紙の今の bytes（作ったときか、最後に積んだときのもの）。
type letterRec struct {
	createdAt int64
	digest    string
	bundle    []byte
}

type harness struct {
	t       testing.TB
	actors  []*island.Actor // 添字 = ID - 1
	origin  map[string]uint64
	letters map[ferry.LetterID]letterRec
	created map[string]bool // created を知らせた依頼
	solved  map[string]bool // resolved を知らせた依頼
	outs    []dtnproto.Output
}

func newHarness(t testing.TB, st *stage.Stage, seed uint64) *harness {
	t.Helper()
	cfg := simConfig(t, st)
	h := &harness{t: t, origin: map[string]uint64{}, letters: map[ferry.LetterID]letterRec{},
		created: map[string]bool{}, solved: map[string]bool{}}
	for _, r := range planOf(t, st, seed) {
		h.origin[r.ID] = r.Origin
	}
	for id := uint64(1); id <= uint64(cfg.Islands); id++ {
		a, outs := newIsland(t, st, seed, id)
		h.actors = append(h.actors, a)
		h.take(outs)
	}
	return h
}

// take は島の出力を覚え、手紙の bytes を写し、知らせるべき requests.note を送る。
func (h *harness) take(outs []dtnproto.Output) {
	h.t.Helper()
	h.outs = append(h.outs, outs...)
	var created, resolved []string
	for _, o := range outs {
		switch o.Type {
		case island.OutputLetterCreated:
			var d island.LetterCreatedData
			if err := json.Unmarshal(o.Data, &d); err != nil {
				h.t.Fatal(err)
			}
			if dtnproto.Digest(o.Bundle) != d.Digest || len(o.Bundle) != d.Size {
				h.t.Fatalf("letter %d: digest/size do not match the bundle", d.Letter.ID)
			}
			h.letters[ferry.LetterID(d.Letter.ID)] = letterRec{createdAt: int64(d.Letter.CreatedAt), digest: d.Digest, bundle: o.Bundle}
		case island.OutputRequestRouted:
			var d island.RequestRoutedData
			if err := json.Unmarshal(o.Data, &d); err != nil {
				h.t.Fatal(err)
			}
			if !h.created[d.ReqID] {
				h.created[d.ReqID] = true
				created = append(created, d.ReqID)
			}
		case island.OutputLedgerApplied:
			var d sim.LedgerApplied
			if err := json.Unmarshal(o.Data, &d); err != nil {
				h.t.Fatal(err)
			}
			if d.Result != ledger.StatusDuplicate && h.origin[d.ReqID] == d.IslandID && !h.solved[d.ReqID] {
				h.solved[d.ReqID] = true
				resolved = append(resolved, d.ReqID)
			}
		}
	}
	if len(created)+len(resolved) == 0 {
		return
	}
	for _, a := range h.actors {
		mustHandle(h.t, a, dtnproto.KindRequestsNote, dtnproto.RequestsNotePayload{
			T: outs[len(outs)-1].T, Created: nonNil(created), Resolved: nonNil(resolved)})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// advanceAll は全島を target まで進める。途中で依頼の結果が出たら、ほかの島がそれより後のイベントを処理する前に
// requests.note で知らせられるよう、島のイベントの印ごとに区切って進める。
func (h *harness) advanceAll(target islandcore.Mark) {
	h.t.Helper()
	for {
		var next islandcore.Mark
		found := false
		for _, a := range h.actors {
			if ev, ok := a.Core().Peek(); ok && (!found || ev.Mark().Less(next)) {
				next, found = ev.Mark(), true
			}
		}
		if !found || !next.Less(target) {
			break
		}
		step := islandcore.Mark{T: next.T, Class: next.Class + 1}
		for _, a := range h.actors {
			h.take(advance(h.t, a, step))
		}
	}
	for _, a := range h.actors {
		if a.Core().Horizon().Less(target) {
			h.take(advance(h.t, a, target))
		}
	}
}

func (h *harness) letter(id ferry.LetterID) dtnproto.Letter {
	h.t.Helper()
	r, ok := h.letters[id]
	if !ok {
		h.t.Fatalf("letter %d has no bytes", id)
	}
	return dtnproto.Letter{LetterID: id, CreatedAt: r.createdAt, Digest: r.digest, Bundle: r.bundle}
}

func (h *harness) do(x action) {
	h.t.Helper()
	h.advanceAll(x.mark())
	a := h.actors[x.island-1]
	switch x.kind {
	case dtnproto.KindDeliver:
		ls := make([]dtnproto.Letter, len(x.ids))
		for i, id := range x.ids {
			ls[i] = h.letter(id)
		}
		h.take(mustHandle(h.t, a, dtnproto.KindDeliver, dtnproto.LettersPayload{T: x.t, Letters: ls}))
	case dtnproto.KindUnload:
		ls := make([]dtnproto.UnloadLetter, len(x.ids))
		for i, id := range x.ids {
			ls[i] = dtnproto.UnloadLetter{Letter: h.letter(id), Place: x.places[i]}
		}
		h.take(mustHandle(h.t, a, dtnproto.KindUnload, dtnproto.UnloadPayload{T: x.t, Letters: ls}))
	case dtnproto.KindPortRemove, dtnproto.KindRelayRemove:
		// 送り出す前に Bundle Age を更新した bytes を受け取る（bpv7-profile.md §3）。
		for _, o := range mustHandle(h.t, a, dtnproto.KindBundleExport, dtnproto.ExportPayload{T: x.t, LetterIDs: x.ids}) {
			var d island.ExportedData
			if err := json.Unmarshal(o.Data, &d); err != nil {
				h.t.Fatal(err)
			}
			r := h.letters[d.LetterID]
			r.bundle, r.digest = o.Bundle, d.Digest
			h.letters[d.LetterID] = r
		}
		h.take(mustHandle(h.t, a, x.kind, dtnproto.LetterIDsPayload{LetterIDs: x.ids}))
	}
}

// playReference は参照モデルでボットに 1 ラウンド遊ばせ、イベントの列を返す。
func playReference(t testing.TB, st *stage.Stage, botName string, seed uint64) []sim.DomainEvent {
	t.Helper()
	e, err := sim.New(simConfig(t, st), seed, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bot.New(botName, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.Play(e, b); err != nil {
		t.Fatal(err)
	}
	return e.DrainEvents()
}

// refEnd は参照モデルのラウンドの終わり（phase.changed{ended} の時刻の ClassGameTimer。demo の ★2 では
// durationMs より前に終わる）。
func refEnd(evs []sim.DomainEvent, cfg sim.Config) islandcore.Mark {
	for _, ev := range evs {
		if pc, ok := ev.Data.(sim.PhaseChanged); ok && pc.Phase == sim.PhaseEnded {
			return islandcore.Mark{T: int64(ev.T), Class: 7}
		}
	}
	return endMark(cfg)
}

// TestIslandsMatchReferenceWithBot は、ボットが船を動かしたラウンドで、全島の Actor の列が参照モデルの
// island:<id> の列と一致することを確かめる。手紙は bpv7 の bytes で運び、依頼の手紙は台帳のコマンドを運ぶ
// （計画は自島の依頼だけ）。依頼の発生と結果は requests.note で知らせる。
func TestIslandsMatchReferenceWithBot(t *testing.T) {
	seeds := uint64(4)
	if testing.Short() {
		seeds = 1
	}
	for _, sid := range referenceStages {
		for _, botName := range []string{"greedy", "steady"} {
			t.Run(sid+"/"+botName, func(t *testing.T) {
				t.Parallel()
				st := repoStage(t, sid)
				cfg := simConfig(t, st)
				for seed := uint64(1); seed <= seeds; seed++ {
					evs := playReference(t, st, botName, seed)
					ref := simtrace.Normalize(evs)
					h := newHarness(t, st, seed)
					acts := actionsOf(t, evs)
					for _, x := range acts {
						h.do(x)
					}
					h.advanceAll(refEnd(evs, cfg))
					got := simtrace.Normalize(domainEvents(t, h.outs))
					delivered, forwarded := 0, 0
					for _, x := range acts {
						if x.kind == dtnproto.KindDeliver {
							delivered += len(x.ids)
						}
					}
					for _, o := range h.outs {
						if o.Type == island.OutputRequestRouted && string(o.Data) != "" {
							var d island.RequestRoutedData
							_ = json.Unmarshal(o.Data, &d)
							if d.Action == "forwarded" && h.origin[d.ReqID] != uint64(0) {
								forwarded++
							}
						}
					}
					if delivered == 0 {
						t.Fatalf("seed %d: the bot delivered no letters", seed)
					}
					for id := uint64(1); id <= uint64(cfg.Islands); id++ {
						name := fmt.Sprintf("island:%d", id)
						if d := compareStream(streamOf(ref, name), streamOf(got, name)); d != "" {
							t.Fatalf("seed %d %s (%d actions, %d delivered): %s", seed, name, len(acts), delivered, d)
						}
					}
					t.Logf("seed %d: %d actions, %d letters delivered, %d request letters forwarded", seed, len(acts), delivered, forwarded)
				}
			})
		}
	}
}
