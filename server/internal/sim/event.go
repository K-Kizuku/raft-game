package sim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// EventType はドメインイベントの種類（プロトコル §6 の type）。
type EventType string

const (
	EvPhaseChanged    EventType = "phase.changed"
	EvLetterCreated   EventType = "letter.created"
	EvLetterMoved     EventType = "letter.moved"
	EvLetterDelivered EventType = "letter.delivered"
	EvLetterDropped   EventType = "letter.dropped"
	EvFerryDeparted   EventType = "ferry.departed"
	EvFerryArrived    EventType = "ferry.arrived"
	EvNodeState       EventType = "node.state"
	EvLeaderElected   EventType = "node.leader_elected"
	EvSteppedDown     EventType = "node.stepped_down"
	EvLedgerApplied   EventType = "ledger.applied"
)

// EventData はイベントの中身。各型は自分の EventType を返す。
// JSON の最上位のキーに seq・t・type を使わない（DomainEvent の JSON で平らに並べるため）。
type EventData interface{ EventType() EventType }

// DomainEvent はドメインイベント 1 件（プロトコル §6 の 1 要素）。Scheduler の Event とは別物。
type DomainEvent struct {
	Seq  uint64  // 1 から振る連番（Engine 全体で 1 本）
	T    SimTime // 出した時点の Now()
	Type EventType
	Data EventData
}

