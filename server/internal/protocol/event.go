package protocol

import "encoding/json"

// イベント（§6。events.events[] の要素）。
//
// JSON のキーの順は seq・t・type、そのあと中身（sim.DomainEvent.MarshalJSON と同じ）。
// そのため各イベントの型は EventHeader を先頭に埋め込み、その後に Type、そのあとに中身のフィールドを置く。
// セッションループは sim.DomainEvent の JSON をそのまま送ってよい。これらの型は TS の生成と JSON の形の検査に使う。

// EventHeader はすべてのイベントに共通のフィールド（type は各イベントの型が持つ）。
type EventHeader struct {
	Seq uint64 `json:"seq"`
	T   int64  `json:"t"`
}

// EventType はイベントの種類（§6 の type）。
type EventType string

const (
	EventTypePhaseChanged    EventType = "phase.changed"
	EventTypeLetterCreated   EventType = "letter.created"
	EventTypeLetterMoved     EventType = "letter.moved"
	EventTypeLetterDelivered EventType = "letter.delivered"
	EventTypeLetterDropped   EventType = "letter.dropped"
	EventTypeLetterStale     EventType = "letter.stale"
	EventTypeFerryDeparted   EventType = "ferry.departed"
	EventTypeFerryArrived    EventType = "ferry.arrived"
	EventTypeFerryBroken     EventType = "ferry.broken"
	EventTypeFerryRepaired   EventType = "ferry.repaired"
	EventTypeRouteBlocked    EventType = "route.blocked"
	EventTypeRouteUnblocked  EventType = "route.unblocked"
	EventTypeForecast        EventType = "forecast"
	EventTypeNodeState       EventType = "node.state"
	EventTypeLeaderElected   EventType = "node.leader_elected"
	EventTypeSteppedDown     EventType = "node.stepped_down"
	EventTypeRequestCreated  EventType = "request.created"
	EventTypeRequestUpdated  EventType = "request.updated"
	EventTypeLedgerApplied   EventType = "ledger.applied"
	EventTypeIslandUpdated   EventType = "island.updated"
	EventTypeScoreUpdated    EventType = "score.updated"
	EventTypeNotice          EventType = "notice"
)

// EventTypes はイベントの種類をすべて、定義の順（§6 の表の順）に返す。呼ぶたびに新しいスライスを返す。
func EventTypes() []EventType {
	return []EventType{
		EventTypePhaseChanged,
		EventTypeLetterCreated,
		EventTypeLetterMoved,
		EventTypeLetterDelivered,
		EventTypeLetterDropped,
		EventTypeLetterStale,
		EventTypeFerryDeparted,
		EventTypeFerryArrived,
		EventTypeFerryBroken,
		EventTypeFerryRepaired,
		EventTypeRouteBlocked,
		EventTypeRouteUnblocked,
		EventTypeForecast,
		EventTypeNodeState,
		EventTypeLeaderElected,
		EventTypeSteppedDown,
		EventTypeRequestCreated,
		EventTypeRequestUpdated,
		EventTypeLedgerApplied,
		EventTypeIslandUpdated,
		EventTypeScoreUpdated,
		EventTypeNotice,
	}
}

// NewEvent は t の型の新しいゼロ値へのポインタを返す。知らない t なら (nil, false)。
func NewEvent(t EventType) (any, bool) {
	switch t {
	case EventTypePhaseChanged:
		return &PhaseChangedEvent{}, true
	case EventTypeLetterCreated:
		return &LetterCreatedEvent{}, true
	case EventTypeLetterMoved:
		return &LetterMovedEvent{}, true
	case EventTypeLetterDelivered:
		return &LetterDeliveredEvent{}, true
	case EventTypeLetterDropped:
		return &LetterDroppedEvent{}, true
	case EventTypeLetterStale:
		return &LetterStaleEvent{}, true
	case EventTypeFerryDeparted:
		return &FerryDepartedEvent{}, true
	case EventTypeFerryArrived:
		return &FerryArrivedEvent{}, true
	case EventTypeFerryBroken:
		return &FerryBrokenEvent{}, true
	case EventTypeFerryRepaired:
		return &FerryRepairedEvent{}, true
	case EventTypeRouteBlocked:
		return &RouteBlockedEvent{}, true
	case EventTypeRouteUnblocked:
		return &RouteUnblockedEvent{}, true
	case EventTypeForecast:
		return &ForecastEvent{}, true
	case EventTypeNodeState:
		return &NodeStateEvent{}, true
	case EventTypeLeaderElected:
		return &LeaderElectedEvent{}, true
	case EventTypeSteppedDown:
		return &SteppedDownEvent{}, true
	case EventTypeRequestCreated:
		return &RequestCreatedEvent{}, true
	case EventTypeRequestUpdated:
		return &RequestUpdatedEvent{}, true
	case EventTypeLedgerApplied:
		return &LedgerAppliedEvent{}, true
	case EventTypeIslandUpdated:
		return &IslandUpdatedEvent{}, true
	case EventTypeScoreUpdated:
		return &ScoreUpdatedEvent{}, true
	case EventTypeNotice:
		return &NoticeEvent{}, true
	}
	return nil, false
}

// PhaseChangedEvent は phase.changed（開会式・航行・終了）。
type PhaseChangedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypePhaseChanged"`
	Phase       Phase     `json:"phase"`
}

// LetterCreatedEvent は letter.created（Raft が出力した・依頼書を書いた）。
type LetterCreatedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeLetterCreated"`
	Letter      Letter    `json:"letter"`
}

