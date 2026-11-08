package ferry

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// FleetConfig は Fleet の設定。値はすべてステージ定義から渡す。
type FleetConfig struct {
	Islands  []uint64 // ノード ID（1..N）
	Routes   []RouteConfig
	Ferries  []FerryConfig
	DockMs   int64 // transport.dockMs
	RelayCap int   // transport.relayCap（Unload の ErrRelayFull の判定に使う）
}

// Fleet は航路・船・船の時刻表と、すべての手紙のメタデータの写し（所在）を持つ（DTN-00 #363 の CoordinatorCore が持つ）。
// 港の規則 R1〜R6 は適用しない（港は島の Port が持つ）。手紙の作成・破棄は、島の Port の結果を Track・MarkDropped
// で写す。Load・Unload・RelayLoad・Arrive は写しの所在で検査して所在と積み荷を変え、島の Port への反映
// （Take・Return・Store・配達）は呼び出し側が行う。
//
// 参照モデル（sim.Engine）では、写しの手紙は Port の手紙と同じ中身（Raft のメッセージのポインタ・依頼書）を持つ。
// 1 つの goroutine から使う。
type Fleet struct {
	islands  []uint64
	isIsland map[uint64]bool
	routes   []Route
	routeIdx map[routeKey]int
	ferries  []Ferry
	ferryIdx map[FerryID]int
	letters  []Letter              // ID の昇順（狭義）。港・船・中継倉庫にある手紙と、まだ Reclaim していない配達済み・破棄の手紙
	ports    map[uint64][]LetterID // 島 → その島が送信元で港にある手紙（ID の昇順）
	relays   map[uint64][]LetterID // 島 → その島の中継倉庫の手紙（ID の昇順）

	dockMs   int64
	relayCap int
}

