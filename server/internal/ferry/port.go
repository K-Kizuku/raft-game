package ferry

import (
	"cmp"
	"errors"
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

// PortConfig は 1 島の港（Port）の設定。値はステージ定義から渡す。
type PortConfig struct {
	Island        uint64          // この港の島
	Islands       []uint64        // 全島の ID（宛先の検査に使う）
	PortCap       int             // transport.portCap（R5）
	PerLinkAppCap int             // transport.perLinkAppCap（R4）
	RelayCap      int             // transport.relayCap（0 = 積み替えなし）
	NewID         func() LetterID // 手紙の ID を振る関数（必須）。振った ID は単調に増え、0 を返さない
}

// Port は 1 島の港と中継倉庫（DTN-00 #363 の IslandCore が持つ）。手紙の中身（Raft のメッセージ・依頼書）を持ち、
// 港に入れるたびに規則 R1〜R6 を適用する。船・航路・ほかの島の状態を持たない。
//
// 破棄した手紙はすぐに手放す（Letter で引けない）。配達・船への積み込みで港・倉庫から出た手紙も持たない。
// 1 つの goroutine から使う。
type Port struct {
	island        uint64
	isIsland      map[uint64]bool // 検索だけに使い、走査しない
	letters       []Letter        // ID の昇順（狭義）。港と中継倉庫にある手紙
	port          []LetterID      // 港の手紙（ID の昇順）
	relay         []LetterID      // 中継倉庫の手紙（ID の昇順）
	newID         func() LetterID
	portCap       int
	perLinkAppCap int
	relayCap      int
}

// NewPort は設定を検査して Port を作る。
func NewPort(cfg PortConfig) (*Port, error) {
	p := &Port{
		island:        cfg.Island,
		isIsland:      make(map[uint64]bool, len(cfg.Islands)),
		newID:         cfg.NewID,
		portCap:       cfg.PortCap,
		perLinkAppCap: cfg.PerLinkAppCap,
		relayCap:      cfg.RelayCap,
	}
	for _, id := range cfg.Islands {
		if id == 0 {
			return nil, errors.New("ferry: island id 0")
		}
		p.isIsland[id] = true
	}
	switch {
	case !p.isIsland[cfg.Island]:
		return nil, fmt.Errorf("ferry: port island %d is not in islands", cfg.Island)
	case cfg.NewID == nil:
		return nil, errors.New("ferry: port: NewID is nil")
	case cfg.PortCap < 1:
		return nil, fmt.Errorf("ferry: portCap %d < 1", cfg.PortCap)
	case cfg.PerLinkAppCap < 1:
		return nil, fmt.Errorf("ferry: perLinkAppCap %d < 1", cfg.PerLinkAppCap)
	case cfg.RelayCap < 0:
		return nil, fmt.Errorf("ferry: relayCap %d < 0", cfg.RelayCap)
	}
	return p, nil
}

// Island はこの港の島を返す。
func (p *Port) Island() uint64 { return p.island }

func (p *Port) index(id LetterID) (int, bool) {
	return slices.BinarySearchFunc(p.letters, id, func(l Letter, id LetterID) int { return cmp.Compare(l.ID, id) })
}

func (p *Port) letter(id LetterID) *Letter {
	i, ok := p.index(id)
	if !ok {
		return nil
	}
	return &p.letters[i]
}

// Letter は港・中継倉庫にある手紙の値の複製を返す（Req は複製、Raft は同じポインタ）。なければ false。
func (p *Port) Letter(id LetterID) (Letter, bool) {
	l := p.letter(id)
	if l == nil {
		return Letter{}, false
	}
	return cloneLetter(*l), true
}

func cloneLetter(l Letter) Letter {
	if l.Req != nil {
		r := *l.Req
		l.Req = &r
	}
	return l
}

// Port は港の手紙の ID（昇順）の複製を返す。nil にしない。
func (p *Port) Port() []LetterID {
	if p.port == nil {
		return []LetterID{}
	}
	return slices.Clone(p.port)
}

// Relay は中継倉庫の手紙の ID（昇順）の複製を返す。nil にしない。
func (p *Port) Relay() []LetterID {
	if p.relay == nil {
		return []LetterID{}
	}
	return slices.Clone(p.relay)
}

// insert は手紙 l を letters の ID の順の位置に入れる。同じ ID があれば誤り。
func (p *Port) insert(l Letter) error {
	i, ok := p.index(l.ID)
	if ok || l.ID == 0 {
		return fmt.Errorf("%w: letter %d already in port %d (or id 0)", ErrInvalidArgument, l.ID, p.island)
	}
	p.letters = slices.Insert(p.letters, i, cloneLetter(l))
	return nil
}

func (p *Port) remove(id LetterID) {
	if i, ok := p.index(id); ok {
		p.letters = slices.Delete(p.letters, i, i+1)
	}
}

// PostRaft は Raft のメッセージを順に手紙にしてこの港に入れ、作った手紙（港に置いた時点の値）と破棄を返す。
// 先にすべてを検査し、誤りがあれば何も変えずにエラーを返す。m は書き換えず、ポインタをそのまま持つ。
// 作った手紙が同じ呼び出しの中で破棄されることもある（created と dropped の両方に入る）。
func (p *Port) PostRaft(now int64, msgs []*pb.Message) (created []Letter, dropped []Drop, err error) {
	from := p.island
	for i, m := range msgs {
		switch {
		case m == nil:
			return nil, nil, fmt.Errorf("%w: msgs[%d] is nil", ErrBadMessage, i)
		case m.GetFrom() != from:
			return nil, nil, fmt.Errorf("%w: msgs[%d] from %d, want %d", ErrBadMessage, i, m.GetFrom(), from)
		case !p.isIsland[m.GetTo()]:
			return nil, nil, fmt.Errorf("%w: msgs[%d] to unknown island %d", ErrBadMessage, i, m.GetTo())
		case m.GetTo() == from:
			return nil, nil, fmt.Errorf("%w: msgs[%d] to itself %d", ErrBadMessage, i, from)
		}
	}
	for _, m := range msgs {
		l := Letter{ID: p.newID(), Kind: LetterRaft, From: from, To: m.GetTo(), CreatedAt: now, Raft: m,
			Location: Location{Kind: LocPort, Island: from}}
		if err := p.insert(l); err != nil {
			panic(fmt.Sprintf("ferry: port %d: new letter: %v", from, err))
		}
		created = append(created, l)
		dropped = p.enqueue(l.ID, dropped)
	}
	return created, dropped, nil
}

// PostRequest は依頼書を 1 通作ってこの港に入れる。
func (p *Port) PostRequest(now int64, to uint64, note RequestNote) (created Letter, dropped []Drop, err error) {
	from := p.island
	if !p.isIsland[to] {
		return Letter{}, nil, fmt.Errorf("%w: to %d", ErrUnknownIsland, to)
	}
	if from == to {
		return Letter{}, nil, fmt.Errorf("%w: request to itself %d", ErrInvalidArgument, from)
	}
	if note.ReqID == "" {
		return Letter{}, nil, fmt.Errorf("%w: empty reqId", ErrInvalidArgument)
	}
	n := note
	l := Letter{ID: p.newID(), Kind: LetterRequest, From: from, To: to, CreatedAt: now, Req: &n,
		Location: Location{Kind: LocPort, Island: from}}
	if err := p.insert(l); err != nil {
		panic(fmt.Sprintf("ferry: port %d: new letter: %v", from, err))
	}
	dropped = p.enqueue(l.ID, nil)
	return cloneLetter(l), dropped, nil
}

// Return は、船から降ろした、この島が送信元の手紙 l を港に戻し、規則 R1〜R6 を適用する（cmd.ferry.unload）。
// l.From がこの島でない・同じ ID がもうある手紙は ErrInvalidArgument で、何も変えない。
func (p *Port) Return(l Letter) (dropped []Drop, err error) {
	if l.From != p.island {
		return nil, fmt.Errorf("%w: letter %d from %d returned to port %d", ErrInvalidArgument, l.ID, l.From, p.island)
	}
	l.Location = Location{Kind: LocPort, Island: p.island}
	if err := p.insert(l); err != nil {
		return nil, err
	}
	return p.enqueue(l.ID, nil), nil
}

// Store は、船から降ろした経由の手紙 l を中継倉庫に預ける（合流・破棄しない）。
// l の送信元か宛先がこの島なら ErrInvalidArgument、倉庫が relayCap 通を超えるなら ErrRelayFull で、何も変えない。
func (p *Port) Store(l Letter) error {
	if l.From == p.island || l.To == p.island {
		return fmt.Errorf("%w: letter %d (%d→%d) stored in relay %d", ErrInvalidArgument, l.ID, l.From, l.To, p.island)
	}
	if len(p.relay)+1 > p.relayCap {
		return fmt.Errorf("%w: relay %d has %d of %d", ErrRelayFull, p.island, len(p.relay), p.relayCap)
	}
	l.Location = Location{Kind: LocRelay, Island: p.island}
	if err := p.insert(l); err != nil {
		return err
	}
	pos, _ := slices.BinarySearch(p.relay, l.ID)
	p.relay = slices.Insert(p.relay, pos, l.ID)
	return nil
}

// Take は港の手紙 ids を渡した順に取り出す（船に積んだ。actor-protocol の port.remove）。
// 1 通でも港になければ ErrLetterNotHere で、何も変えない。
func (p *Port) Take(ids []LetterID) ([]Letter, error) {
	return p.take(ids, LocPort, &p.port)
}

// TakeRelay は中継倉庫の手紙 ids を渡した順に取り出す（relay.remove）。
func (p *Port) TakeRelay(ids []LetterID) ([]Letter, error) {
	return p.take(ids, LocRelay, &p.relay)
}

func (p *Port) take(ids []LetterID, kind LocationKind, list *[]LetterID) ([]Letter, error) {
	if err := checkIDs(ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if l := p.letter(id); l == nil || l.Location.Kind != kind {
			return nil, fmt.Errorf("%w: letter %d is not in %s %d", ErrLetterNotHere, id, locName(kind), p.island)
		}
	}
	out := make([]Letter, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneLetter(*p.letter(id)))
		if i, ok := slices.BinarySearch(*list, id); ok {
			*list = slices.Delete(*list, i, i+1)
		}
		p.remove(id)
	}
	return out, nil
}

