package ferry

import (
	"errors"
	"fmt"
	"slices"

	pb "go.etcd.io/raft/v3/raftpb"
)

// エラー。fmt.Errorf("%w: …") で詳細を添えて返すので、errors.Is で判定する。
var (
	ErrUnknownIsland    = errors.New("ferry: unknown island")
	ErrUnknownFerry     = errors.New("ferry: unknown ferry")
	ErrBadMessage       = errors.New("ferry: bad message")
	ErrInvalidArgument  = errors.New("ferry: invalid argument")
	ErrNotDocked        = errors.New("ferry: ferry is not docked")
	ErrNotReady         = errors.New("ferry: ferry is not ready to depart")
	ErrCapacityExceeded = errors.New("ferry: capacity exceeded")
	ErrLetterNotHere    = errors.New("ferry: letter is not here")
	ErrNoRoute          = errors.New("ferry: no route")
	ErrRouteBlocked     = errors.New("ferry: route is blocked")
	ErrRelayFull        = errors.New("ferry: relay is full")
	ErrNotArriving      = errors.New("ferry: ferry is not arriving now")
	ErrBroken           = errors.New("ferry: ferry is broken")
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
	RelayCap      int   // transport.relayCap（島ごとの中継倉庫の上限。0 = 積み替えなし）
}

// Transport は港・船・航路・手紙の状態を 1 つにまとめたもの（単一のプロセスでの使い方）。
// 航路・船・手紙の所在の写しは Fleet（埋め込み）、各島の港と中継倉庫は Port が持ち、Transport はその間を
// つなぐ（DTN-00 #363 で分けた。sim.Engine は Fleet と各島の Port を直接使う）。手紙の ID は全体で 1 本の連番。
// 1 つの goroutine から使う。
type Transport struct {
	*Fleet
	islandPorts   map[uint64]*Port // 島 → 港。検索だけに使い、走査しない
	lastID        LetterID         // 最後に振った ID（0 = まだない）。Reclaim しても戻さない
	portCap       int
	perLinkAppCap int
}

// New は設定を検査して Transport を作る。
func New(cfg Config) (*Transport, error) {
	fl, err := NewFleet(FleetConfig{
		Islands:  cfg.Islands,
		Routes:   cfg.Routes,
		Ferries:  cfg.Ferries,
		DockMs:   cfg.DockMs,
		RelayCap: cfg.RelayCap,
	})
	if err != nil {
		return nil, err
	}
	t := &Transport{
		Fleet:         fl,
		islandPorts:   make(map[uint64]*Port, len(cfg.Islands)),
		portCap:       cfg.PortCap,
		perLinkAppCap: cfg.PerLinkAppCap,
	}
	for _, id := range cfg.Islands {
		p, err := NewPort(PortConfig{
			Island:        id,
			Islands:       cfg.Islands,
			PortCap:       cfg.PortCap,
			PerLinkAppCap: cfg.PerLinkAppCap,
			RelayCap:      cfg.RelayCap,
			NewID:         t.nextID,
		})
		if err != nil {
			return nil, err
		}
		t.islandPorts[id] = p
	}
	return t, nil
}

func (t *Transport) nextID() LetterID {
	t.lastID++
	return t.lastID
}

// newLetter は、港の規則を通さずに手紙 l を写しに加える（テストが不変条件を壊すのに使う）。
func (t *Transport) newLetter(l Letter) LetterID {
	l.ID = t.nextID()
	t.letters = append(t.letters, l)
	return l.ID
}

// track は Port が作った手紙と破棄を写しに反映する。
func (t *Transport) track(created []Letter, dropped []Drop) {
	for _, l := range created {
		if err := t.Track(l); err != nil {
			panic(fmt.Sprintf("ferry: track: %v", err))
		}
	}
	t.markDropped(dropped)
}

func (t *Transport) markDropped(dropped []Drop) {
	for _, d := range dropped {
		if err := t.MarkDropped(d.Letter); err != nil {
			panic(fmt.Sprintf("ferry: mark dropped: %v", err))
		}
	}
}

// PostRaft は Raft のメッセージを順に手紙にして from の港に入れる。
// 先にすべてを検査し、誤りがあれば何も変えずにエラーを返す。
// m は書き換えず、ポインタをそのまま持つ。
func (t *Transport) PostRaft(now int64, from uint64, msgs []*pb.Message) (created []LetterID, dropped []Drop, err error) {
	p, ok := t.islandPorts[from]
	if !ok {
		return nil, nil, fmt.Errorf("%w: %d", ErrUnknownIsland, from)
	}
	letters, dropped, err := p.PostRaft(now, msgs)
	if err != nil {
		return nil, nil, err
	}
	t.track(letters, dropped)
	for _, l := range letters {
		created = append(created, l.ID)
	}
	return created, dropped, nil
}

