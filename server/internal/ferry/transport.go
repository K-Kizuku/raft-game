package ferry

import (
	"errors"
	"fmt"
	"slices"

	pb "go.etcd.io/raft/v3/raftpb"
)

// エラー。fmt.Errorf("%w: …") で詳細を添えて返すので、errors.Is で判定する。
var (
	ErrUnknownIsland     = errors.New("ferry: unknown island")
	ErrUnknownFerry      = errors.New("ferry: unknown ferry")
	ErrBadMessage        = errors.New("ferry: bad message")
	ErrInvalidArgument   = errors.New("ferry: invalid argument")
	ErrNotDocked         = errors.New("ferry: ferry is not docked")
	ErrNotReady          = errors.New("ferry: ferry is not ready to depart")
	ErrCapacityExceeded  = errors.New("ferry: capacity exceeded")
	ErrLetterNotHere     = errors.New("ferry: letter is not here")
	ErrNoRoute           = errors.New("ferry: no route")
	ErrRouteBlocked      = errors.New("ferry: route is blocked")
	ErrRelayNotSupported = errors.New("ferry: relay is not supported")
	ErrNotArriving       = errors.New("ferry: ferry is not arriving now")
)

// RouteConfig は航路の設定（ステージ定義の routes）。
type RouteConfig struct {
	A, B     uint64
	TravelMs int64
}

// FerryConfig は船の設定（ステージ定義の ferries）。
type FerryConfig struct {
	ID            FerryID
	Name          string
	Home          uint64
	Capacity      int
	SpeedPermille int64
}

// Config の値はすべてステージ定義（T-201）から渡す。このパッケージに既定値を持たない。
type Config struct {
	Islands       []uint64 // ノード ID（1..N）
	Routes        []RouteConfig
	Ferries       []FerryConfig
	DockMs        int64 // transport.dockMs
	PortCap       int   // transport.portCap（R5）
	PerLinkAppCap int   // transport.perLinkAppCap（R4）
}

// Transport は港・船・航路・手紙の状態を持つ。1 つの goroutine から使う。
type Transport struct {
	islands  []uint64
	isIsland map[uint64]bool
	routes   []Route
	routeIdx map[routeKey]int
	ferries  []Ferry
	ferryIdx map[FerryID]int
	letters  []Letter              // 添字 = ID - 1
	ports    map[uint64][]LetterID // 島 → その島が送信元の手紙（ID の昇順）

	dockMs        int64
	portCap       int
	perLinkAppCap int
}

// New は設定を検査して Transport を作る。
func New(cfg Config) (*Transport, error) {
	if len(cfg.Islands) == 0 {
		return nil, errors.New("ferry: no islands")
	}
	t := &Transport{
		islands:       slices.Clone(cfg.Islands),
		isIsland:      make(map[uint64]bool, len(cfg.Islands)),
		routeIdx:      make(map[routeKey]int, len(cfg.Routes)),
		ferryIdx:      make(map[FerryID]int, len(cfg.Ferries)),
		ports:         make(map[uint64][]LetterID, len(cfg.Islands)),
		dockMs:        cfg.DockMs,
		portCap:       cfg.PortCap,
		perLinkAppCap: cfg.PerLinkAppCap,
	}
	for _, id := range cfg.Islands {
		if id == 0 {
			return nil, errors.New("ferry: island id 0")
		}
		if t.isIsland[id] {
			return nil, fmt.Errorf("ferry: duplicate island %d", id)
		}
		t.isIsland[id] = true
	}
	for _, rc := range cfg.Routes {
		if !t.isIsland[rc.A] || !t.isIsland[rc.B] {
			return nil, fmt.Errorf("ferry: route %d-%d: unknown island", rc.A, rc.B)
		}
		if rc.A == rc.B {
			return nil, fmt.Errorf("ferry: route %d-%d: same island", rc.A, rc.B)
		}
		k := makeRouteKey(rc.A, rc.B)
		if _, dup := t.routeIdx[k]; dup {
			return nil, fmt.Errorf("ferry: duplicate route %d-%d", rc.A, rc.B)
		}
		if rc.TravelMs < 1 {
			return nil, fmt.Errorf("ferry: route %d-%d: travelMs %d < 1", rc.A, rc.B, rc.TravelMs)
		}
		t.routeIdx[k] = len(t.routes)
		t.routes = append(t.routes, Route{A: k.a, B: k.b, TravelMs: rc.TravelMs})
	}
	for _, fc := range cfg.Ferries {
		if fc.ID == "" {
			return nil, errors.New("ferry: empty ferry id")
		}
		if _, dup := t.ferryIdx[fc.ID]; dup {
			return nil, fmt.Errorf("ferry: duplicate ferry %q", fc.ID)
		}
		if !t.isIsland[fc.Home] {
			return nil, fmt.Errorf("ferry: ferry %q: unknown home %d", fc.ID, fc.Home)
		}
		if fc.Capacity < 1 {
			return nil, fmt.Errorf("ferry: ferry %q: capacity %d < 1", fc.ID, fc.Capacity)
		}
		if fc.SpeedPermille < 1 {
			return nil, fmt.Errorf("ferry: ferry %q: speedPermille %d < 1", fc.ID, fc.SpeedPermille)
		}
		t.ferryIdx[fc.ID] = len(t.ferries)
		t.ferries = append(t.ferries, Ferry{
			ID:            fc.ID,
			Name:          fc.Name,
			Capacity:      fc.Capacity,
			SpeedPermille: fc.SpeedPermille,
			State:         Docked,
			At:            fc.Home,
		})
	}
	if cfg.DockMs < 0 {
		return nil, fmt.Errorf("ferry: dockMs %d < 0", cfg.DockMs)
	}
	if cfg.PortCap < 1 {
		return nil, fmt.Errorf("ferry: portCap %d < 1", cfg.PortCap)
	}
	if cfg.PerLinkAppCap < 1 {
		return nil, fmt.Errorf("ferry: perLinkAppCap %d < 1", cfg.PerLinkAppCap)
	}
	return t, nil
}