func locName(k LocationKind) string {
	if k == LocRelay {
		return "relay"
	}
	return "port"
}

// enqueue は港の letters にある手紙 id を港の ID の順の位置に入れ、規則 R1〜R6 を適用する。
// 破棄を dropped の末尾に足して返す。破棄した手紙は手放す。
func (p *Port) enqueue(id LetterID, dropped []Drop) []Drop {
	pos, _ := slices.BinarySearch(p.port, id)
	p.port = slices.Insert(p.port, pos, id)
	l := p.letter(id)

	drop := func(victim LetterID, reason DropReason, rule string) {
		if i, ok := slices.BinarySearch(p.port, victim); ok {
			p.port = slices.Delete(p.port, i, i+1)
		}
		p.remove(victim)
		dropped = append(dropped, Drop{Letter: victim, Reason: reason, Rule: rule})
	}
	alive := func() bool { return p.letter(id) != nil }

	if l.Kind == LetterRequest {
		// R6: 同じ ReqID の依頼書のうち、ID が最大のものだけを残す。
		key := l.Req.ReqID
		victims := p.matching(func(e *Letter) bool { return e.Kind == LetterRequest && e.Req.ReqID == key })
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
		ms := p.matching(func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == typ && e.To == to })
		for _, v := range ms[:len(ms)-1] {
			drop(v, DropSuperseded, "R1")
		}
	case isElection(typ):
		// R2: 古く Term が L 以下の E を破棄する。新しく Term が L 以上の E があれば L を破棄する。
		term := m.GetTerm()
		ms := p.matching(func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == typ && e.To == to })
		for _, v := range ms {
			if v < id && p.letter(v).Raft.GetTerm() <= term {
				drop(v, DropSuperseded, "R2")
			}
		}
		for _, v := range ms {
			if v > id && term <= p.letter(v).Raft.GetTerm() {
				drop(id, DropSuperseded, "R2")
				break
			}
		}
	case typ == pb.MsgAppResp && !m.GetReject():
		// R3: 同じ宛先の拒否でない MsgAppResp のうち、ID が最大のものだけを残す。
		ms := p.matching(func(e *Letter) bool {
			return isRaft(e) && e.Raft.GetType() == pb.MsgAppResp && !e.Raft.GetReject() && e.To == to
		})
		for _, v := range ms[:len(ms)-1] {
			drop(v, DropSuperseded, "R3")
		}
	case typ == pb.MsgApp:
		// R4: 同じ宛先の MsgApp が PerLinkAppCap 通を超えている間、ID が最小のものを破棄する。
		ms := p.matching(func(e *Letter) bool { return isRaft(e) && e.Raft.GetType() == pb.MsgApp && e.To == to })
		for i := 0; len(ms)-i > p.perLinkAppCap; i++ {
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
		for _, e := range p.port {
			el := p.letter(e)
			if el.Kind != LetterRaft {
				continue
			}
			count++
			if pr := priority(el.Raft.GetType()); victim == 0 || pr < best {
				victim, best = e, pr
			}
		}
		if count <= p.portCap {
			break
		}
		drop(victim, DropOverflow, "R5")
	}
	return dropped
}