// NewFleet は設定を検査して Fleet を作る。
func NewFleet(cfg FleetConfig) (*Fleet, error) {
	if len(cfg.Islands) == 0 {
		return nil, errors.New("ferry: no islands")
	}
	t := &Fleet{
		islands:  slices.Clone(cfg.Islands),
		isIsland: make(map[uint64]bool, len(cfg.Islands)),
		routeIdx: make(map[routeKey]int, len(cfg.Routes)),
		ferryIdx: make(map[FerryID]int, len(cfg.Ferries)),
		ports:    make(map[uint64][]LetterID, len(cfg.Islands)),
		relays:   make(map[uint64][]LetterID, len(cfg.Islands)),
		dockMs:   cfg.DockMs,
		relayCap: cfg.RelayCap,
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
	if cfg.RelayCap < 0 {
		return nil, fmt.Errorf("ferry: relayCap %d < 0", cfg.RelayCap)
	}
	return t, nil
}

// index は手紙 id の t.letters での添字（なければ入れる位置）を返す。
func (t *Fleet) index(id LetterID) (int, bool) {
	if id == 0 {
		return 0, false
	}
	return slices.BinarySearchFunc(t.letters, id, func(l Letter, id LetterID) int { return cmp.Compare(l.ID, id) })
}

func (t *Fleet) letter(id LetterID) *Letter {
	i, ok := t.index(id)
	if !ok {
		return nil
	}
	return &t.letters[i]
}

// Track は、島の港で作った手紙 l（Location は {LocPort, l.From}）を写しに加える。
// 同じ ID の手紙がある・ID が 0・場所が送信元の港でない・島がないときは ErrInvalidArgument で、何も変えない。
func (t *Fleet) Track(l Letter) error {
	if !t.isIsland[l.From] || !t.isIsland[l.To] {
		return fmt.Errorf("%w: letter %d %d→%d: unknown island", ErrInvalidArgument, l.ID, l.From, l.To)
	}
	if l.Location != (Location{Kind: LocPort, Island: l.From}) {
		return fmt.Errorf("%w: letter %d location %+v, want port %d", ErrInvalidArgument, l.ID, l.Location, l.From)
	}
	i, ok := t.index(l.ID)
	if ok || l.ID == 0 {
		return fmt.Errorf("%w: letter %d already tracked (or id 0)", ErrInvalidArgument, l.ID)
	}
	t.letters = slices.Insert(t.letters, i, cloneLetter(l))
	port := t.ports[l.From]
	pos, _ := slices.BinarySearch(port, l.ID)
	t.ports[l.From] = slices.Insert(port, pos, l.ID)
	return nil
}

// MarkDropped は、港の規則で破棄した手紙 id を写しで破棄にする（Reclaim まで Letter で引ける）。
// 港にない手紙は ErrLetterNotHere で、何も変えない。
func (t *Fleet) MarkDropped(id LetterID) error {
	l := t.letter(id)
	if l == nil || l.Location.Kind != LocPort {
		return fmt.Errorf("%w: letter %d is not in a port", ErrLetterNotHere, id)
	}
	t.removeFromPort(l.Location.Island, id)
	l.Location = Location{Kind: LocDropped}
	return nil
}

func (t *Fleet) removeFromPort(island uint64, id LetterID) {
	port := t.ports[island]
	if i, ok := slices.BinarySearch(port, id); ok {
		t.ports[island] = slices.Delete(port, i, i+1)
	}
}

// Letter は手紙の値の複製を返す（Req は複製、Raft は同じポインタ）。
// 配達済み・破棄した手紙も、Reclaim を呼ぶまでは引ける（Location が LocDelivered・LocDropped）。
// Reclaim で手放した ID・知らない ID・0 は (Letter{}, false) を返す。
func (t *Fleet) Letter(id LetterID) (Letter, bool) {
	l := t.letter(id)
	if l == nil {
		return Letter{}, false
	}
	return cloneLetter(*l), true
}

// Reclaim は、配達済み・破棄した手紙（LocDelivered・LocDropped）をすべて手放し、その数を返す。
// 手放した手紙は Letter で引けなくなる（Load・Unload では存在しない ID と同じ扱い）。ID は再利用しない。
// 港・船・中継倉庫にある手紙・船・航路は変えない。
//
// 呼び出し側は、配達・破棄の結果から必要な値（Letter、Raft のポインタ）を取り出し、
// ドメインイベントを作り終えたあとで呼ぶ。呼ばなければ終わった手紙を持ち続ける（メモリは増え続ける）。
// スライスの容量は縮めない。
func (t *Fleet) Reclaim() int {
	n := len(t.letters)
	t.letters = slices.DeleteFunc(t.letters, func(l Letter) bool {
		return l.Location.Kind == LocDelivered || l.Location.Kind == LocDropped
	})
	return n - len(t.letters)
}

// Port は島の港にある手紙の ID（昇順）の複製を返す。島がなければ nil。
func (t *Fleet) Port(island uint64) []LetterID {
	if !t.isIsland[island] {
		return nil
	}
	p := t.ports[island]
	if p == nil {
		return []LetterID{}
	}
	return slices.Clone(p)
}

// Relay は島の中継倉庫にある手紙の ID（昇順）の複製を返す。島がなければ nil。
func (t *Fleet) Relay(island uint64) []LetterID {
	if !t.isIsland[island] {
		return nil
	}
	r := t.relays[island]
	if r == nil {
		return []LetterID{}
	}
	return slices.Clone(r)
}

func cloneFerry(f Ferry) Ferry {
	f.Cargo = slices.Clone(f.Cargo)
	if f.Cargo == nil {
		f.Cargo = []LetterID{}
	}
	return f
}

// Ferry は船の値の複製を返す（Cargo も複製）。
func (t *Fleet) Ferry(id FerryID) (Ferry, bool) {
	i, ok := t.ferryIdx[id]
	if !ok {
		return Ferry{}, false
	}
	return cloneFerry(t.ferries[i]), true
}

// Ferries は船の複製を Config の順に返す。
func (t *Fleet) Ferries() []Ferry {
	out := make([]Ferry, len(t.ferries))
	for i, f := range t.ferries {
		out[i] = cloneFerry(f)
	}
	return out
}

// Routes は航路の複製を Config の順に返す。
func (t *Fleet) Routes() []Route { return slices.Clone(t.routes) }

// CheckInvariants は、写しの手紙・船・中継倉庫の不変条件（I-7 のうち港の規則を除く部分）を確かめ、
// 違反を 1 つ見つけたら説明つきのエラーを返す。港の規則（R1〜R6 の結果・portCap）は島の Port が確かめる。
func (t *Fleet) CheckInvariants() error {
	// 並びが崩れていると二分探索が当てにならないので、港・船より先に ID の並びを確かめる。
	for i := range t.letters {
		id := t.letters[i].ID
		if id < 1 {
			return fmt.Errorf("ferry: invariant: letter at %d has id %d", i, id)
		}
		if i > 0 && t.letters[i-1].ID >= id {
			return fmt.Errorf("ferry: invariant: letter at %d has id %d, not above %d", i, id, t.letters[i-1].ID)
		}
	}
	seen := make([]bool, len(t.letters)) // 港・船・中継倉庫の一覧に出てきた手紙（t.letters の添字）
	mark := func(id LetterID, where string) error {
		i, ok := t.index(id)
		if !ok {
			return fmt.Errorf("ferry: invariant: %s: unknown letter %d", where, id)
		}
		if seen[i] {
			return fmt.Errorf("ferry: invariant: letter %d listed twice (%s)", id, where)
		}
		seen[i] = true
		return nil
	}
	for _, island := range t.islands {
		port := t.ports[island]
		where := fmt.Sprintf("port %d", island)
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
		}
	}
	for _, island := range t.islands {
		relay := t.relays[island]
		for _, id := range relay {
			if err := mark(id, fmt.Sprintf("relay %d", island)); err != nil {
				return err
			}
		}
		if err := checkRelay(island, relay, t.letter, t.relayCap); err != nil {
			return err
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
			// 故障中に出航していない（Break は停泊中だけなので、故障が直る前に出た船はない）。
			if f.BrokenUntil != 0 && f.DepartAt < f.BrokenUntil {
				return fmt.Errorf("ferry: invariant: %s: departed at %d before broken until %d", where, f.DepartAt, f.BrokenUntil)
			}
		default:
			return fmt.Errorf("ferry: invariant: %s: unknown state %d", where, f.State)
		}
	}
	for i := range t.letters {
		l := &t.letters[i]
		switch l.Location.Kind {
		case LocPort, LocFerry, LocRelay:
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
