// Package simconv は、sim の型と protocol の型を変換する（DesignDoc §4.3、T-301）。
//
// sim は自分の型のまま。protocol は同じ JSON の形の型を別に持ち、このパッケージが変換する。
// 両者の JSON が一致することはテストで確かめる。sim は protocol・simconv を import しない。
package simconv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

var (
	// ErrNotEngineCommand は、電文の type が Engine の受けるコマンドでないこと。
	ErrNotEngineCommand = errors.New("simconv: not an engine command")
	// ErrBadPayload は、コマンドの payload の形が不正なこと（呼び出し側は ack の bad_request にする）。
	ErrBadPayload = errors.New("simconv: bad payload")
)

// DecodeEngineCommand は、Engine が受けるコマンドの電文を sim.Command にする。
//
// 対象は cmd.game.start・cmd.ferry.load・cmd.ferry.unload・cmd.relay.load・cmd.ferry.autoload・cmd.ferry.depart・demo.trigger。
// それ以外の typ は ErrNotEngineCommand。payload の形が不正なら ErrBadPayload（demo.trigger の event は "storm" だけ）。
// payload が空・null なら {} として扱い、知らないキーは無視する（§8）。
// 島の範囲・手紙の重複・船があるかなどは検査しない（Engine.Apply が §4.1 のコードで返す）。
func DecodeEngineCommand(typ protocol.MessageType, payload json.RawMessage) (sim.Command, error) {
	switch typ {
	case protocol.MessageTypeCmdGameStart:
		if _, err := decodeObject(typ, payload); err != nil {
			return sim.Command{}, err
		}
		return sim.Command{Type: sim.CmdGameStart}, nil

	case protocol.MessageTypeCmdFerryLoad, protocol.MessageTypeCmdFerryUnload, protocol.MessageTypeCmdRelayLoad:
		m, err := decodeObject(typ, payload)
		if err != nil {
			return sim.Command{}, err
		}
		var p protocol.FerryLettersPayload
		if err := field(typ, m, "ferryId", &p.FerryID); err != nil {
			return sim.Command{}, err
		}
		if err := field(typ, m, "letterIds", &p.LetterIDs); err != nil {
			return sim.Command{}, err
		}
		switch {
		case p.FerryID == "":
			return sim.Command{}, fmt.Errorf("%w: %s: ferryId is empty", ErrBadPayload, typ)
		case len(p.LetterIDs) == 0:
			return sim.Command{}, fmt.Errorf("%w: %s: letterIds is empty", ErrBadPayload, typ)
		}
		t := sim.CmdFerryLoad
		switch typ {
		case protocol.MessageTypeCmdFerryUnload:
			t = sim.CmdFerryUnload
		case protocol.MessageTypeCmdRelayLoad:
			t = sim.CmdRelayLoad
		}
		return sim.Command{Type: t, FerryID: p.FerryID, LetterIDs: p.LetterIDs}, nil

	case protocol.MessageTypeCmdFerryAutoload, protocol.MessageTypeCmdFerryDepart:
		m, err := decodeObject(typ, payload)
		if err != nil {
			return sim.Command{}, err
		}
		var p protocol.FerryToPayload
		if err := field(typ, m, "ferryId", &p.FerryID); err != nil {
			return sim.Command{}, err
		}
		if err := field(typ, m, "to", &p.To); err != nil {
			return sim.Command{}, err
		}
		switch {
		case p.FerryID == "":
			return sim.Command{}, fmt.Errorf("%w: %s: ferryId is empty", ErrBadPayload, typ)
		case p.To == 0:
			return sim.Command{}, fmt.Errorf("%w: %s: to is missing or 0", ErrBadPayload, typ)
		}
		t := sim.CmdFerryAutoload
		if typ == protocol.MessageTypeCmdFerryDepart {
			t = sim.CmdFerryDepart
		}
		return sim.Command{Type: t, FerryID: p.FerryID, To: p.To}, nil

	case protocol.MessageTypeDemoTrigger:
		m, err := decodeObject(typ, payload)
		if err != nil {
			return sim.Command{}, err
		}
		var p protocol.DemoTriggerPayload
		if err := field(typ, m, "event", &p.Event); err != nil {
			return sim.Command{}, err
		}
		if p.Event != sim.DemoEventStorm {
			return sim.Command{}, fmt.Errorf("%w: %s: event must be %q", ErrBadPayload, typ, sim.DemoEventStorm)
		}
		return sim.Command{Type: sim.CmdDemoTrigger, Event: p.Event}, nil
	}
	return sim.Command{}, fmt.Errorf("%w: %q", ErrNotEngineCommand, typ)
}

