package ferry

import (
	"fmt"
	"slices"

	pb "go.etcd.io/raft/v3/raftpb"
)

type kindTo struct {
	typ pb.MessageType
	to  uint64
}

func isHeartbeat(t pb.MessageType) bool {
	return t == pb.MsgHeartbeat || t == pb.MsgHeartbeatResp
}

func isElection(t pb.MessageType) bool {
	switch t {
	case pb.MsgPreVote, pb.MsgVote, pb.MsgPreVoteResp, pb.MsgVoteResp:
		return true
	}
	return false
}

// priority は R5 の優先度（小さいほど先に破棄する）。
func priority(t pb.MessageType) int {
	switch {
	case isHeartbeat(t):
		return 1
	case t == pb.MsgAppResp:
		return 2
	case t == pb.MsgApp:
		return 3
	case isElection(t):
		return 4
	default: // MsgSnap・MsgTimeoutNow など（設計書にない判断: 最も低くする）
		return 0
	}
}

// PostRaft は Raft のメッセージを順に手紙にして from の港に入れる。
// 先にすべてを検査し、誤りがあれば何も変えずにエラーを返す。
// m は書き換えず、ポインタをそのまま持つ。
func (t *Transport) PostRaft(now int64, from uint64, msgs []*pb.Message) (created []LetterID, dropped []Drop, err error) {
	if !t.isIsland[from] {
		return nil, nil, fmt.Errorf("%w: %d", ErrUnknownIsland, from)
	}
	for i, m := range msgs {
		switch {
		case m == nil:
			return nil, nil, fmt.Errorf("%w: msgs[%d] is nil", ErrBadMessage, i)
		case m.GetFrom() != from:
			return nil, nil, fmt.Errorf("%w: msgs[%d] from %d, want %d", ErrBadMessage, i, m.GetFrom(), from)
		case !t.isIsland[m.GetTo()]:
			return nil, nil, fmt.Errorf("%w: msgs[%d] to unknown island %d", ErrBadMessage, i, m.GetTo())
		case m.GetTo() == from:
			return nil, nil, fmt.Errorf("%w: msgs[%d] to itself %d", ErrBadMessage, i, from)
		}
	}
	for _, m := range msgs {
		id := t.newLetter(Letter{Kind: LetterRaft, From: from, To: m.GetTo(), CreatedAt: now, Raft: m})
		created = append(created, id)
		dropped = t.enqueue(from, id, dropped)
	}
	return created, dropped, nil
}

// PostRequest は依頼書を 1 通作って from の港に入れる。
func (t *Transport) PostRequest(now int64, from, to uint64, note RequestNote) (id LetterID, dropped []Drop, err error) {
	if !t.isIsland[from] {
		return 0, nil, fmt.Errorf("%w: from %d", ErrUnknownIsland, from)
	}
	if !t.isIsland[to] {
		return 0, nil, fmt.Errorf("%w: to %d", ErrUnknownIsland, to)
	}
	if from == to {
		return 0, nil, fmt.Errorf("%w: request to itself %d", ErrInvalidArgument, from)
	}
	if note.ReqID == "" {
		return 0, nil, fmt.Errorf("%w: empty reqId", ErrInvalidArgument)
	}
	n := note
	id = t.newLetter(Letter{Kind: LetterRequest, From: from, To: to, CreatedAt: now, Req: &n})
	dropped = t.enqueue(from, id, nil)
	return id, dropped, nil
}

func (t *Transport) newLetter(l Letter) LetterID {
	l.ID = LetterID(len(t.letters) + 1)
	t.letters = append(t.letters, l)
	return l.ID
}

