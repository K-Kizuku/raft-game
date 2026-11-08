package island_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/island"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

var _ runtime.Actor = (*island.Actor)(nil)

// repoRoot はリポジトリの根（このパッケージから 4 つ上）。
const repoRoot = "../../../.."

var (
	stagesOnce sync.Once
	stagesList []*stage.Stage
	stagesErr  error
)

// repoStages はリポジトリの data/ のステージを読む。data/islands.json がなければ試験を飛ばす。
func repoStages(t testing.TB) []*stage.Stage {
	t.Helper()
	if _, err := os.Stat(repoRoot + "/data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない")
	}
	stagesOnce.Do(func() {
		stagesList, stagesErr = stage.LoadDir(os.DirFS(repoRoot), "data/islands.json", "data/stages")
	})
	if stagesErr != nil {
		t.Fatal(stagesErr)
	}
	return stagesList
}

func repoStage(t testing.TB, id string) *stage.Stage {
	t.Helper()
	for _, st := range repoStages(t) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("ステージ %s がない", id)
	return nil
}

func simConfig(t testing.TB, st *stage.Stage) sim.Config {
	t.Helper()
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// planOf は seed の依頼の計画（参照モデルと同じ。対象のステージに注文ラッシュはない）。
func planOf(t testing.TB, st *stage.Stage, seed uint64) []game.Request {
	t.Helper()
	cfg := simConfig(t, st)
	plan, err := game.PlanRequests(cfg.Requests.Clone(), cfg.Islands, int64(cfg.DurationMs), cfg.Resources, sim.Sub(seed, sim.StreamRequests))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// ownPlan は plan のうち依頼元が id のもの（init の plan）。
func ownPlan(plan []game.Request, id uint64) []dtnproto.PlannedRequest {
	var out []dtnproto.PlannedRequest
	for _, r := range plan {
		if r.Origin == id {
			out = append(out, dtnproto.PlannedRequest{ID: r.ID, Origin: r.Origin, Kind: r.Kind, Resource: r.Resource,
				Critical: r.Critical, CreatedAt: r.CreatedAt, Deadline: r.Deadline})
		}
	}
	return out
}

func initPayload(t testing.TB, st *stage.Stage, seed, id uint64) dtnproto.InitPayload {
	t.Helper()
	return dtnproto.InitPayload{Stage: st.ID, StageHash: st.Hash, Seed: seed, Actor: dtnproto.IslandRef(id), Plan: ownPlan(planOf(t, st, seed), id)}
}

// newIsland は st・seed の島 id の Actor を作って init する。init の出力も返す。
func newIsland(t testing.TB, st *stage.Stage, seed, id uint64, opts ...island.Option) (*island.Actor, []dtnproto.Output) {
	t.Helper()
	a := island.New(repoStages(t), opts...)
	outs, err := a.Init(initPayload(t, st, seed, id))
	if err != nil {
		t.Fatalf("init island %d: %v", id, err)
	}
	return a, outs
}

func mustJSON(t testing.TB, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// handle は a に kind の要求を送る。
func handle(t testing.TB, a *island.Actor, kind dtnproto.Kind, payload any) ([]dtnproto.Output, error) {
	t.Helper()
	return a.Handle(kind, mustJSON(t, payload))
}

// mustHandle は handle し、成功しなければ試験を止める。
func mustHandle(t testing.TB, a *island.Actor, kind dtnproto.Kind, payload any) []dtnproto.Output {
	t.Helper()
	outs, err := handle(t, a, kind, payload)
	if err != nil {
		t.Fatalf("%s %s: %v", kind, mustJSON(t, payload), err)
	}
	return outs
}

func advance(t testing.TB, a *island.Actor, h islandcore.Mark) []dtnproto.Output {
	t.Helper()
	return mustHandle(t, a, dtnproto.KindAdvance, dtnproto.AdvancePayload{Horizon: dtnproto.Mark{T: h.T, C: uint8(h.Class)}})
}

// domainEvents は島の Actor の出力を、参照モデルのドメインイベントの列にする（simtrace に渡す）。
// term.changed・request.routed・bundle.exported はドメインイベントにならない（Coordinator が使う）。
// 足した欄（digest・size・msgTerm・rule）は、sim の型に読むときに落ちる。
func domainEvents(t testing.TB, outs []dtnproto.Output) []sim.DomainEvent {
	t.Helper()
	var evs []sim.DomainEvent
	for _, o := range outs {
		var d sim.EventData
		var err error
		switch o.Type {
		case island.OutputLetterCreated:
			d, err = decodeAs[sim.LetterCreated](o.Data)
		case island.OutputLetterDropped:
			d, err = decodeAs[sim.LetterDropped](o.Data)
		case island.OutputLetterDelivered:
			d, err = decodeAs[sim.LetterDelivered](o.Data)
		case island.OutputNodeState:
			d, err = decodeAs[sim.NodeState](o.Data)
		case island.OutputLeaderElected:
			d, err = decodeAs[sim.LeaderElected](o.Data)
		case island.OutputSteppedDown:
			d, err = decodeAs[sim.SteppedDown](o.Data)
		case island.OutputLedgerApplied:
			d, err = decodeAs[sim.LedgerApplied](o.Data)
		case island.OutputTermChanged, island.OutputRequestRouted, island.OutputBundleExported:
			continue
		default:
			t.Fatalf("unknown output type %q", o.Type)
		}
		if err != nil {
			t.Fatalf("%s: %v", o.Type, err)
		}
		evs = append(evs, sim.DomainEvent{Seq: uint64(len(evs) + 1), T: sim.SimTime(o.T), Type: d.EventType(), Data: d})
	}
	return evs
}

func decodeAs[T sim.EventData](raw json.RawMessage) (sim.EventData, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}