// MarshalJSON は {"seq":…,"t":…,"type":"…", 中身のフィールド…} の平らなオブジェクトにする（プロトコル §6）。
// キーの順序は seq・t・type、そのあと中身の構造体のフィールドの順。末尾に改行を付けない。
func (e DomainEvent) MarshalJSON() ([]byte, error) {
	inner, err := json.Marshal(e.Data)
	if err != nil {
		return nil, fmt.Errorf("sim: marshal %s: %w", e.Type, err)
	}
	if len(inner) < 2 || inner[0] != '{' || inner[len(inner)-1] != '}' {
		return nil, fmt.Errorf("sim: marshal %s: data is not a JSON object: %s", e.Type, inner)
	}
	typ, err := json.Marshal(string(e.Type))
	if err != nil {
		return nil, fmt.Errorf("sim: marshal %s: %w", e.Type, err)
	}
	var b bytes.Buffer
	b.WriteString(`{"seq":`)
	b.WriteString(strconv.FormatUint(e.Seq, 10))
	b.WriteString(`,"t":`)
	b.WriteString(strconv.FormatInt(int64(e.T), 10))
	b.WriteString(`,"type":`)
	b.Write(typ)
	if body := inner[1 : len(inner)-1]; len(bytes.TrimSpace(body)) > 0 {
		b.WriteByte(',')
		b.Write(body)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// PhaseChanged は phase.changed。
type PhaseChanged struct {
	Phase Phase `json:"phase"`
}

// LetterCreated は letter.created。
type LetterCreated struct {
	Letter LetterView `json:"letter"`
}

// LetterMoved は letter.moved。
type LetterMoved struct {
	LetterID uint64  `json:"letterId"`
	Loc      LocView `json:"loc"`
}

// LetterDelivered は letter.delivered。
type LetterDelivered struct {
	LetterID uint64 `json:"letterId"`
	IslandID uint64 `json:"islandId"`
	Accepted bool   `json:"accepted"`
}

// LetterDropped は letter.dropped。
type LetterDropped struct {
	LetterID uint64 `json:"letterId"`
	Reason   string `json:"reason"` // "superseded" | "overflow"
}

// FerryDeparted は ferry.departed。
type FerryDeparted struct {
	FerryID  string  `json:"ferryId"`
	From     uint64  `json:"from"`
	To       uint64  `json:"to"`
	DepartAt SimTime `json:"departAt"`
	ArriveAt SimTime `json:"arriveAt"`
}

// FerryArrived は ferry.arrived。
type FerryArrived struct {
	FerryID  string  `json:"ferryId"`
	IslandID uint64  `json:"islandId"`
	ReadyAt  SimTime `json:"readyAt"`
}

// NodeState は node.state。
type NodeState struct {
	ID        uint64 `json:"id"`
	Role      string `json:"role"` // "follower" | "pre_candidate" | "candidate" | "leader"
	Term      uint64 `json:"term"`
	Vote      uint64 `json:"vote"` // HardState の Vote（0 = 投票していない）
	Lead      uint64 `json:"lead"`
	Commit    uint64 `json:"commit"`
	Applied   uint64 `json:"applied"`
	LastIndex uint64 `json:"lastIndex"`
}

// LeaderElected は node.leader_elected。
type LeaderElected struct {
	ID   uint64 `json:"id"`
	Term uint64 `json:"term"`
}

// SteppedDown は node.stepped_down。reason はゲーム側の推定（プロトコル §6 の注）。
type SteppedDown struct {
	ID     uint64 `json:"id"`
	Term   uint64 `json:"term"`
	Reason string `json:"reason"` // "check_quorum" | "higher_term"
}

// LedgerApplied は ledger.applied。
type LedgerApplied struct {
	IslandID uint64 `json:"islandId"`
	Index    uint64 `json:"index"`
	ReqID    string `json:"reqId"`            // Decode できないときなどは ""（ledger.Result のまま）
	Result   string `json:"result"`           // "ok" | "rejected" | "duplicate"
	Reason   string `json:"reason,omitempty"` // rejected のときだけ
}

func (PhaseChanged) EventType() EventType    { return EvPhaseChanged }
func (LetterCreated) EventType() EventType   { return EvLetterCreated }
func (LetterMoved) EventType() EventType     { return EvLetterMoved }
func (LetterDelivered) EventType() EventType { return EvLetterDelivered }
func (LetterDropped) EventType() EventType   { return EvLetterDropped }
func (FerryDeparted) EventType() EventType   { return EvFerryDeparted }
func (FerryArrived) EventType() EventType    { return EvFerryArrived }
func (NodeState) EventType() EventType       { return EvNodeState }
func (LeaderElected) EventType() EventType   { return EvLeaderElected }
func (SteppedDown) EventType() EventType     { return EvSteppedDown }
func (LedgerApplied) EventType() EventType   { return EvLedgerApplied }

// LetterView は手紙の表示用の値（プロトコル §5.2）。
type LetterView struct {
	ID        uint64       `json:"id"`
	Kind      string       `json:"kind"` // "raft" | "request"
	From      uint64       `json:"from"`
	To        uint64       `json:"to"`
	CreatedAt SimTime      `json:"createdAt"`
	Loc       LocView      `json:"loc"`
	Label     string       `json:"label"`
	Raft      *RaftView    `json:"raft,omitempty"`
	Request   *RequestView `json:"request,omitempty"`
}

// LocView は手紙のある場所（プロトコル §5.2 の loc）。
type LocView struct {
	Type     string `json:"type"` // "port" | "ferry" | "delivered" | "dropped"
	IslandID uint64 `json:"islandId,omitempty"`
	FerryID  string `json:"ferryId,omitempty"`
}

// RaftView は Raft の手紙の中身（プロトコル §5.2 の raft）。ゲッターで読んだ値の複製。
type RaftView struct {
	Type       string      `json:"type"` // pb.MessageType の String()（例: "MsgApp"）
	Term       uint64      `json:"term"`
	LogTerm    uint64      `json:"logTerm"`
	Index      uint64      `json:"index"`
	Commit     uint64      `json:"commit"`
	Entries    []EntryView `json:"entries"` // nil にしない（空なら []）
	Reject     bool        `json:"reject"`
	RejectHint uint64      `json:"rejectHint"`
	Stale      bool        `json:"stale"`
}

// EntryView は手紙に入ったエントリ 1 つ。
type EntryView struct {
	Index   uint64 `json:"index"`
	Term    uint64 `json:"term"`
	Type    string `json:"type"`    // "normal" | "conf_change" | "conf_change_v2"
	Summary string `json:"summary"` // 通常のエントリの "<kind> <reqId>"・"invalid"。空のエントリと構成変更は ""
}

// RequestView は依頼書の中身（プロトコル §5.2 の request）。
type RequestView struct {
	ReqID string `json:"reqId"`
	Hops  int    `json:"hops"`
}

// portLoc は港の位置。
func portLoc(island uint64) LocView { return LocView{Type: "port", IslandID: island} }

// ferryLoc は船の上の位置。
func ferryLoc(id ferry.FerryID) LocView { return LocView{Type: "ferry", FerryID: string(id)} }

// newLetterView は、港に置いた時点の手紙の表示用の値を作る。loc は {port, From}。
// senderTerm は送信元のノードの今の Term（raft.stale の判定に使う）。
func newLetterView(l ferry.Letter, senderTerm uint64) LetterView {
	v := LetterView{
		ID:        uint64(l.ID),
		From:      l.From,
		To:        l.To,
		CreatedAt: SimTime(l.CreatedAt),
		Loc:       portLoc(l.From),
		Label:     letterLabel(l),
	}
	switch l.Kind {
	case ferry.LetterRequest:
		v.Kind = "request"
		v.Request = &RequestView{ReqID: l.Req.ReqID, Hops: l.Req.Hops}
	default:
		v.Kind = "raft"
		v.Raft = newRaftView(l.Raft, senderTerm)
	}
	return v
}

// newRaftView は m をゲッターで読んで表示用の値を作る。m は書き換えない。
func newRaftView(m *pb.Message, senderTerm uint64) *RaftView {
	ents := m.GetEntries()
	views := make([]EntryView, 0, len(ents))
	for _, ent := range ents {
		views = append(views, newEntryView(ent))
	}
	return &RaftView{
		Type:       m.GetType().String(),
		Term:       m.GetTerm(),
		LogTerm:    m.GetLogTerm(),
		Index:      m.GetIndex(),
		Commit:     m.GetCommit(),
		Entries:    views,
		Reject:     m.GetReject(),
		RejectHint: m.GetRejectHint(),
		Stale:      m.GetTerm() < senderTerm,
	}
}

// newEntryView はエントリ 1 つの表示用の値を作る。
func newEntryView(ent *pb.Entry) EntryView {
	typ, summary := entryTypeSummary(ent.GetType(), ent.GetData())
	return EntryView{Index: ent.GetIndex(), Term: ent.GetTerm(), Type: typ, Summary: summary}
}

// entryTypeSummary はエントリの種類と要約の文字列を返す（EntryView と NodeDetail の LogEntryDetail で共通）。
// 種類は "normal" | "conf_change" | "conf_change_v2"（知らない種類は pb.EntryType の String()）。
// 要約は、通常のエントリの "<kind> <reqId>"、読めなければ "invalid"、空のエントリと構成変更は ""。
func entryTypeSummary(t pb.EntryType, data []byte) (typ, summary string) {
	switch t {
	case pb.EntryNormal:
		typ = "normal"
		if len(data) > 0 {
			if c, err := ledger.Decode(data); err == nil {
				summary = c.Kind + " " + c.ReqID
			} else {
				summary = "invalid"
			}
		}
	case pb.EntryConfChange:
		typ = "conf_change"
	case pb.EntryConfChangeV2:
		typ = "conf_change_v2"
	default:
		typ = t.String()
	}
	return typ, summary
}

// roleString は役割の文字列（node.state の role）。pre_candidate はプロトコルにない値（設計書にない判断）。
func roleString(r raft.StateType) string {
	switch r {
	case raft.StateFollower:
		return "follower"
	case raft.StatePreCandidate:
		return "pre_candidate"
	case raft.StateCandidate:
		return "candidate"
	case raft.StateLeader:
		return "leader"
	}
	panic(fmt.Sprintf("sim: unknown raft state %d", r))
}
