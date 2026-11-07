// Package stage は、ETL（server/cmd/islandsdata）が作った islands.json と、
// 人が書くステージ定義 data/stages/*.yaml を読んで検証し、整数だけで表した Stage を返す
// （データ仕様 §3.1・§4.1・§5、ADR-0022）。
//
// このパッケージは sim.Engine の中で動くものではないが、ステージの値はシミュレーションの結果を
// 決めるので、sim と同じ規則で書く: 浮動小数点・実時間・乱数・goroutine・チャネルを使わず、
// map の走査で出力や Issue の順序を決めない（DesignDoc §9.2）。
// 既定値も持たない。必須の欄を書いていなければエラーにする。
package stage

import "strings"

// Provenance は出典区分（データ仕様 §1）。
type Provenance string

const (
	Fact     Provenance = "fact"
	Derived  Provenance = "derived"
	Creative Provenance = "creative"
)

// Source は出典（データ仕様 §5.2 の source）。
type Source struct {
	Dataset string // islands.json の sources のキー
	Region  string // D2 の DESIGNATED_REGION_NAME
	Field   string // D2 の列名
	Quote   string // "" = 引用なし
}

// Text は出典区分つきの文章（intro・flavor）。
type Text struct {
	Text       string
	Provenance Provenance
	Source     *Source // nil = 書いていない
}

// Stage は検証済みのステージ定義。値はすべて整数で、ゲーム内時間はミリ秒。
type Stage struct {
	ID                   string
	Title                string
	Region               string
	Intro                Text
	Learning             []string // 書いていなければ nil
	DurationMs           int64
	Unlock               string   // "" = null（最初から遊べる）
	Tutorial             string   // "" = 書いていない
	Islands              []Island // 添字 + 1 = ノード ID
	Routes               []Route
	Ferries              []Ferry
	Raft                 Raft
	Transport            Transport
	Resources            []Resource
	Requests             Requests
	Events               []Event
	Demo                 *Demo // nil = demo のキーがない
	Scoring              Scoring
	Satisfaction         Satisfaction
	SpeedAllowedPermille []int64 // speed.allowed（1000 = 1 倍）。狭義の昇順
	Hash                 string  // "sha256:" + 64 桁の 16 進の小文字
	Warnings             []Issue // 検査の警告（エラーではない）。なければ nil
}

// Island はステージの島。
type Island struct {
	NodeID              uint64 // 1..N（islands の順）
	Ref                 string // islands.json の id
	Name                string // islands.json の name
	Prefecture          string // islands.json の prefecture
	X, Y                int64  // layout
	Color               string // "#RRGGBB"（書いたとおり）
	Population          int64  // islands.json の population.value
	PopulationSqrtCenti int64  // islands.json の populationSqrt × 100
	WeightPermille      int64  // データ仕様 §4.1 の w_i。全島の合計は 1000
}

// Route は島と島の間の航路。
type Route struct {
	A, B       uint64 // ノード ID（書いた順のまま。並べ替えない）
	TravelMs   int64
	Provenance Provenance
	Source     *Source
}

// Ferry は船。
type Ferry struct {
	ID            string
	Name          string
	Home          uint64 // ノード ID
	Capacity      int
	SpeedPermille int64      // speed × 1000（1200 = 1.2 倍の時間がかかる）
	Provenance    Provenance // 書いていなければ Creative
}

// Raft は etcd-io/raft の設定（DesignDoc §5.2）。
type Raft struct {
	InitialLeader   uint64 // ノード ID
	TickMs          int64
	HeartbeatTick   int
	ElectionTick    int
	CheckQuorum     bool
	PreVote         bool
	MaxInflightMsgs int
	MaxSizePerMsg   uint64
}

// Transport は輸送の設定。
type Transport struct {
	DockMs        int64
	PerLinkAppCap int
	PortCap       int
	RelayCap      int // 中継倉庫（P1）
}

// ResourceKind は資源の種類。
type ResourceKind string

const (
	ResourceFirework ResourceKind = "firework"
	ResourceStall    ResourceKind = "stall"
)

// Resource は予約できる資源。
type Resource struct {
	ID       string
	Kind     ResourceKind
	Label    string
	Critical bool // 書いていなければ false
	Capacity int  // stall の定員。firework では 0
}