// enqueue は手紙 id を island の港の ID の順の位置に入れ、規則 R1〜R6 を適用する。
// 破棄を dropped の末尾に足して返す。
func (t *Transport) enqueue(island uint64, id LetterID, dropped []Drop) []Drop {
	port := t.ports[island]
	pos, _ := slices.BinarySearch(port, id)
	port = slices.Insert(port, pos, id)
	t.ports[island] = port
	l := t.letter(id)
	l.Location = Location{Kind: LocPort, Island: island}

	drop := func(victim LetterID, reason DropReason, rule string) {
		t.removeFromPort(island, victim)
		t.letter(victim).Location = Location{Kind: LocDropped}
		dropped = append(dropped, Drop{Letter: victim, Reason: reason, Rule: rule})
	}
	alive := func() bool { return t.letter(id).Location.Kind == LocPort }

	if l.Kind == LetterRequest {
		// R6: 同じ ReqID の依頼書のうち、ID が最大のものだけを残す。
		key := l.Req.ReqID
		victims := t.matching(island, func(e *Letter) bool { return e.Kind == LetterRequest && e.Req.ReqID == key })
		for _, v := range victims[:len(victims)-1] {
			drop(v, DropSuperseded, "R6")
		}
		return dropped
	}

	m := l.Raft
	typ, to := m.GetType(), l.To
	isRaft := func(e *Letter) bool { return e.Kind == LetterRaft }
	switch {
	case isHeartbeat(typ):
		// R1: 同じ (種類, 宛先) の手紙のうち、ID が最大のものだけを残す。
		ms := t.matching(island, func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == typ && e.To == to })
		for _, v := range ms[:len(ms)-1] {
			drop(v, DropSuperseded, "R1")
		}
	case isElection(typ):
		// R2: 古く Term が L 以下の E を破棄する。新しく Term が L 以上の E があれば L を破棄する。
		term := m.GetTerm()
		ms := t.matching(island, func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == typ && e.To == to })
		for _, v := range ms {
			if v < id && t.letter(v).Raft.GetTerm() <= term {
				drop(v, DropSuperseded, "R2")
			}
		}
		for _, v := range ms {
			if v > id && term <= t.letter(v).Raft.GetTerm() {
				drop(id, DropSuperseded, "R2")
				break
			}
		}
	case typ == pb.MsgAppResp && !m.GetReject():
		// R3: 同じ宛先の拒否でない MsgAppResp のうち、ID が最大のものだけを残す。
		ms := t.matching(island, func(e *Letter) bool {
			return isRaft(e) && e.Raft.GetType() == pb.MsgAppResp && !e.Raft.GetReject() && e.To == to
		})
		for _, v := range ms[:len(ms)-1] {
			drop(v, DropSuperseded, "R3")
		}
	case typ == pb.MsgApp:
		// R4: 同じ宛先の MsgApp が PerLinkAppCap 通を超えている間、ID が最小のものを破棄する。
		ms := t.matching(island, func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == pb.MsgApp && e.To == to })
		for i := 0; len(ms)-i > t.perLinkAppCap; i++ {
			drop(ms[i], DropOverflow, "R4")
		}
	}
	if !alive() {
		return dropped
	}
	// R5: 港の Raft の手紙が PortCap 通を超えている間、優先度が最も低い区分のうち ID が最小のものを破棄する。
	for {
		count := 0
		var victim LetterID
		best := 0
		for _, e := range t.ports[island] {
			el := t.letter(e)
			if el.Kind != LetterRaft {
				continue
			}
			count++
			if p := priority(el.Raft.GetType()); victim == 0 || p < best {
				victim, best = e, p
			}
		}
		if count <= t.portCap {
			break
		}
		drop(victim, DropOverflow, "R5")
	}
	return dropped
}

// matching は island の港で pred を満たす手紙の ID を昇順に返す。
func (t *Transport) matching(island uint64, pred func(*Letter) bool) []LetterID {
	var out []LetterID
	for _, id := range t.ports[island] {
		if pred(t.letter(id)) {
			out = append(out, id)
		}
	}
	return out
}

func (t *Transport) removeFromPort(island uint64, id LetterID) {
	port := t.ports[island]
	if i, ok := slices.BinarySearch(port, id); ok {
		t.ports[island] = slices.Delete(port, i, i+1)
	}
}
