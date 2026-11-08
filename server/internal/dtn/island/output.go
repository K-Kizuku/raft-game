package island

import (
	"encoding/json"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// 出力の type（actor-protocol.md §4）。IslandCore の出力の type（islandcore.Out*）と bundle.exported。
const (
	OutputLetterCreated   = islandcore.OutLetterCreated
	OutputLetterDropped   = islandcore.OutLetterDropped
	OutputLetterDelivered = islandcore.OutLetterDelivered
	OutputNodeState       = islandcore.OutNodeState
	OutputLeaderElected   = islandcore.OutLeaderElected
	OutputSteppedDown     = islandcore.OutSteppedDown
	OutputTermChanged     = islandcore.OutTermChanged
	OutputLedgerApplied   = islandcore.OutLedgerApplied
	OutputRequestRouted   = islandcore.OutRequestRouted
	OutputBundleExported  = "bundle.exported"
)

// LetterCreatedData は letter.created の data: sim.LetterCreated（{letter: LetterView}）に digest・size・msgTerm を
// 足したもの。msgTerm は Raft のメッセージ自身の Term（依頼の手紙は 0）。Bundle の bytes は Output.Bundle に入る。
type LetterCreatedData struct {
	sim.LetterCreated
	Digest  string `json:"digest"`
	Size    int    `json:"size"`
	MsgTerm uint64 `json:"msgTerm"`
}

// LetterDroppedData は letter.dropped の data: sim.LetterDropped に rule（R1〜R6）を足したもの。
type LetterDroppedData struct {
	sim.LetterDropped
	Rule string `json:"rule"`
}

// TermChangedData は term.changed の data。
type TermChangedData struct {
	Term uint64 `json:"term"`
}

// RequestRoutedData は request.routed の data。to・letterId は forwarded のときだけ（依頼の手紙の宛先と ID）。
type RequestRoutedData struct {
	ReqID    string         `json:"reqId"`
	Action   string         `json:"action"` // proposed | forwarded | waiting
	To       uint64         `json:"to,omitempty"`
	LetterID ferry.LetterID `json:"letterId,omitempty"`
}

// convert は IslandCore の出力を電文の出力にする（順・t・class・seq はそのまま）。
// letter.created ごとに Bundle を作って港の手紙として持ち、letter.dropped の手紙の Bundle は捨てる。
// Bundle を作れない（16 KiB を超えるなど）のは Actor の中の失敗なので internal（状態は変わった後）。
func (a *Actor) convert(outs []islandcore.Output) ([]dtnproto.Output, error) {
	res := make([]dtnproto.Output, 0, len(outs))
	for _, o := range outs {
		var data any
		var bundle []byte
		switch d := o.Data.(type) {
		case islandcore.LetterCreatedOut:
			b, err := a.encodeLetter(d.Letter)
			if err != nil {
				return nil, err
			}
			digest := dtnproto.Digest(b)
			a.bundles[d.Letter.ID] = held{bundle: b, digest: digest, createdAt: d.Letter.CreatedAt}
			data = LetterCreatedData{
				LetterCreated: sim.LetterCreated{Letter: sim.NewLetterView(d.Letter, d.SenderTerm)},
				Digest:        digest, Size: len(b), MsgTerm: d.MsgTerm,
			}
			bundle = b
		case islandcore.LetterDroppedOut:
			delete(a.bundles, d.LetterID)
			data = LetterDroppedData{LetterDropped: sim.LetterDropped{LetterID: uint64(d.LetterID), Reason: string(d.Reason)}, Rule: d.Rule}
		case islandcore.LetterDeliveredOut:
			data = sim.LetterDelivered{LetterID: uint64(d.LetterID), IslandID: a.id, Accepted: d.Accepted}
		case islandcore.NodeStateOut:
			data = sim.NodeState{ID: a.id, Role: sim.RoleName(d.Role), Term: d.Term, Vote: d.Vote, Lead: d.Lead,
				Commit: d.Commit, Applied: d.Applied, LastIndex: d.LastIndex}
		case islandcore.LeaderElectedOut:
			data = sim.LeaderElected{ID: a.id, Term: d.Term}
		case islandcore.SteppedDownOut:
			data = sim.SteppedDown{ID: a.id, Term: d.Term, Reason: d.Reason}
		case islandcore.TermChangedOut:
			data = TermChangedData{Term: d.Term}
		case islandcore.LedgerAppliedOut:
			data = sim.LedgerApplied{IslandID: a.id, Index: d.Index, ReqID: d.ReqID, Result: d.Result, Reason: d.Reason}
		case islandcore.RequestRoutedOut:
			data = RequestRoutedData{ReqID: d.ReqID, Action: d.Action, To: d.To, LetterID: d.LetterID}
		default:
			return nil, internal("island %d: unknown output %T", a.id, o.Data)
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, internal("island %d: marshal %s: %v", a.id, o.Type, err)
		}
		res = append(res, dtnproto.Output{T: o.T, Class: uint8(o.Class), Seq: o.Seq, Type: o.Type, Data: raw, Bundle: bundle})
	}
	return res, nil
}

// encodeLetter は港に置いた手紙 l の Bundle（Bundle Age 0）を作る。依頼の手紙には、窓口が知っている依頼の
// 台帳のコマンドを入れる（bpv7-profile.md §4。転送先の島が計画を持たずに Propose できるように）。
func (a *Actor) encodeLetter(l ferry.Letter) ([]byte, error) {
	var cmd []byte
	if l.Kind == ferry.LetterRequest {
		r, ok := a.core.Request(l.Req.ReqID)
		if !ok {
			return nil, internal("letter %d: unknown request %s", l.ID, l.Req.ReqID)
		}
		c, err := ledger.Encode(r.Command())
		if err != nil {
			return nil, internal("letter %d: %v", l.ID, err)
		}
		cmd = c
	}
	b, err := a.codec.Encode(l, cmd, 0)
	if err != nil {
		return nil, internal("letter %d: encode: %v", l.ID, err)
	}
	if len(b) > a.lim.MaxBundleBytes {
		return nil, internal("letter %d: bundle of %d bytes exceeds %d", l.ID, len(b), a.lim.MaxBundleBytes)
	}
	return b, nil
}