// 依頼の種類（ledger の Kind と同じ文字列）。
const (
	RequestFireworkReserve = "firework.reserve"
	RequestStallReserve    = "stall.reserve"
)

// Requests は依頼の設定。
type Requests struct {
	DeadlineNormalMs   int64
	DeadlineCriticalMs int64
	RetryMs            int64
	Script             []ScriptRequest // 書いた順のまま
	Random             Random
}

// ScriptRequest は台本の依頼。
type ScriptRequest struct {
	At       int64
	Island   uint64 // ノード ID
	Kind     string
	Resource string
}

// Random はランダムな依頼の設定（データ仕様 §4.2）。
type Random struct {
	RateMilliPerMin            int64
	SlotMs                     int64
	PopulationExponentPermille int64 // 0・500・1000 のどれか
	Kinds                      []RandomKind
}

// RandomKind はランダムな依頼の種類と重み。
type RandomKind struct {
	Kind     string
	Resource string
	Weight   int64
}

// EventKind はイベントの種類。
type EventKind string

const (
	EventStorm        EventKind = "storm"
	EventCancellation EventKind = "cancellation"
	EventBreakdown    EventKind = "breakdown"
	EventRush         EventKind = "rush"
	EventDrift        EventKind = "drift"
)

// Window はイベントの開始の時刻の範囲。at と書いたときは Start == End == at。
type Window struct{ Start, End int64 }

// Event は 1 つのイベント。その種類で使わない欄はゼロ値。
type Event struct {
	Kind                EventKind
	When                Window      // storm・breakdown・rush・drift
	DurationMs          int64       // storm・cancellation・breakdown
	Routes              [][2]uint64 // storm（ノード ID。書いた順のまま）
	ForecastMs          int64       // storm・rush
	Flavor              *Text       // storm（任意）
	Island              uint64      // cancellation（ノード ID）
	ProbabilityPermille int64       // cancellation
	SlotMs              int64       // cancellation
	Provenance          Provenance  // cancellation
	Source              *Source     // cancellation
	Ferry               string      // breakdown（船の ID か "any"）
	Requests            int         // rush
	Letters             int         // drift
	MinAgeMs            int64       // drift
}

// Demo はデモ用の設定。
type Demo struct {
	EndOnAllCriticalSecured bool
	Storm                   *DemoStorm // nil = 書いていない
}

// DemoStorm は発表者の操作で起こす嵐。
type DemoStorm struct {
	Routes     [][2]uint64
	DurationMs int64
	Flavor     *Text
}

// Scoring は目標（配点のキーは T-203 で足す）。
type Scoring struct {
	TargetSuccesses int64 // scoring.target.successes
	TargetScore3    int64 // scoring.target.score3
}

// Satisfaction は満足度の設定。
type Satisfaction struct {
	Initial               int64
	SilenceAfterMs        int64
	SilencePenaltyEveryMs int64
	SilencePenaltyAmount  int64
}

// Issue は 1 つの誤り（または警告）。
type Issue struct {
	Path    string // YAML・JSON の場所。例: "routes[1].travelMs"（添字は 0 始まり）。ファイル全体のときは ""
	Message string // 空でない日本語の説明
}

// Error は 1 つのファイルの誤りの一覧。
type Error struct {
	File   string  // Parse・ParseIslands に渡した name
	Issues []Issue // 1 件以上
}

// Error は、各 Issue を「stage: <File>: <Path>: <Message>」の 1 行にして改行でつなぐ。
func (e *Error) Error() string {
	var b strings.Builder
	for i, is := range e.Issues {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("stage: ")
		b.WriteString(e.File)
		b.WriteString(": ")
		if is.Path != "" {
			b.WriteString(is.Path)
			b.WriteString(": ")
		}
		b.WriteString(is.Message)
	}
	return b.String()
}

// fileError は 1 件の Issue だけの *Error を作る。
func fileError(file, path, msg string) *Error {
	return &Error{File: file, Issues: []Issue{{Path: path, Message: msg}}}
}

// oneLine は、複数行のメッセージ（yaml の unmarshal errors など）を 1 行にする。
func oneLine(s string) string {
	lines := strings.Split(s, "\n")
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " ")
}