func (t *Transport) letter(id LetterID) *Letter {
	if id == 0 || uint64(id) > uint64(len(t.letters)) {
		return nil
	}
	return &t.letters[id-1]
}

// Letter は手紙の値の複製を返す（Req は複製、Raft は同じポインタ）。
func (t *Transport) Letter(id LetterID) (Letter, bool) {
	l := t.letter(id)
	if l == nil {
		return Letter{}, false
	}
	c := *l
	if l.Req != nil {
		r := *l.Req
		c.Req = &r
	}
	return c, true
}

// Port は島の港にある手紙の ID（昇順）の複製を返す。島がなければ nil。
func (t *Transport) Port(island uint64) []LetterID {
	if !t.isIsland[island] {
		return nil
	}
	p := t.ports[island]
	if p == nil {
		return []LetterID{}
	}
	return slices.Clone(p)
}

func cloneFerry(f Ferry) Ferry {
	f.Cargo = slices.Clone(f.Cargo)
	if f.Cargo == nil {
		f.Cargo = []LetterID{}
	}
	return f
}

// Ferry は船の値の複製を返す（Cargo も複製）。
func (t *Transport) Ferry(id FerryID) (Ferry, bool) {
	i, ok := t.ferryIdx[id]
	if !ok {
		return Ferry{}, false
	}
	return cloneFerry(t.ferries[i]), true
}

// Ferries は船の複製を Config の順に返す。
func (t *Transport) Ferries() []Ferry {
	out := make([]Ferry, len(t.ferries))
	for i, f := range t.ferries {
		out[i] = cloneFerry(f)
	}
	return out
}

// Routes は航路の複製を Config の順に返す。
func (t *Transport) Routes() []Route { return slices.Clone(t.routes) }

// CheckInvariants は港・船・手紙の不変条件（V-7・I-7）を確かめ、違反を 1 つ見つけたら
// 説明つきのエラーを返す。
func (t *Transport) CheckInvariants() error {
	seen := make([]bool, len(t.letters)) // 港・船の一覧に出てきた手紙
	mark := func(id LetterID, where string) error {
		l := t.letter(id)
		if l == nil {
			return fmt.Errorf("ferry: invariant: %s: unknown letter %d", where, id)
		}
		if seen[id-1] {
			return fmt.Errorf("ferry: invariant: letter %d listed twice (%s)", id, where)
		}
		seen[id-1] = true
		return nil
	}
	for _, island := range t.islands {
		port := t.ports[island]
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
			if err := mark(id, where); err != nil {
				return err
			}
			l := t.letter(id)
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
				if appN[l.To] > t.perLinkAppCap {
					return fmt.Errorf("ferry: invariant: %s: MsgApp to %d exceeds perLinkAppCap %d", where, l.To, t.perLinkAppCap)
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
					p := t.letter(prev)
					if p.Kind == LetterRaft && p.Raft.GetType() == typ && p.To == l.To && p.Raft.GetTerm() <= m.GetTerm() {
						return fmt.Errorf("ferry: invariant: %s: %v to %d: letter %d (term %d) not superseded by %d (term %d)",
							where, typ, l.To, prev, p.Raft.GetTerm(), id, m.GetTerm())
					}
				}
			}
		}
		if raftN > t.portCap {
			return fmt.Errorf("ferry: invariant: %s: %d raft letters exceed portCap %d", where, raftN, t.portCap)
		}
	}
	for _, f := range t.ferries {
		where := fmt.Sprintf("ferry %q", f.ID)
		if len(f.Cargo) > f.Capacity {
			return fmt.Errorf("ferry: invariant: %s: cargo %d exceeds capacity %d", where, len(f.Cargo), f.Capacity)
		}
		for _, id := range f.Cargo {
			if err := mark(id, where); err != nil {
				return err
			}
			if l := t.letter(id); l.Location != (Location{Kind: LocFerry, Ferry: f.ID}) {
				return fmt.Errorf("ferry: invariant: %s: letter %d location %+v", where, id, l.Location)
			}
		}
		switch f.State {
		case Docked:
			if !t.isIsland[f.At] || f.From != 0 || f.To != 0 {
				return fmt.Errorf("ferry: invariant: %s: docked with at=%d from=%d to=%d", where, f.At, f.From, f.To)
			}
		case Sailing:
			if _, ok := t.route(f.From, f.To); f.At != 0 || !ok || f.From == f.To {
				return fmt.Errorf("ferry: invariant: %s: sailing with at=%d from=%d to=%d", where, f.At, f.From, f.To)
			}
		default:
			return fmt.Errorf("ferry: invariant: %s: unknown state %d", where, f.State)
		}
	}
	for i := range t.letters {
		l := &t.letters[i]
		if l.ID != LetterID(i+1) {
			return fmt.Errorf("ferry: invariant: letter at %d has id %d", i, l.ID)
		}
		switch l.Location.Kind {
		case LocPort, LocFerry:
			if !seen[i] {
				return fmt.Errorf("ferry: invariant: letter %d at %+v is not listed", l.ID, l.Location)
			}
		default:
			if seen[i] {
				return fmt.Errorf("ferry: invariant: letter %d at %+v is listed", l.ID, l.Location)
			}
		}
	}
	return nil
}
