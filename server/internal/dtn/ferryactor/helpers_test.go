package ferryactor_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/ferryactor"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

var _ runtime.Actor = (*ferryactor.Actor)(nil)

// repoRoot はリポジトリの根（このパッケージから 4 つ上）。
const repoRoot = "../../../.."

var (
	stagesOnce sync.Once
	stagesList []*stage.Stage
	stagesErr  error
)

// repoStages はリポジトリの data/ のステージを読む。data/islands.json がなければ試験を飛ばす。
func repoStages(t *testing.T) []*stage.Stage {
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

func repoStage(t *testing.T, id string) *stage.Stage {
	t.Helper()
	for _, st := range repoStages(t) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("ステージ %s がない", id)
	return nil
}

func initPayload(st *stage.Stage, fid string) dtnproto.InitPayload {
	return dtnproto.InitPayload{Stage: st.ID, StageHash: st.Hash, Seed: 1, Actor: dtnproto.FerryRef(ferry.FerryID(fid)), Plan: nil}
}

// newFerry は stage1 の f1（home 1・capacity 6・dockMs 1500）の Actor を作って init する。
func newFerry(t *testing.T) *ferryactor.Actor {
	t.Helper()
	st := repoStage(t, "stage1")
	a := ferryactor.New(repoStages(t))
	outs, err := a.Init(initPayload(st, "f1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 || outs[0].T != 0 || outs[0].Class != 0 || outs[0].Seq != 1 || outs[0].Type != ferryactor.OutputFerryState {
		t.Fatalf("init outputs = %+v", outs)
	}
	return a
}

// letterID は島 island の n 通目の手紙の ID（simulation-time.md §5.1）。
func letterID(island, n uint64) ferry.LetterID { return ferry.LetterID(island*1_000_000_000 + n) }

// codecFor は st の bpv7 の Codec。
func codecFor(st *stage.Stage) bpv7.LetterCodec {
	return bpv7.LetterCodec{LifetimeMs: uint64(st.DurationMs + 60_000)}
}

// raftLetter は Raft の手紙 id（from → to、createdAt）の電文の手紙を作る。
func raftLetter(t *testing.T, codec dtnproto.LetterCodec, id ferry.LetterID, from, to uint64, createdAt int64) dtnproto.Letter {
	t.Helper()
	l := ferry.Letter{ID: id, Kind: ferry.LetterRaft, From: from, To: to, CreatedAt: createdAt,
		Raft: &pb.Message{Type: pb.MsgHeartbeat.Enum(), From: &from, To: &to}}
	return encodeLetter(t, codec, l)
}

func encodeLetter(t *testing.T, codec dtnproto.LetterCodec, l ferry.Letter) dtnproto.Letter {
	t.Helper()
	b, err := codec.Encode(l, nil, 0)
	if err != nil {
		t.Fatalf("encode letter %d: %v", l.ID, err)
	}
	return dtnproto.Letter{LetterID: l.ID, CreatedAt: l.CreatedAt, Digest: dtnproto.Digest(b), Bundle: b}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// step は Actor への要求 1 つ。
type step struct {
	kind    dtnproto.Kind
	payload any
}

func arrive(t int64, island uint64) step {
	return step{dtnproto.KindArrive, dtnproto.ArrivePayload{T: t, Island: island}}
}

func depart(t int64, to uint64, arriveAt int64) step {
	return step{dtnproto.KindDepart, dtnproto.DepartPayload{T: t, To: to, ArriveAt: arriveAt}}
}

func cargoAdd(t int64, letters ...dtnproto.Letter) step {
	if letters == nil {
		letters = []dtnproto.Letter{}
	}
	return step{dtnproto.KindCargoAdd, dtnproto.LettersPayload{T: t, Letters: letters}}
}

func cargoRemove(ids ...ferry.LetterID) step {
	if ids == nil {
		ids = []ferry.LetterID{}
	}
	return step{dtnproto.KindCargoRemove, dtnproto.LetterIDsPayload{LetterIDs: ids}}
}

func export(t int64, ids ...ferry.LetterID) step {
	if ids == nil {
		ids = []ferry.LetterID{}
	}
	return step{dtnproto.KindBundleExport, dtnproto.ExportPayload{T: t, LetterIDs: ids}}
}

func (s step) do(t *testing.T, a *ferryactor.Actor) ([]dtnproto.Output, error) {
	t.Helper()
	return a.Handle(s.kind, mustJSON(t, s.payload))
}

// mustDo は s を処理し、成功しなければ試験を止める。
func mustDo(t *testing.T, a *ferryactor.Actor, s step) []dtnproto.Output {
	t.Helper()
	outs, err := s.do(t, a)
	if err != nil {
		t.Fatalf("%s %s: %v", s.kind, mustJSON(t, s.payload), err)
	}
	return outs
}

// stateOf は outputs の ferry.state（ちょうど 1 つ）を読む。
func stateOf(t *testing.T, outs []dtnproto.Output) (dtnproto.Output, ferryactor.StateData) {
	t.Helper()
	if len(outs) != 1 || outs[0].Type != ferryactor.OutputFerryState {
		t.Fatalf("outputs = %+v, want one ferry.state", outs)
	}
	var d ferryactor.StateData
	if err := json.Unmarshal(outs[0].Data, &d); err != nil {
		t.Fatal(err)
	}
	return outs[0], d
}

// errCode は err のコード（nil なら ""）。
func errCode(err error) dtnproto.Code {
	if err == nil {
		return ""
	}
	var pe *dtnproto.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return "non-proto: " + dtnproto.Code(err.Error())
}

// request は Runtime に送る電文を作る。
func request(t *testing.T, actor dtnproto.ActorRef, seq uint64, kind dtnproto.Kind, payload any, replay bool) []byte {
	t.Helper()
	req := dtnproto.Request{V: 1, SessionID: "s_ferry", Actor: actor, Epoch: 1, CoordEpoch: 1, EventSeq: seq,
		Kind: kind, Payload: mustJSON(t, payload), Replay: replay}
	return mustJSON(t, req)
}
