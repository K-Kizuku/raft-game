package protocol

import "encoding/json"

// サーバー → クライアントの payload（§5）と、その中の型（§5.1〜§5.5・§7）。
//
// Letter・Loc・RaftInfo・LetterEntry・RequestRef・Ferry・RouteState・NodeSummary は、sim の対応する型
// （LetterView・LocView・RaftView・EntryView・RequestView・FerryView・RouteView・NodeState）と
// JSON が一致するようにする（json のタグ・omitempty・フィールドの順を同じにする）。simconv のテストで確かめる。

// Phase はラウンドのフェーズ。intro は cmd.game.start の前（ステージ紹介）。
type Phase string

const (
	PhaseIntro   Phase = "intro"
	PhaseOpening Phase = "opening"
	PhaseSailing Phase = "sailing"
	PhaseEnded   Phase = "ended"
)

// Role は島（Raft のノード）の役割。
type Role string

const (
	RoleFollower     Role = "follower"
	RolePreCandidate Role = "pre_candidate"
	RoleCandidate    Role = "candidate"
	RoleLeader       Role = "leader"
)

// LetterKind は手紙の種類（§5.2 の kind）。
type LetterKind string

const (
	LetterKindRaft    LetterKind = "raft"
	LetterKindRequest LetterKind = "request"
)

// LocType は手紙のある場所の種類（§5.2 の loc.type）。
type LocType string

const (
	LocTypePort      LocType = "port"
	LocTypeFerry     LocType = "ferry"
	LocTypeRelay     LocType = "relay"
	LocTypeDelivered LocType = "delivered"
	LocTypeDropped   LocType = "dropped"
)

// LetterLabel はゲーム上の手紙の区分（§5.2 の label）。
type LetterLabel string

const (
	LetterLabelHeartbeat     LetterLabel = "heartbeat"
	LetterLabelHeartbeatResp LetterLabel = "heartbeat_resp"
	LetterLabelAppend        LetterLabel = "append"
	LetterLabelAppendProbe   LetterLabel = "append_probe"
	LetterLabelAppendResp    LetterLabel = "append_resp"
	LetterLabelAppendReject  LetterLabel = "append_reject"
	LetterLabelPrevote       LetterLabel = "prevote"
	LetterLabelPrevoteResp   LetterLabel = "prevote_resp"
	LetterLabelVote          LetterLabel = "vote"
	LetterLabelVoteResp      LetterLabel = "vote_resp"
	LetterLabelRequest       LetterLabel = "request"
	LetterLabelOther         LetterLabel = "other"
)

// EntryType は Raft のエントリの種類。
type EntryType string

const (
	EntryTypeNormal       EntryType = "normal"
	EntryTypeConfChange   EntryType = "conf_change"
	EntryTypeConfChangeV2 EntryType = "conf_change_v2"
)

// FerryState は船の状態（§5.1 の ferries[].state）。
type FerryState string

const (
	FerryStateDocked  FerryState = "docked"
	FerryStateSailing FerryState = "sailing"
)

// DropReason は手紙を処分した理由（letter.dropped の reason）。
type DropReason string

const (
	DropReasonSuperseded DropReason = "superseded"
	DropReasonOverflow   DropReason = "overflow"
)

// StepDownReason は Leader でなくなった理由の推定（node.stepped_down の reason）。
type StepDownReason string

const (
	StepDownReasonCheckQuorum StepDownReason = "check_quorum"
	StepDownReasonHigherTerm  StepDownReason = "higher_term"
)

// LedgerResult は台帳への適用の結果（ledger.applied の result）。
type LedgerResult string

const (
	LedgerResultOK        LedgerResult = "ok"
	LedgerResultRejected  LedgerResult = "rejected"
	LedgerResultDuplicate LedgerResult = "duplicate"
)

// RequestStatus は依頼の状態（§5.3 の status）。
type RequestStatus string

const (
	RequestStatusReceived  RequestStatus = "received"
	RequestStatusWaiting   RequestStatus = "waiting"
	RequestStatusInTransit RequestStatus = "in_transit"
	RequestStatusProposed  RequestStatus = "proposed"
	RequestStatusCommitted RequestStatus = "committed"
	RequestStatusSucceeded RequestStatus = "succeeded"
	RequestStatusRejected  RequestStatus = "rejected"
)

// Provenance は値の出典の区分（データ仕様 §1）。
type Provenance string

const (
	ProvenanceFact     Provenance = "fact"
	ProvenanceDerived  Provenance = "derived"
	ProvenanceCreative Provenance = "creative"
)