// LetterMovedEvent は letter.moved（積む・降ろす・預ける）。
type LetterMovedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeLetterMoved"`
	LetterID    uint64    `json:"letterId"`
	Loc         Loc       `json:"loc"`
}

// LetterDeliveredEvent は letter.delivered（船が着いて配達した）。
type LetterDeliveredEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeLetterDelivered"`
	LetterID    uint64    `json:"letterId"`
	IslandID    uint64    `json:"islandId"`
	Accepted    bool      `json:"accepted"`
}

// LetterDroppedEvent は letter.dropped（港の規則で処分した）。
type LetterDroppedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType  `json:"type" tstype:"typeof EventTypeLetterDropped"`
	LetterID    uint64     `json:"letterId"`
	Reason      DropReason `json:"reason"`
}

// LetterStaleEvent は letter.stale（送信元の Term が上がり、手紙が古くなった）。
type LetterStaleEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeLetterStale"`
	LetterID    uint64    `json:"letterId"`
}

// FerryDepartedEvent は ferry.departed（出航）。
type FerryDepartedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeFerryDeparted"`
	FerryID     string    `json:"ferryId"`
	From        uint64    `json:"from"`
	To          uint64    `json:"to"`
	DepartAt    int64     `json:"departAt"`
	ArriveAt    int64     `json:"arriveAt"`
}

// FerryArrivedEvent は ferry.arrived（到着）。
type FerryArrivedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeFerryArrived"`
	FerryID     string    `json:"ferryId"`
	IslandID    uint64    `json:"islandId"`
	ReadyAt     int64     `json:"readyAt"`
}

// FerryBrokenEvent は ferry.broken（故障。P1）。
type FerryBrokenEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeFerryBroken"`
	FerryID     string    `json:"ferryId"`
	Until       int64     `json:"until"`
}

// FerryRepairedEvent は ferry.repaired（修理。P1）。
type FerryRepairedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeFerryRepaired"`
	FerryID     string    `json:"ferryId"`
	Until       int64     `json:"until"`
}

// RouteBlockedEvent は route.blocked（嵐）。
type RouteBlockedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeRouteBlocked"`
	RouteIDs    []string  `json:"routeIds"`
	Until       int64     `json:"until"`
}

// RouteUnblockedEvent は route.unblocked（嵐が去った）。
type RouteUnblockedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeRouteUnblocked"`
	RouteIDs    []string  `json:"routeIds"`
	Until       int64     `json:"until"`
}

// ForecastEvent は forecast（嵐の予報）。
type ForecastEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeForecast"`
	Event       string    `json:"event"`
	At          int64     `json:"at"`
	RouteIDs    []string  `json:"routeIds"`
}

// NodeStateEvent は node.state（いずれかの値が変わった）。
type NodeStateEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeNodeState"`
	NodeSummary `tstype:",extends"`
}

// LeaderElectedEvent は node.leader_elected（新しい Leader が誕生した）。
type LeaderElectedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeLeaderElected"`
	ID          uint64    `json:"id"`
	Term        uint64    `json:"term"`
}

// SteppedDownEvent は node.stepped_down（Leader でなくなった）。
type SteppedDownEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType      `json:"type" tstype:"typeof EventTypeSteppedDown"`
	ID          uint64         `json:"id"`
	Term        uint64         `json:"term"`
	Reason      StepDownReason `json:"reason"`
}

// RequestCreatedEvent は request.created（依頼の発生）。
type RequestCreatedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType `json:"type" tstype:"typeof EventTypeRequestCreated"`
	Request     Request   `json:"request"`
}

// RequestUpdatedEvent は request.updated（依頼の状態の変化）。
type RequestUpdatedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType     `json:"type" tstype:"typeof EventTypeRequestUpdated"`
	ID          string        `json:"id"`
	Status      RequestStatus `json:"status"`
	Expired     bool          `json:"expired"`
	Result      *string       `json:"result" tstype:"string | null,required"`
}

// LedgerAppliedEvent は ledger.applied（台帳への適用）。reason は rejected のときだけ。
type LedgerAppliedEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType    `json:"type" tstype:"typeof EventTypeLedgerApplied"`
	IslandID    uint64       `json:"islandId"`
	Index       uint64       `json:"index"`
	ReqID       string       `json:"reqId"`
	Result      LedgerResult `json:"result"`
	Reason      string       `json:"reason,omitempty"`
}

// IslandUpdatedEvent は island.updated（満足度の変化）。
type IslandUpdatedEvent struct {
	EventHeader     `tstype:",extends"`
	Type            EventType `json:"type" tstype:"typeof EventTypeIslandUpdated"`
	ID              uint64    `json:"id"`
	Satisfaction    int       `json:"satisfaction"`
	LastDeliveredAt *int64    `json:"lastDeliveredAt" tstype:"number | null,required"`
}

// ScoreUpdatedEvent は score.updated（スコアの変化）。
type ScoreUpdatedEvent struct {
	EventHeader     `tstype:",extends"`
	Type            EventType `json:"type" tstype:"typeof EventTypeScoreUpdated"`
	Total           int       `json:"total"`
	Successes       int       `json:"successes"`
	CriticalSecured []string  `json:"criticalSecured"`
}

// NoticeEvent は notice（島のキャラクターの通知。文言はクライアントが key から組み立てる）。
type NoticeEvent struct {
	EventHeader `tstype:",extends"`
	Type        EventType                  `json:"type" tstype:"typeof EventTypeNotice"`
	IslandID    uint64                     `json:"islandId"`
	Key         string                     `json:"key"`
	Params      map[string]json.RawMessage `json:"params"`
}
