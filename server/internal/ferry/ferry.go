package ferry

import (
	"fmt"
	"slices"
)

// FerryID はステージ定義の ferries[].id（例: "f1"）。
type FerryID string

// FerryState は船の状態。
type FerryState uint8

const (
	Docked FerryState = iota + 1
	Sailing
)

// Ferry は船 1 隻。
type Ferry struct {
	ID            FerryID
	Name          string
	Capacity      int
	SpeedPermille int64 // 航行時間の倍率の千分率（1000 = 航路の標準時間、1200 = 1.2 倍の時間がかかる）
	State         FerryState
	At            uint64     // Docked のときの島。Sailing のときは 0
	From, To      uint64     // Sailing のとき。Docked のときは 0
	DepartAt      int64      // Sailing のとき。Docked のときは 0
	ArriveAt      int64      // Sailing のとき。Docked のときは 0
	ReadyAt       int64      // Docked のとき、この時刻から出航できる
	Cargo         []LetterID // 積んだ順
}

// TravelMs は round(routeMs × speedPermille / 1000) を整数で計算する（0.5 は切り上げ）。
// 結果が 1 未満なら 1 を返す。
func TravelMs(routeMs, speedPermille int64) int64 {
	v := (routeMs*speedPermille + 500) / 1000
	if v < 1 {
		return 1
	}
	return v
}

func (t *Transport) ferry(id FerryID) (*Ferry, error) {
	i, ok := t.ferryIdx[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFerry, id)
	}
	return &t.ferries[i], nil
}

func checkIDs(letters []LetterID) error {
	if len(letters) == 0 {
		return fmt.Errorf("%w: no letters", ErrInvalidArgument)
	}
	seen := make(map[LetterID]bool, len(letters))
	for _, id := range letters {
		if seen[id] {
			return fmt.Errorf("%w: duplicate letter %d", ErrInvalidArgument, id)
		}
		seen[id] = true
	}
	return nil
}

// Load は停泊中の船に、今いる島の港の手紙を積む。1 通でも積めなければ 1 通も積まない。
func (t *Transport) Load(id FerryID, letters []LetterID) error {
	f, err := t.ferry(id)
	if err != nil {
		return err
	}
	if f.State != Docked {
		return fmt.Errorf("%w: %q", ErrNotDocked, id)
	}
	if err := checkIDs(letters); err != nil {
		return err
	}
	here := Location{Kind: LocPort, Island: f.At}
	for _, lid := range letters {
		if l := t.letter(lid); l == nil || l.Location != here {
			return fmt.Errorf("%w: letter %d is not in port %d", ErrLetterNotHere, lid, f.At)
		}
	}
	if len(f.Cargo)+len(letters) > f.Capacity {
		return fmt.Errorf("%w: %q has %d of %d, loading %d", ErrCapacityExceeded, id, len(f.Cargo), f.Capacity, len(letters))
	}
	for _, lid := range letters {
		t.removeFromPort(f.At, lid)
		t.letter(lid).Location = Location{Kind: LocFerry, Ferry: id}
		f.Cargo = append(f.Cargo, lid)
	}
	return nil
}

// Unload は停泊中の船から手紙を降ろし、ID の昇順に今いる島の港に入れる（規則 R1〜R6 を適用する）。
// 1 通でも降ろせなければ 1 通も降ろさない。
func (t *Transport) Unload(id FerryID, letters []LetterID) (dropped []Drop, err error) {
	f, err := t.ferry(id)
	if err != nil {
		return nil, err
	}
	if f.State != Docked {
		return nil, fmt.Errorf("%w: %q", ErrNotDocked, id)
	}
	if err := checkIDs(letters); err != nil {
		return nil, err
	}
	for _, lid := range letters {
		if !slices.Contains(f.Cargo, lid) {
			return nil, fmt.Errorf("%w: letter %d is not on %q", ErrLetterNotHere, lid, id)
		}
	}
	for _, lid := range letters {
		if l := t.letter(lid); l.From != f.At {
			return nil, fmt.Errorf("%w: letter %d is from %d, ferry %q is at %d", ErrRelayNotSupported, lid, l.From, id, f.At)
		}
	}
	f.Cargo = slices.DeleteFunc(f.Cargo, func(x LetterID) bool { return slices.Contains(letters, x) })
	sorted := slices.Clone(letters)
	slices.Sort(sorted)
	for _, lid := range sorted {
		dropped = t.enqueue(f.At, lid, dropped)
	}
	return dropped, nil
}

// Depart は停泊中の船を to に向けて出航させ、到着の時刻を返す。
func (t *Transport) Depart(now int64, id FerryID, to uint64) (arriveAt int64, err error) {
	f, err := t.ferry(id)
	if err != nil {
		return 0, err
	}
	if f.State != Docked {
		return 0, fmt.Errorf("%w: %q", ErrNotDocked, id)
	}
	if now < f.ReadyAt {
		return 0, fmt.Errorf("%w: %q is ready at %d, now %d", ErrNotReady, id, f.ReadyAt, now)
	}
	ri, ok := t.route(f.At, to)
	if !t.isIsland[to] || to == f.At || !ok {
		return 0, fmt.Errorf("%w: %d-%d", ErrNoRoute, f.At, to)
	}
	r := t.routes[ri]
	if r.blocked(now) {
		return 0, fmt.Errorf("%w: %s until %d", ErrRouteBlocked, r.ID(), r.BlockedUntil)
	}
	f.State = Sailing
	f.From, f.To, f.At = f.At, to, 0
	f.DepartAt = now
	f.ArriveAt = now + TravelMs(r.TravelMs, f.SpeedPermille)
	return f.ArriveAt, nil
}

// Arrive は航行中の船を ArriveAt ちょうどの時刻に到着させ、宛先がその島の手紙を配達する。
// 配達した手紙の ID を積んだ順に返す。宛先が別の島の手紙は積んだまま（経由輸送）。
func (t *Transport) Arrive(now int64, id FerryID) (delivered []LetterID, err error) {
	f, err := t.ferry(id)
	if err != nil {
		return nil, err
	}
	if f.State != Sailing || now != f.ArriveAt {
		return nil, fmt.Errorf("%w: %q (state %d, arriveAt %d, now %d)", ErrNotArriving, id, f.State, f.ArriveAt, now)
	}
	at := f.To
	f.State = Docked
	f.At = at
	f.From, f.To, f.DepartAt, f.ArriveAt = 0, 0, 0, 0
	f.ReadyAt = now + t.dockMs
	kept := f.Cargo[:0]
	for _, lid := range f.Cargo {
		l := t.letter(lid)
		if l.To == at {
			l.Location = Location{Kind: LocDelivered, Island: at}
			delivered = append(delivered, lid)
			continue
		}
		kept = append(kept, lid)
	}
	f.Cargo = kept
	return delivered, nil
}