// ProgressState は Leader から見た Follower の複製の状態（§5.4 の progress[].state）。
type ProgressState string

const (
	ProgressStateProbe     ProgressState = "probe"
	ProgressStateReplicate ProgressState = "replicate"
	ProgressStateSnapshot  ProgressState = "snapshot"
)

// Clock はゲーム内の時計（§7）。
type Clock struct {
	T      int64   `json:"t"`
	Speed  float64 `json:"speed"`
	Paused bool    `json:"paused"`
	EndsAt int64   `json:"endsAt"`
}

// SnapshotClock は Snapshot の clock（Clock に allowedSpeeds を足したもの）。
type SnapshotClock struct {
	Clock         `tstype:",extends"`
	AllowedSpeeds []float64 `json:"allowedSpeeds"`
}

// Session は Snapshot の session。
type Session struct {
	ID       string `json:"id"`
	StageID  string `json:"stageId"`
	Seed     uint64 `json:"seed"`
	Demo     bool   `json:"demo"`
	Restored bool   `json:"restored"`
	// StageHash はステージ定義のハッシュ（stage.Stage.Hash。"sha256:…"）。DO が保存して入力ログの stageHash に入れる。
	// Go は常に送る（JSON は omitempty にしない）。yaml のタグは tygo に TS の型で省略可能（?）と書かせるためだけのもので、
	// protocol を YAML にすることはない。既存の web の Session の例（web/src/net・state・protocol のテスト）を壊さないため（T-302）。
	StageHash string `json:"stageHash" yaml:"stageHash,omitempty"`
	// Build はコンテナのビルドの識別子（/healthz の build と同じ）。DO が保存して入力ログの build に入れる。
	Build string `json:"build" yaml:"build,omitempty"`
}

// StageView は Snapshot の stage（§5.1）。
type StageView struct {
	Title     string         `json:"title"`
	Islands   []Island       `json:"islands"`
	Routes    []Route        `json:"routes"`
	Resources []Resource     `json:"resources"`
	DemoStorm *DemoStormView `json:"demoStorm,omitempty"` // デモの嵐（ステージ定義の demo.storm）。書いていなければ省く（T-305b）
}

// DemoStormView はデモの嵐の表示用の値（§5.1）。発表者の操作（demo.trigger）で遮断する航路と、嵐の紹介の文。
type DemoStormView struct {
	RouteIDs []string   `json:"routeIds"`                                  // 遮断する航路の ID（§1 の形。定義の順）。nil にしない
	Flavor   *StageText `json:"flavor" tstype:"StageText | null,required"` // 紹介の文（demo.storm.flavor）。なければ null
}

// Island はステージの島 1 つ。
type Island struct {
	ID           uint64       `json:"id"`
	Name         string       `json:"name"`
	Municipality string       `json:"municipality"`
	Layout       IslandLayout `json:"layout"`
	Population   Population   `json:"population"`
	Color        string       `json:"color"`
}

// IslandLayout は島の描画の位置。
type IslandLayout struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Population は島の人口と出典（データ仕様 §3）。
type Population struct {
	Value      int        `json:"value"`
	AsOf       string     `json:"asOf"`
	Provenance Provenance `json:"provenance"`
	Note       string     `json:"note,omitempty"`
	Source     string     `json:"source"`
}

// Route はステージの航路 1 つ。
type Route struct {
	ID         string     `json:"id"`
	A          uint64     `json:"a"`
	B          uint64     `json:"b"`
	TravelMs   int64      `json:"travelMs"`
	Provenance Provenance `json:"provenance"`
}

// Resource はステージの資源 1 つ。
type Resource struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Critical bool   `json:"critical"`
}

// Ferry は船の状態（§5.1 の ferries[]。sim.FerryView と同じ JSON）。
// from・to は sailing のときだけ。departAt・arriveAt は docked のとき 0。
type Ferry struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Capacity int        `json:"capacity"`
	State    FerryState `json:"state"`
	IslandID uint64     `json:"islandId,omitempty"`
	From     uint64     `json:"from,omitempty"`
	To       uint64     `json:"to,omitempty"`
	DepartAt int64      `json:"departAt"`
	ArriveAt int64      `json:"arriveAt"`
	ReadyAt  int64      `json:"readyAt"`
	Cargo    []uint64   `json:"cargo"`
}

// RouteState は航路の状態（§5.1 の routesState[]。sim.RouteView と同じ JSON）。
type RouteState struct {
	ID           string `json:"id"`
	BlockedUntil *int64 `json:"blockedUntil" tstype:"number | null,required"`
}