// PostRequest は依頼書を 1 通作って from の港に入れる。
func (t *Transport) PostRequest(now int64, from, to uint64, note RequestNote) (id LetterID, dropped []Drop, err error) {
	p, ok := t.islandPorts[from]
	if !ok {
		return 0, nil, fmt.Errorf("%w: from %d", ErrUnknownIsland, from)
	}
	l, dropped, err := p.PostRequest(now, to, note)
	if err != nil {
		return 0, nil, err
	}
	t.track([]Letter{l}, dropped)
	return l.ID, dropped, nil
}

// Load は停泊中の船に、今いる島の港の手紙を積む。1 通でも積めなければ 1 通も積まない。
func (t *Transport) Load(id FerryID, letters []LetterID) error {
	f, ok := t.Ferry(id)
	if err := t.Fleet.Load(id, letters); err != nil {
		return err
	}
	if _, err := t.islandPorts[f.At].Take(letters); !ok || err != nil {
		panic(fmt.Sprintf("ferry: load: port %d: %v", f.At, err))
	}
	return nil
}

// Unload は停泊中の船から手紙を降ろす。ID の昇順に、送信元が今いる島の手紙はその島の港に戻し
// （規則 R1〜R6 を適用する）、そうでない手紙（経由の手紙）はその島の中継倉庫に預ける（合流・破棄しない）。
// 預けると倉庫が relayCap 通を超えるなら ErrRelayFull。1 通でも降ろせなければ 1 通も降ろさない。
// 返す dropped は港へ戻した手紙の破棄だけ（倉庫では破棄は起きない）。
func (t *Transport) Unload(id FerryID, letters []LetterID) (dropped []Drop, err error) {
	unloaded, err := t.Fleet.Unload(id, letters)
	if err != nil {
		return nil, err
	}
	for _, l := range unloaded {
		p := t.islandPorts[l.Location.Island]
		if l.Location.Kind == LocPort {
			ds, err := p.Return(l)
			if err != nil {
				panic(fmt.Sprintf("ferry: unload: %v", err))
			}
			t.markDropped(ds)
			dropped = append(dropped, ds...)
			continue
		}
		if err := p.Store(l); err != nil {
			panic(fmt.Sprintf("ferry: unload: %v", err))
		}
	}
	return dropped, nil
}

// RelayLoad は停泊中の船に、今いる島の中継倉庫の手紙を渡した順に積む（どの船でもよい）。
// 1 通でも積めなければ 1 通も積まない。
func (t *Transport) RelayLoad(id FerryID, letters []LetterID) error {
	f, ok := t.Ferry(id)
	if err := t.Fleet.RelayLoad(id, letters); err != nil {
		return err
	}
	if _, err := t.islandPorts[f.At].TakeRelay(letters); !ok || err != nil {
		panic(fmt.Sprintf("ferry: relay load: relay %d: %v", f.At, err))
	}
	return nil
}

// CheckInvariants は港・船・手紙の不変条件（V-7・I-7）を確かめ、違反を 1 つ見つけたら
// 説明つきのエラーを返す。
func (t *Transport) CheckInvariants() error {
	for i := range t.letters {
		if id := t.letters[i].ID; id > t.lastID {
			return fmt.Errorf("ferry: invariant: letter at %d has id %d (last id %d)", i, id, t.lastID)
		}
	}
	if err := t.Fleet.CheckInvariants(); err != nil {
		return err
	}
	for _, island := range t.islands {
		if err := checkPortRules(island, t.ports[island], t.letter, t.portCap, t.perLinkAppCap); err != nil {
			return err
		}
		p := t.islandPorts[island]
		if err := p.CheckInvariants(); err != nil {
			return err
		}
		if err := CheckPortMirror(t.Fleet, p); err != nil {
			return err
		}
	}
	return nil
}

// CheckPortMirror は、Fleet の写しの island の港・中継倉庫の手紙の ID の並びが、島の Port と一致するかを確かめる。
func CheckPortMirror(fl *Fleet, p *Port) error {
	island := p.Island()
	if got, want := fl.ports[island], p.port; !slices.Equal(got, want) {
		return fmt.Errorf("ferry: invariant: port %d: mirror %v, port %v", island, got, want)
	}
	if got, want := fl.relays[island], p.relay; !slices.Equal(got, want) {
		return fmt.Errorf("ferry: invariant: relay %d: mirror %v, relay %v", island, got, want)
	}
	return nil
}
