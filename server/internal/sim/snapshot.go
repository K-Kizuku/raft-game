package sim

import (
	"fmt"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Snapshot は Engine の今の状態（プロトコル §5.1 のうち Engine が持つ分）。
// T-301 が protocol.Snapshot に変換する（または置き換える）。JSON はトレースに入らない。ワイヤの形は T-301 が決める。
type Snapshot struct {
	Phase    Phase          `json:"phase"`
	T        SimTime        `json:"t"`            // Now()（プロトコルでは clock.t）
	EndsAt   SimTime        `json:"endsAt"`       // Config.DurationMs（プロトコルでは clock.endsAt）
	Ferries  []FerryView    `json:"ferries"`      // Config の順
	Routes   []RouteView    `json:"routesState"`  // Config の順
	Letters  []LetterView   `json:"letters"`      // 港と船の上にある手紙。ID の昇順
	Requests []RequestState `json:"requests"`     // 発生済みの依頼。ID の順。最後に出した request.* の値。nil にしない
	Nodes    []NodeState    `json:"nodes"`        // ID の昇順。最後に出した node.state の値
	Islands  []IslandState  `json:"islandsState"` // 採点するとき全島を ID の順に。採点しないときは nil
	Score    *ScoreState    `json:"score"`        // 採点しないときは nil
	LastSeq  uint64         `json:"lastSeq"`      // 最後に出したイベントの Seq（0 = まだない）
}

// FerryView は船の状態（プロトコル §5.1 の ferries[]）。
type FerryView struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Capacity int      `json:"capacity"`
	State    string   `json:"state"`              // "docked" | "sailing"
	IslandID uint64   `json:"islandId,omitempty"` // docked のときの島。sailing のときは 0
	From     uint64   `json:"from,omitempty"`     // sailing のとき。docked のときは 0
	To       uint64   `json:"to,omitempty"`       // sailing のとき。docked のときは 0
	DepartAt SimTime  `json:"departAt"`           // sailing のとき。docked のときは 0
	ArriveAt SimTime  `json:"arriveAt"`           // sailing のとき。docked のときは 0
	ReadyAt  SimTime  `json:"readyAt"`            // docked: この時刻から出航できる。sailing: ArriveAt + DockMs
	Cargo    []uint64 `json:"cargo"`              // 積んだ順。nil にしない（空なら []）
}

// RouteView は航路の状態（プロトコル §5.1 の routesState[]）。
type RouteView struct {
	ID           string   `json:"id"`           // "A-B"（ferry.Route.ID()）
	BlockedUntil *SimTime `json:"blockedUntil"` // Now() < BlockedUntil のときだけ値を持つ。そうでなければ nil（JSON は null）
}

// Snapshot は今の状態を返す。状態・イベント・ハッシュ・入力ログを変えない。返す値は Engine と何も共有しない。
// どのフェーズでも呼べる。Seq <= LastSeq のイベントをすべて当てはめた後の状態と一致する。
//
// cmd.game.start の前（PhaseIntro）は、まだ node.state を出していないので、全ノードが follower で値は 0
// （設計書にない判断。開会式の結果は cmd.game.start のイベントで初めて見せる）。
func (e *Engine) Snapshot() Snapshot {
	now := e.Now()
	ferries := e.tr.Ferries()
	s := Snapshot{
		Phase:    e.phase,
		T:        now,
		EndsAt:   e.cfg.DurationMs,
		Ferries:  make([]FerryView, 0, len(ferries)),
		Routes:   []RouteView{},
		Letters:  []LetterView{},
		Requests: e.requestStates(),
		Nodes:    make([]NodeState, 0, len(e.last)),
		Islands:  e.islandStates(),
		Score:    e.scoreState(),
		LastSeq:  e.seq,
	}

	var ids []ferry.LetterID
	for island := uint64(1); island <= uint64(e.cfg.Islands); island++ {
		ids = append(ids, e.tr.Port(island)...)
	}
	for _, f := range ferries {
		s.Ferries = append(s.Ferries, e.ferryView(f))
		ids = append(ids, f.Cargo...)
	}

	for _, r := range e.tr.Routes() {
		v := RouteView{ID: r.ID()}
		if int64(now) < r.BlockedUntil {
			until := SimTime(r.BlockedUntil)
			v.BlockedUntil = &until
		}
		s.Routes = append(s.Routes, v)
	}

	slices.Sort(ids)
	for _, lid := range ids {
		l, ok := e.tr.Letter(lid)
		if !ok {
			panic(fmt.Sprintf("sim: snapshot: letter %d not found", lid))
		}
		v := newLetterView(l, e.staleTerm[l.From-1])
		switch l.Location.Kind {
		case ferry.LocPort:
			v.Loc = portLoc(l.Location.Island)
		case ferry.LocFerry:
			v.Loc = ferryLoc(l.Location.Ferry)
		default:
			panic(fmt.Sprintf("sim: snapshot: letter %d in port or ferry is at %+v", lid, l.Location))
		}
		s.Letters = append(s.Letters, v)
	}

	for i, v := range e.last {
		s.Nodes = append(s.Nodes, NodeState{
			ID:        uint64(i + 1),
			Role:      roleString(v.role),
			Term:      v.term,
			Vote:      v.vote,
			Lead:      v.lead,
			Commit:    v.commit,
			Applied:   v.applied,
			LastIndex: v.lastIndex,
		})
	}
	return s
}

// ferryView は船の表示用の値を作る。f は Transport が返した複製。
func (e *Engine) ferryView(f ferry.Ferry) FerryView {
	v := FerryView{
		ID:       string(f.ID),
		Name:     f.Name,
		Capacity: f.Capacity,
		Cargo:    make([]uint64, len(f.Cargo)),
	}
	for i, lid := range f.Cargo {
		v.Cargo[i] = uint64(lid)
	}
	switch f.State {
	case ferry.Docked:
		v.State = "docked"
		v.IslandID = f.At
		v.ReadyAt = SimTime(f.ReadyAt)
	case ferry.Sailing:
		v.State = "sailing"
		v.From = f.From
		v.To = f.To
		v.DepartAt = SimTime(f.DepartAt)
		v.ArriveAt = SimTime(f.ArriveAt)
		// 着いてから出航できる時刻（プロトコル §5.1 の例の readyAt）。
		v.ReadyAt = SimTime(f.ArriveAt + e.cfg.DockMs)
	default:
		panic(fmt.Sprintf("sim: snapshot: ferry %q has unknown state %d", f.ID, f.State))
	}
	return v
}