// Letter は手紙（§5.2。sim.LetterView と同じ JSON）。
type Letter struct {
	ID        uint64      `json:"id"`
	Kind      LetterKind  `json:"kind"`
	From      uint64      `json:"from"`
	To        uint64      `json:"to"`
	CreatedAt int64       `json:"createdAt"`
	Loc       Loc         `json:"loc"`
	Label     LetterLabel `json:"label"`
	Raft      *RaftInfo   `json:"raft,omitempty"`
	Request   *RequestRef `json:"request,omitempty"`
}

// Loc は手紙のある場所（§5.2 の loc。sim.LocView と同じ JSON）。
type Loc struct {
	Type     LocType `json:"type"`
	IslandID uint64  `json:"islandId,omitempty"`
	FerryID  string  `json:"ferryId,omitempty"`
}

// RaftInfo は Raft の手紙の中身（§5.2 の raft。sim.RaftView と同じ JSON）。
type RaftInfo struct {
	Type       string        `json:"type"` // "MsgApp" など
	Term       uint64        `json:"term"`
	LogTerm    uint64        `json:"logTerm"`
	Index      uint64        `json:"index"`
	Commit     uint64        `json:"commit"`
	Entries    []LetterEntry `json:"entries"`
	Reject     bool          `json:"reject"`
	RejectHint uint64        `json:"rejectHint"`
	Stale      bool          `json:"stale"`
}

// LetterEntry は手紙に入ったエントリ 1 つ（sim.EntryView と同じ JSON）。
type LetterEntry struct {
	Index   uint64    `json:"index"`
	Term    uint64    `json:"term"`
	Type    EntryType `json:"type"`
	Summary string    `json:"summary"`
}

// RequestRef は依頼書の中身（§5.2 の request。sim.RequestView と同じ JSON）。
type RequestRef struct {
	ReqID string `json:"reqId"`
	Hops  int    `json:"hops"`
}

// Request は依頼（§5.3）。
type Request struct {
	ID        string        `json:"id"`
	Origin    uint64        `json:"origin"`
	Kind      string        `json:"kind"` // "stall.reserve" など（ステージと台帳が決める）
	Resource  string        `json:"resource"`
	Critical  bool          `json:"critical"`
	CreatedAt int64         `json:"createdAt"`
	Deadline  int64         `json:"deadline"`
	Status    RequestStatus `json:"status"`
	Expired   bool          `json:"expired"`
	Result    *string       `json:"result" tstype:"string | null,required"` // 中身は T-202 が決める
}

// NodeSummary は島（Raft のノード）の要約（§5.1 の nodes[]・node.state。sim.NodeState と同じ JSON）。
type NodeSummary struct {
	ID        uint64 `json:"id"`
	Role      Role   `json:"role"`
	Term      uint64 `json:"term"`
	Lead      uint64 `json:"lead"`
	Commit    uint64 `json:"commit"`
	Applied   uint64 `json:"applied"`
	LastIndex uint64 `json:"lastIndex"`
}

// IslandState は島の満足度など（§5.1 の islandsState[]）。
type IslandState struct {
	ID              uint64 `json:"id"`
	Satisfaction    int    `json:"satisfaction"`
	LastDeliveredAt *int64 `json:"lastDeliveredAt" tstype:"number | null,required"`
}

// Score はラウンド中のスコア（§5.1 の score）。
type Score struct {
	Total           int      `json:"total"`
	Successes       int      `json:"successes"`
	Target          int      `json:"target"`
	CriticalSecured []string `json:"criticalSecured"`
}

// Snapshot は snapshot の payload（§5.1）。キーの順はプロトコルの例の順。
type Snapshot struct {
	Protocol     int           `json:"protocol"`
	Session      Session       `json:"session"`
	Phase        Phase         `json:"phase"`
	Clock        SnapshotClock `json:"clock"`
	Stage        StageView     `json:"stage"`
	Ferries      []Ferry       `json:"ferries"`
	RoutesState  []RouteState  `json:"routesState"`
	Letters      []Letter      `json:"letters"`
	Requests     []Request     `json:"requests"`
	Nodes        []NodeSummary `json:"nodes"`
	IslandsState []IslandState `json:"islandsState"`
	Score        Score         `json:"score"`
	LastSeq      uint64        `json:"lastSeq"`
}

// EventsPayload は events の payload。
type EventsPayload struct {
	Clock  Clock             `json:"clock"`
	Events []json.RawMessage `json:"events" tstype:"ServerEvent[]"` // 各要素は sim.DomainEvent の JSON
}