// EncodeEngineCommand は、sim.Command を電文の type と payload にする（入力ログ・ボット・テスト用）。
// DecodeEngineCommand の逆。対象は DecodeEngineCommand と同じ（demo.trigger を含む）。
// Engine の受けるコマンドでなければ ErrNotEngineCommand。
func EncodeEngineCommand(c sim.Command) (protocol.ClientCommand, error) {
	var (
		typ     protocol.MessageType
		payload any
	)
	switch c.Type {
	case sim.CmdGameStart:
		typ, payload = protocol.MessageTypeCmdGameStart, struct{}{}
	case sim.CmdFerryLoad, sim.CmdFerryUnload, sim.CmdRelayLoad:
		typ = protocol.MessageTypeCmdFerryLoad
		switch c.Type {
		case sim.CmdFerryUnload:
			typ = protocol.MessageTypeCmdFerryUnload
		case sim.CmdRelayLoad:
			typ = protocol.MessageTypeCmdRelayLoad
		}
		ids := c.LetterIDs
		if ids == nil {
			ids = []uint64{}
		}
		payload = protocol.FerryLettersPayload{FerryID: c.FerryID, LetterIDs: ids}
	case sim.CmdFerryAutoload, sim.CmdFerryDepart:
		typ = protocol.MessageTypeCmdFerryAutoload
		if c.Type == sim.CmdFerryDepart {
			typ = protocol.MessageTypeCmdFerryDepart
		}
		payload = protocol.FerryToPayload{FerryID: c.FerryID, To: c.To}
	case sim.CmdDemoTrigger:
		typ, payload = protocol.MessageTypeDemoTrigger, protocol.DemoTriggerPayload{Event: c.Event}
	default:
		return protocol.ClientCommand{}, fmt.Errorf("%w: %q", ErrNotEngineCommand, c.Type)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return protocol.ClientCommand{}, fmt.Errorf("simconv: encode %s: %w", typ, err)
	}
	return protocol.ClientCommand{Type: typ, Payload: b}, nil
}

// replayIgnored は、入力ログにあってもリプレイでは読み飛ばすコマンド（DesignDoc §10.1。Engine の結果に影響しない）。
var replayIgnored = []protocol.MessageType{
	protocol.MessageTypeCmdGamePause,
	protocol.MessageTypeCmdGameSpeed,
	protocol.MessageTypeCmdGameSkip,
	protocol.MessageTypeCmdSync,
}

// InputEntriesFromLog は、入力ログのコマンドを sim.InputEntry の列にする（T-302 が sim.ReplayLog を作るのに使う）。
//
// cmd.game.pause・cmd.game.speed・cmd.game.skip・cmd.sync は読み飛ばす（demo.trigger は Engine のコマンドとして読む）。それ以外で Engine のコマンドに
// できないものがあれば、そこで止めて位置を含むエラーを返す（黙って読み飛ばさない）。
// 時刻の順・t の範囲は検査しない（sim.Replay が検査する）。結果は空でも nil でない。
func InputEntriesFromLog(cmds []protocol.InputCommand) ([]sim.InputEntry, error) {
	out := make([]sim.InputEntry, 0, len(cmds))
	for i, c := range cmds {
		if slices.Contains(replayIgnored, c.Cmd.Type) {
			continue
		}
		cmd, err := DecodeEngineCommand(c.Cmd.Type, c.Cmd.Payload)
		if err != nil {
			return nil, fmt.Errorf("simconv: input log command %d (t=%d, %s): %w", i, c.T, c.Cmd.Type, err)
		}
		out = append(out, sim.InputEntry{T: sim.SimTime(c.T), Cmd: cmd})
	}
	return out, nil
}

// AckFromSim は sim.Ack を ack の payload にする。
func AckFromSim(cmdID string, a sim.Ack) protocol.Ack {
	return protocol.Ack{
		CmdID:   cmdID,
		OK:      a.OK,
		T:       int64(a.T),
		Code:    protocol.AckCode(a.Code),
		Message: a.Message,
	}
}