// matching は港で pred を満たす手紙の ID を昇順に返す。
func (p *Port) matching(pred func(*Letter) bool) []LetterID {
	var out []LetterID
	for _, id := range p.port {
		if pred(p.letter(id)) {
			out = append(out, id)
		}
	}
	return out
}

// CheckInvariants は港・中継倉庫の不変条件（V-7 の港の規則の結果・倉庫の上限・並び）を確かめる。
func (p *Port) CheckInvariants() error {
	for i := range p.letters {
		if i > 0 && p.letters[i-1].ID >= p.letters[i].ID {
			return fmt.Errorf("ferry: invariant: port %d: letters not strictly ascending at %d", p.island, i)
		}
	}
	seen := 0
	for _, list := range [][]LetterID{p.port, p.relay} {
		for _, id := range list {
			if p.letter(id) == nil {
				return fmt.Errorf("ferry: invariant: port %d: unknown letter %d", p.island, id)
			}
			seen++
		}
	}
	if seen != len(p.letters) {
		return fmt.Errorf("ferry: invariant: port %d: %d letters listed, %d held", p.island, seen, len(p.letters))
	}
	if err := checkPortRules(p.island, p.port, p.letter, p.portCap, p.perLinkAppCap); err != nil {
		return err
	}
	return checkRelay(p.island, p.relay, p.letter, p.relayCap)
}

