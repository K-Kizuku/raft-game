package host_test

import (
	"encoding/json"
	"fmt"
	"slices"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// fakeIsland は Handoff の試験用の島の Actor（DTN-03 の本物の島の代わり）。Raft は動かさず、港・倉庫・配達済みの
// 集合だけを持つ。advance で台本の手紙を作り（letter.created に BPv7 の bytes を付ける）、deliver・unload・
// port.remove・relay.remove・bundle.export を受ける。配達済みの letterId を覚え、2 回目の deliver を invalid で
// 断る（D-4）。港の規則 R1〜R6 はかけない（unload の place: "port" はそのまま港に戻す）。
type fakeIsland struct {
	id      uint64
	ref     dtnproto.ActorRef
	codec   dtnproto.LetterCodec
	script  []ferry.Letter // advance で作る手紙（CreatedAt の順）
	created int

	horizon   dtnproto.Mark
	seq       uint64
	port      []islandItem
	relay     []islandItem
	delivered []ferry.LetterID
}

type islandItem struct {
	id        ferry.LetterID
	createdAt int64
	bundle    []byte
}

func newFakeIsland(id uint64, codec dtnproto.LetterCodec, script []ferry.Letter) *fakeIsland {
	return &fakeIsland{id: id, ref: dtnproto.IslandRef(id), codec: codec, script: script}
}

// raftLetter は島 from の n 通目の Raft の手紙（from → to の heartbeat。term は中身の違いだけに使う）。
func raftLetter(from, n, to uint64, createdAt int64, term uint64) ferry.Letter {
	return ferry.Letter{ID: ferry.LetterID(from*1_000_000_000 + n), Kind: ferry.LetterRaft, From: from, To: to, CreatedAt: createdAt,
		Raft: &pb.Message{Type: pb.MsgHeartbeat.Enum(), From: &from, To: &to, Term: &term}}
}

func (a *fakeIsland) Init(dtnproto.InitPayload) ([]dtnproto.Output, error) {
	return []dtnproto.Output{a.out(0, 0, "node.state", `{"role":"follower"}`)}, nil
}

func (a *fakeIsland) out(t int64, class uint8, typ, data string) dtnproto.Output {
	a.seq++
	return dtnproto.Output{T: t, Class: class, Seq: a.seq, Type: typ, Data: json.RawMessage(data)}
}

func invalidf(format string, args ...any) error {
	return dtnproto.Errorf(dtnproto.CodeInvalid, format, args...)
}

func (a *fakeIsland) Handle(kind dtnproto.Kind, raw json.RawMessage) ([]dtnproto.Output, error) {
	pv, perr := dtnproto.DecodePayload(kind, raw, a.ref, dtnproto.DefaultLimits)
	if perr != nil {
		return nil, perr
	}
	var outs []dtnproto.Output
	switch p := pv.(type) {
	case *dtnproto.AdvancePayload:
		for a.created < len(a.script) && a.script[a.created].CreatedAt <= p.Horizon.T {
			l := a.script[a.created]
			a.created++
			b, err := a.codec.Encode(l, nil, 0)
			if err != nil {
				return nil, err
			}
			a.port = append(a.port, islandItem{id: l.ID, createdAt: l.CreatedAt, bundle: b})
			o := a.out(l.CreatedAt, 3, host.OutputLetterCreated,
				fmt.Sprintf(`{"letterId":%d,"from":%d,"to":%d,"digest":%q,"size":%d}`, l.ID, l.From, l.To, dtnproto.Digest(b), len(b)))
			o.Bundle = b
			outs = append(outs, o)
		}
		a.horizon = p.Horizon
	case *dtnproto.LettersPayload: // deliver
		if kind != dtnproto.KindDeliver {
			return nil, invalidf("%s is not for an island", kind)
		}
		for _, l := range p.Letters {
			if slices.Contains(a.delivered, l.LetterID) {
				return nil, invalidf("deliver: letter %d was already delivered", l.LetterID)
			}
			if err := a.check(l); err != nil {
				return nil, err
			}
		}
		for _, l := range p.Letters {
			a.delivered = append(a.delivered, l.LetterID)
			outs = append(outs, a.out(p.T, 1, host.OutputLetterDelivered, fmt.Sprintf(`{"letterId":%d}`, l.LetterID)))
		}
	case *dtnproto.UnloadPayload:
		for _, l := range p.Letters {
			if a.find(a.port, l.LetterID) >= 0 || a.find(a.relay, l.LetterID) >= 0 {
				return nil, invalidf("unload: letter %d is already here", l.LetterID)
			}
			if err := a.check(l.Letter); err != nil {
				return nil, err
			}
		}
		for _, l := range p.Letters {
			it := islandItem{id: l.LetterID, createdAt: l.CreatedAt, bundle: slices.Clone(l.Bundle)}
			if l.Place == dtnproto.PlacePort {
				a.port = append(a.port, it)
			} else {
				a.relay = append(a.relay, it)
			}
		}
		outs = append(outs, a.out(p.T, 8, "island.unloaded", fmt.Sprintf(`{"letters":%d}`, len(p.Letters))))
	case *dtnproto.LetterIDsPayload:
		list := &a.port
		if kind == dtnproto.KindRelayRemove {
			list = &a.relay
		} else if kind != dtnproto.KindPortRemove {
			return nil, invalidf("%s is not for an island", kind)
		}
		for _, id := range p.LetterIDs {
			if a.find(*list, id) < 0 {
				return nil, invalidf("%s: letter %d is not here", kind, id)
			}
		}
		*list = slices.DeleteFunc(*list, func(it islandItem) bool { return slices.Contains(p.LetterIDs, it.id) })
	case *dtnproto.ExportPayload:
		for _, id := range p.LetterIDs {
			it, ok := a.lookup(id)
			if !ok {
				return nil, invalidf("bundle.export: letter %d is not here", id)
			}
			b, err := a.codec.WithAge(it.bundle, p.T-it.createdAt)
			if err != nil {
				return nil, invalidf("bundle.export: %v", err)
			}
			outs = append(outs, dtnproto.Output{T: p.T, Class: 8, Type: host.OutputBundleExported,
				Data: json.RawMessage(fmt.Sprintf(`{"letterId":%d,"digest":%q,"size":%d}`, id, dtnproto.Digest(b), len(b))), Bundle: b})
		}
	default:
		return nil, invalidf("%s is not for an island", kind)
	}
	return outs, nil
}

func (a *fakeIsland) check(l dtnproto.Letter) error {
	if dtnproto.Digest(l.Bundle) != l.Digest {
		return invalidf("letter %d: digest does not match", l.LetterID)
	}
	dl, _, _, err := a.codec.Decode(l.Bundle)
	if err != nil {
		return invalidf("letter %d: %v", l.LetterID, err)
	}
	if dl.ID != l.LetterID {
		return invalidf("letter %d: the bundle is letter %d", l.LetterID, dl.ID)
	}
	return nil
}

func (a *fakeIsland) find(list []islandItem, id ferry.LetterID) int {
	return slices.IndexFunc(list, func(it islandItem) bool { return it.id == id })
}

func (a *fakeIsland) lookup(id ferry.LetterID) (islandItem, bool) {
	if i := a.find(a.port, id); i >= 0 {
		return a.port[i], true
	}
	if i := a.find(a.relay, id); i >= 0 {
		return a.relay[i], true
	}
	return islandItem{}, false
}

// islandView は fakeIsland の inspect の data。
type islandView struct {
	Port      []ferry.LetterID `json:"port"`
	Relay     []ferry.LetterID `json:"relay"`
	Delivered []ferry.LetterID `json:"delivered"`
}

func (a *fakeIsland) view() islandView {
	v := islandView{Port: []ferry.LetterID{}, Relay: []ferry.LetterID{}, Delivered: slices.Clone(a.delivered)}
	for _, it := range a.port {
		v.Port = append(v.Port, it.id)
	}
	for _, it := range a.relay {
		v.Relay = append(v.Relay, it.id)
	}
	if v.Delivered == nil {
		v.Delivered = []ferry.LetterID{}
	}
	return v
}

func (a *fakeIsland) Inspect() (json.RawMessage, error) { return json.Marshal(a.view()) }

func (a *fakeIsland) Cursor() dtnproto.Cursor {
	b, _ := json.Marshal(a.view())
	return dtnproto.Cursor{Horizon: a.horizon, StateDigest: dtnproto.Digest(b)}
}
