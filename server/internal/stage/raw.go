package stage

// ステージ定義の YAML をそのまま受ける型（データ仕様 §5.2・§5.3・§5.5）。
// yaml のタグは YAML のキーと同じ名前にする（KnownFields(true) で未知のキーをエラーにする）。
// 必須のマッピングはポインタで受け、書いたかどうかを見分ける。

type rawStage struct {
	Format       yInt             `yaml:"format"`
	ID           string           `yaml:"id"`
	Title        string           `yaml:"title"`
	Region       string           `yaml:"region"`
	Intro        *rawText         `yaml:"intro"`
	Learning     []string         `yaml:"learning"`
	DurationMs   yInt             `yaml:"durationMs"`
	Unlock       *string          `yaml:"unlock"`
	Tutorial     *string          `yaml:"tutorial"`
	Islands      []rawIsland      `yaml:"islands"`
	Routes       []rawRoute       `yaml:"routes"`
	Ferries      []rawFerry       `yaml:"ferries"`
	Raft         *rawRaft         `yaml:"raft"`
	Transport    *rawTransport    `yaml:"transport"`
	Resources    []rawResource    `yaml:"resources"`
	Requests     *rawRequests     `yaml:"requests"`
	Events       []rawEvent       `yaml:"events"`
	Demo         *rawDemo         `yaml:"demo"`
	Scoring      *rawScoring      `yaml:"scoring"`
	Satisfaction *rawSatisfaction `yaml:"satisfaction"`
	Speed        *rawSpeed        `yaml:"speed"`
}

type rawText struct {
	Text       string     `yaml:"text"`
	Provenance string     `yaml:"provenance"`
	Source     *rawSource `yaml:"source"`
}

type rawSource struct {
	Dataset string `yaml:"dataset"`
	Region  string `yaml:"region"`
	Field   string `yaml:"field"`
	Quote   string `yaml:"quote"`
}

type rawIsland struct {
	Ref    string     `yaml:"ref"`
	Layout *rawLayout `yaml:"layout"`
	Color  string     `yaml:"color"`
}

type rawLayout struct {
	X yInt `yaml:"x"`
	Y yInt `yaml:"y"`
}

type rawRoute struct {
	A          string     `yaml:"a"`
	B          string     `yaml:"b"`
	TravelMs   yInt       `yaml:"travelMs"`
	Provenance string     `yaml:"provenance"`
	Source     *rawSource `yaml:"source"`
}

type rawFerry struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Home       string `yaml:"home"`
	Capacity   yInt   `yaml:"capacity"`
	Speed      yDec   `yaml:"speed"`
	Provenance string `yaml:"provenance"`
}

type rawRaft struct {
	InitialLeader   string `yaml:"initialLeader"`
	TickMs          yInt   `yaml:"tickMs"`
	HeartbeatTick   yInt   `yaml:"heartbeatTick"`
	ElectionTick    yInt   `yaml:"electionTick"`
	CheckQuorum     yBool  `yaml:"checkQuorum"`
	PreVote         yBool  `yaml:"preVote"`
	MaxInflightMsgs yInt   `yaml:"maxInflightMsgs"`
	MaxSizePerMsg   yInt   `yaml:"maxSizePerMsg"`
}

type rawTransport struct {
	DockMs        yInt `yaml:"dockMs"`
	PerLinkAppCap yInt `yaml:"perLinkAppCap"`
	PortCap       yInt `yaml:"portCap"`
	RelayCap      yInt `yaml:"relayCap"`
}

type rawResource struct {
	ID       string `yaml:"id"`
	Kind     string `yaml:"kind"`
	Label    string `yaml:"label"`
	Critical yBool  `yaml:"critical"`
	Capacity yInt   `yaml:"capacity"`
}

type rawRequests struct {
	DeadlineMs *rawDeadline       `yaml:"deadlineMs"`
	RetryMs    yInt               `yaml:"retryMs"`
	Script     []rawScriptRequest `yaml:"script"`
	Random     *rawRandom         `yaml:"random"`
}

type rawDeadline struct {
	Normal   yInt `yaml:"normal"`
	Critical yInt `yaml:"critical"`
}

type rawScriptRequest struct {
	At       yInt   `yaml:"at"`
	Island   string `yaml:"island"`
	Kind     string `yaml:"kind"`
	Resource string `yaml:"resource"`
}

type rawRandom struct {
	RateMilliPerMin    yInt            `yaml:"rateMilliPerMin"`
	SlotMs             yInt            `yaml:"slotMs"`
	PopulationExponent yDec            `yaml:"populationExponent"`
	Kinds              []rawRandomKind `yaml:"kinds"`
}

type rawRandomKind struct {
	Kind     string `yaml:"kind"`
	Resource string `yaml:"resource"`
	Weight   yInt   `yaml:"weight"`
}

// rawEvent は 5 種のイベントのすべてのキーを持つ。「書かない」キーを見つけるため、
// 文字列と列もポインタで受ける。
type rawEvent struct {
	Kind                string      `yaml:"kind"`
	At                  yInt        `yaml:"at"`
	Window              *[]yInt     `yaml:"window"`
	DurationMs          yInt        `yaml:"durationMs"`
	Routes              *[][]string `yaml:"routes"`
	ForecastMs          yInt        `yaml:"forecastMs"`
	Flavor              *rawText    `yaml:"flavor"`
	Island              *string     `yaml:"island"`
	ProbabilityPermille yInt        `yaml:"probabilityPermille"`
	SlotMs              yInt        `yaml:"slotMs"`
	Provenance          *string     `yaml:"provenance"`
	Source              *rawSource  `yaml:"source"`
	Ferry               *string     `yaml:"ferry"`
	Requests            yInt        `yaml:"requests"`
	Letters             yInt        `yaml:"letters"`
	MinAgeMs            yInt        `yaml:"minAgeMs"`
}

type rawDemo struct {
	EndOnAllCriticalSecured yBool         `yaml:"endOnAllCriticalSecured"`
	Storm                   *rawDemoStorm `yaml:"storm"`
}

type rawDemoStorm struct {
	Routes     *[][]string `yaml:"routes"`
	DurationMs yInt        `yaml:"durationMs"`
	Flavor     *rawText    `yaml:"flavor"`
}

type rawScoring struct {
	Target                  *rawTarget `yaml:"target"`
	Points                  *rawPoints `yaml:"points"`
	FerryCostMsPerPoint     yInt       `yaml:"ferryCostMsPerPoint"`
	FairnessPerSatisfaction yInt       `yaml:"fairnessPerSatisfaction"`
}

type rawPoints struct {
	Normal         yInt `yaml:"normal"`
	Critical       yInt `yaml:"critical"`
	Late           yInt `yaml:"late"`
	Rejected       yInt `yaml:"rejected"`
	ExpiredPenalty yInt `yaml:"expiredPenalty"`
}

type rawTarget struct {
	Successes yInt `yaml:"successes"`
	Score3    yInt `yaml:"score3"`
}

type rawSatisfaction struct {
	Initial         yInt               `yaml:"initial"`
	SilenceAfterMs  yInt               `yaml:"silenceAfterMs"`
	SilencePenalty  *rawSilencePenalty `yaml:"silencePenalty"`
	Succeeded       yInt               `yaml:"succeeded"`
	RejectedPenalty yInt               `yaml:"rejectedPenalty"`
	ExpiredPenalty  yInt               `yaml:"expiredPenalty"`
}

type rawSilencePenalty struct {
	EveryMs yInt `yaml:"everyMs"`
	Amount  yInt `yaml:"amount"`
}

type rawSpeed struct {
	Allowed []yDec `yaml:"allowed"`
}