// LetterFromSim は sim.LetterView を protocol.Letter にする。入力とスライス・ポインタを共有しない。
func LetterFromSim(v sim.LetterView) protocol.Letter {
	l := protocol.Letter{
		ID:        v.ID,
		Kind:      protocol.LetterKind(v.Kind),
		From:      v.From,
		To:        v.To,
		CreatedAt: int64(v.CreatedAt),
		Loc:       locFromSim(v.Loc),
		Label:     protocol.LetterLabel(v.Label),
	}
	if r := v.Raft; r != nil {
		entries := make([]protocol.LetterEntry, len(r.Entries))
		for i, e := range r.Entries {
			entries[i] = protocol.LetterEntry{
				Index:   e.Index,
				Term:    e.Term,
				Type:    protocol.EntryType(e.Type),
				Summary: e.Summary,
			}
		}
		l.Raft = &protocol.RaftInfo{
			Type:       r.Type,
			Term:       r.Term,
			LogTerm:    r.LogTerm,
			Index:      r.Index,
			Commit:     r.Commit,
			Entries:    entries,
			Reject:     r.Reject,
			RejectHint: r.RejectHint,
			Stale:      r.Stale,
		}
	}
	if q := v.Request; q != nil {
		l.Request = &protocol.RequestRef{ReqID: q.ReqID, Hops: q.Hops}
	}
	return l
}

// SnapshotFromSim は sim.Snapshot を protocol.Snapshot にする。
//
// sim.Snapshot が持つ分（phase・clock.t・clock.endsAt・ferries・routesState・letters・nodes・lastSeq）を写し、
// protocol を protocol.Version にする。それ以外は呼び出し側（T-302・T-303）が埋める。値はゼロ値で、
// 配列はすべて空の配列（JSON は []）。入力と何も共有しない。
func SnapshotFromSim(s sim.Snapshot) protocol.Snapshot {
	out := protocol.Snapshot{
		Protocol: protocol.Version,
		Phase:    protocol.Phase(s.Phase),
		Clock: protocol.SnapshotClock{
			Clock:         protocol.Clock{T: int64(s.T), EndsAt: int64(s.EndsAt)},
			AllowedSpeeds: []float64{},
		},
		Stage: protocol.StageView{
			Islands:   []protocol.Island{},
			Routes:    []protocol.Route{},
			Resources: []protocol.Resource{},
		},
		Ferries:      make([]protocol.Ferry, len(s.Ferries)),
		RoutesState:  make([]protocol.RouteState, len(s.Routes)),
		Letters:      make([]protocol.Letter, len(s.Letters)),
		Requests:     []protocol.Request{},
		Nodes:        make([]protocol.NodeSummary, len(s.Nodes)),
		IslandsState: []protocol.IslandState{},
		Score:        protocol.Score{CriticalSecured: []string{}},
		LastSeq:      s.LastSeq,
	}
	for i, f := range s.Ferries {
		cargo := make([]uint64, len(f.Cargo))
		copy(cargo, f.Cargo)
		out.Ferries[i] = protocol.Ferry{
			ID:       f.ID,
			Name:     f.Name,
			Capacity: f.Capacity,
			State:    protocol.FerryState(f.State),
			IslandID: f.IslandID,
			From:     f.From,
			To:       f.To,
			DepartAt: int64(f.DepartAt),
			ArriveAt: int64(f.ArriveAt),
			ReadyAt:  int64(f.ReadyAt),
			Cargo:    cargo,
		}
		if f.BrokenUntil != nil {
			until := int64(*f.BrokenUntil)
			out.Ferries[i].BrokenUntil = &until
		}
	}
	for i, r := range s.Routes {
		rs := protocol.RouteState{ID: r.ID}
		if r.BlockedUntil != nil {
			until := int64(*r.BlockedUntil)
			rs.BlockedUntil = &until
		}
		out.RoutesState[i] = rs
	}
	for i, l := range s.Letters {
		out.Letters[i] = LetterFromSim(l)
	}
	for i, n := range s.Nodes {
		out.Nodes[i] = protocol.NodeSummary{
			ID:        n.ID,
			Role:      protocol.Role(n.Role),
			Term:      n.Term,
			Vote:      n.Vote,
			Lead:      n.Lead,
			Commit:    n.Commit,
			Applied:   n.Applied,
			LastIndex: n.LastIndex,
		}
	}
	return out
}

// locFromSim は sim.LocView を protocol.Loc にする。
func locFromSim(v sim.LocView) protocol.Loc {
	return protocol.Loc{Type: protocol.LocType(v.Type), IslandID: v.IslandID, FerryID: v.FerryID}
}

// decodeObject は payload を JSON のオブジェクトとして読む。空・null は {}。
func decodeObject(typ protocol.MessageType, payload json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadPayload, typ, err)
	}
	if m == nil { // null
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

// field は m の key（大文字・小文字を区別する）の値を dst に読む。key がなければ何もしない。
func field(typ protocol.MessageType, m map[string]json.RawMessage, key string, dst any) error {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: %s: %s: %v", ErrBadPayload, typ, key, err)
	}
	return nil
}