// Ack は ack の payload。T は適用したゲーム内時刻で、失敗でも入れる。
type Ack struct {
	CmdID   string  `json:"cmdId"`
	OK      bool    `json:"ok"`
	T       int64   `json:"t"`
	Code    AckCode `json:"code,omitempty"`
	Message string  `json:"message,omitempty"`
}

// ErrorPayload は error の payload。fatal なら直後に切断する。
type ErrorPayload struct {
	Code    string `json:"code"` // 値の一覧は T-302・T-303
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// PongPayload は pong の payload。
type PongPayload struct {
	ClientTime float64 `json:"clientTime"`
	SimTime    int64   `json:"simTime"`
}

// NodeDetail は node.detail の payload（§5.4。エンジニアモード）。
type NodeDetail struct {
	ID               uint64                     `json:"id"`
	Role             Role                       `json:"role"`
	Term             uint64                     `json:"term"`
	Vote             uint64                     `json:"vote"`
	Lead             uint64                     `json:"lead"`
	Commit           uint64                     `json:"commit"`
	Applied          uint64                     `json:"applied"`
	LastIndex        uint64                     `json:"lastIndex"`
	Log              []LogEntry                 `json:"log"`                // 末尾の 50 件
	Progress         []Progress                 `json:"progress,omitempty"` // Leader のときだけ
	Ledger           map[string]json.RawMessage `json:"ledger" tstype:"{ [resourceId: string]: string | string[] }"`
	ElectionEstimate ElectionEstimate           `json:"electionEstimate"`
}

// LogEntry はノードのログのエントリ 1 つ。
type LogEntry struct {
	Index     uint64    `json:"index"`
	Term      uint64    `json:"term"`
	Type      EntryType `json:"type"`
	Summary   string    `json:"summary"`
	Committed bool      `json:"committed"`
}

// Progress は Leader から見た Follower 1 つの複製の進み具合。
type Progress struct {
	ID           uint64        `json:"id"`
	Match        uint64        `json:"match"`
	Next         uint64        `json:"next"`
	State        ProgressState `json:"state"`
	RecentActive bool          `json:"recentActive"`
}

// ElectionEstimate は選挙までの見込み（推定値）。
type ElectionEstimate struct {
	TicksSinceLeaderContact int    `json:"ticksSinceLeaderContact"`
	Note                    string `json:"note"`
}

// RoundResult は round.result・internal.result の payload（§5.5）。
type RoundResult struct {
	Score             RoundScore  `json:"score"`
	Stars             int         `json:"stars"`
	Stats             RoundStats  `json:"stats"`
	Story             []StoryItem `json:"story"`
	InputLogAvailable bool        `json:"inputLogAvailable"`
}

// RoundScore はラウンドの最終スコア。
type RoundScore struct {
	Total     int            `json:"total"`
	Breakdown ScoreBreakdown `json:"breakdown"`
}

// ScoreBreakdown はスコアの内訳。
type ScoreBreakdown struct {
	Requests  int `json:"requests"`
	Expired   int `json:"expired"`
	FerryCost int `json:"ferryCost"`
	Fairness  int `json:"fairness"`
}

// RoundStats はラウンドの統計（DesignDoc §10.3）。
type RoundStats struct {
	Succeeded          int            `json:"succeeded"`
	Rejected           int            `json:"rejected"`
	Expired            int            `json:"expired"`
	LettersDelivered   map[string]int `json:"lettersDelivered"` // label ごとの配達の数
	LeaderChanges      int            `json:"leaderChanges"`
	AvgCommitLatencyMs int64          `json:"avgCommitLatencyMs"`
	LongestSilenceMs   int64          `json:"longestSilenceMs"`
	FinalLogs          []FinalLog     `json:"finalLogs"`
}

//tygo:emit
var _ = `export type TermRange = [term: number, first: number, last: number];`

// FinalLog は終了時のノード 1 つのログの形。Terms の各要素は [term, 最初の index, 最後の index]（TS では TermRange）。
type FinalLog struct {
	ID        uint64      `json:"id"`
	LastIndex uint64      `json:"lastIndex"`
	Commit    uint64      `json:"commit"`
	Terms     [][3]uint64 `json:"terms" tstype:"TermRange[]"`
}

// StoryItem はリザルトの物語の 1 項目。
type StoryItem struct {
	T        int64    `json:"t"`
	Text     string   `json:"text"`
	Concepts []string `json:"concepts"`
}