// checkPortRules は island の港の手紙 port（letter で引く）が、並び・場所・規則 R1〜R6 の結果を満たすかを確かめる。
func checkPortRules(island uint64, port []LetterID, letter func(LetterID) *Letter, portCap, perLinkAppCap int) error {
	where := fmt.Sprintf("port %d", island)
	raftN := 0
	appN := make(map[uint64]int)
	hb := make(map[kindTo]int)
	appResp := make(map[uint64]int)
	reqs := make(map[string]int)
	for i, id := range port {
		if i > 0 && port[i-1] >= id {
			return fmt.Errorf("ferry: invariant: %s: ids not strictly ascending at %d", where, i)
		}
		l := letter(id)
		if l == nil {
			return fmt.Errorf("ferry: invariant: %s: unknown letter %d", where, id)
		}
		if l.Location != (Location{Kind: LocPort, Island: island}) {
			return fmt.Errorf("ferry: invariant: %s: letter %d location %+v", where, id, l.Location)
		}
		if l.From != island {
			return fmt.Errorf("ferry: invariant: %s: letter %d from %d", where, id, l.From)
		}
		if l.Kind == LetterRequest {
			reqs[l.Req.ReqID]++
			if reqs[l.Req.ReqID] > 1 {
				return fmt.Errorf("ferry: invariant: %s: request %q twice", where, l.Req.ReqID)
			}
			continue
		}
		raftN++
		m := l.Raft
		switch typ := m.GetType(); {
		case typ == pb.MsgApp:
			appN[l.To]++
			if appN[l.To] > perLinkAppCap {
				return fmt.Errorf("ferry: invariant: %s: MsgApp to %d exceeds perLinkAppCap %d", where, l.To, perLinkAppCap)
			}
		case isHeartbeat(typ):
			k := kindTo{typ, l.To}
			hb[k]++
			if hb[k] > 1 {
				return fmt.Errorf("ferry: invariant: %s: %v to %d twice", where, typ, l.To)
			}
		case typ == pb.MsgAppResp && !m.GetReject():
			appResp[l.To]++
			if appResp[l.To] > 1 {
				return fmt.Errorf("ferry: invariant: %s: MsgAppResp to %d twice", where, l.To)
			}
		case isElection(typ):
			for _, prev := range port[:i] {
				pl := letter(prev)
				if pl.Kind == LetterRaft && pl.Raft.GetType() == typ && pl.To == l.To && pl.Raft.GetTerm() <= m.GetTerm() {
					return fmt.Errorf("ferry: invariant: %s: %v to %d: letter %d (term %d) not superseded by %d (term %d)",
						where, typ, l.To, prev, pl.Raft.GetTerm(), id, m.GetTerm())
				}
			}
		}
	}
	if raftN > portCap {
		return fmt.Errorf("ferry: invariant: %s: %d raft letters exceed portCap %d", where, raftN, portCap)
	}
	return nil
}

// checkRelay は island の中継倉庫の手紙 relay が、並び・場所・送信元と宛先・上限を満たすかを確かめる。
func checkRelay(island uint64, relay []LetterID, letter func(LetterID) *Letter, relayCap int) error {
	where := fmt.Sprintf("relay %d", island)
	for i, id := range relay {
		if i > 0 && relay[i-1] >= id {
			return fmt.Errorf("ferry: invariant: %s: ids not strictly ascending at %d", where, i)
		}
		l := letter(id)
		if l == nil {
			return fmt.Errorf("ferry: invariant: %s: unknown letter %d", where, id)
		}
		if l.Location != (Location{Kind: LocRelay, Island: island}) {
			return fmt.Errorf("ferry: invariant: %s: letter %d location %+v", where, id, l.Location)
		}
		if l.To == island {
			return fmt.Errorf("ferry: invariant: %s: letter %d to %d", where, id, l.To)
		}
		if l.From == island {
			return fmt.Errorf("ferry: invariant: %s: letter %d from %d", where, id, l.From)
		}
	}
	if len(relay) > relayCap {
		return fmt.Errorf("ferry: invariant: %s: %d letters exceed relayCap %d", where, len(relay), relayCap)
	}
	return nil
}
