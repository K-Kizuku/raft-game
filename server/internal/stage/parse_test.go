package stage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"sync"
	"testing"
)

var hashRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func parseFixture(t *testing.T, name string) *Stage {
	t.Helper()
	st, err := Parse("stages/"+name, []byte(readTestdata(t, "stages/"+name)), fixtureIslands(t))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	if !hashRe.MatchString(st.Hash) {
		t.Fatalf("Hash = %q", st.Hash)
	}
	return st
}

func weightsOf(st *Stage) []int64 {
	w := make([]int64, len(st.Islands))
	for i, isl := range st.Islands {
		w[i] = isl.WeightPermille
	}
	return w
}

// P-1 alpha。
func TestParseAlpha(t *testing.T) {
	t.Parallel()
	st := parseFixture(t, "alpha.yaml")
	st.Hash = ""
	want := &Stage{
		ID:     "alpha",
		Title:  "テストの海",
		Region: "波間県（架空）",
		Intro: Text{Text: "架空の穏やかな海です。", Provenance: Derived,
			Source: &Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "NATURAL_CLIMATE_FEATURES"}},
		DurationMs: 180000,
		Islands: []Island{
			{NodeID: 1, Ref: "hama-hoshizuna", Name: "星砂島", Prefecture: "波間県", X: 600, Y: 200, Color: "#E07A5F", Population: 19134, PopulationSqrtCenti: 13833, WeightPermille: 773},
			{NodeID: 2, Ref: "hama-tsukikage", Name: "月影島", Prefecture: "波間県", X: 400, Y: 300, Color: "#3D85C6", Population: 768, PopulationSqrtCenti: 2771, WeightPermille: 155},
			{NodeID: 3, Ref: "hama-todai", Name: "灯台島", Prefecture: "波間県", X: 200, Y: 300, Color: "#81B29A", Population: 169, PopulationSqrtCenti: 1300, WeightPermille: 72},
		},
		Routes: []Route{
			{A: 1, B: 2, TravelMs: 1500, Provenance: Derived,
				Source: &Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "月影~星砂(3km・10分"}},
			{A: 2, B: 3, TravelMs: 4000, Provenance: Creative},
		},
		Ferries: []Ferry{{ID: "f1", Name: "テスト丸", Home: 1, Capacity: 4, SpeedPermille: 1000, Provenance: Creative}},
		Raft: Raft{InitialLeader: 1, TickMs: 500, HeartbeatTick: 2, ElectionTick: 40, CheckQuorum: true, PreVote: true,
			MaxInflightMsgs: 4, MaxSizePerMsg: 4096},
		Transport: Transport{DockMs: 1500, PerLinkAppCap: 4, PortCap: 24, RelayCap: 12},
		Resources: []Resource{
			{ID: "firework:a", Kind: ResourceFirework, Label: "大花火", Critical: true},
			{ID: "stall:main", Kind: ResourceStall, Label: "屋台の出店枠", Capacity: 2},
		},
		Requests: Requests{
			DeadlineNormalMs: 60000, DeadlineCriticalMs: 90000, RetryMs: 25000,
			Script: []ScriptRequest{
				{At: 5000, Island: 3, Kind: RequestFireworkReserve, Resource: "firework:a"},
				{At: 6000, Island: 2, Kind: RequestStallReserve, Resource: "stall:main"},
			},
			Random: Random{RateMilliPerMin: 500, SlotMs: 10000, PopulationExponentPermille: 500,
				Kinds: []RandomKind{{Kind: RequestStallReserve, Resource: "stall:main", Weight: 1}}},
		},
		Scoring:              Scoring{TargetSuccesses: 2, TargetScore3: 500},
		Satisfaction:         Satisfaction{Initial: 60, SilenceAfterMs: 30000, SilencePenaltyEveryMs: 10000, SilencePenaltyAmount: 2},
		SpeedAllowedPermille: []int64{500, 1000, 2000, 4000},
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("Parse(alpha) =\n%+v\nwant\n%+v", st, want)
	}
}

// P-2 beta。
func TestParseBeta(t *testing.T) {
	t.Parallel()
	st := parseFixture(t, "beta.yaml")
	if st.Unlock != "alpha" || st.Tutorial != "beta-tour" || !reflect.DeepEqual(st.Learning, []string{"log_replication", "commit"}) {
		t.Errorf("Unlock・Tutorial・Learning = %q %q %v", st.Unlock, st.Tutorial, st.Learning)
	}
	if got, want := weightsOf(st), []int64{461, 92, 43, 5, 236, 58, 105}; !reflect.DeepEqual(got, want) {
		t.Errorf("重み = %v, want %v", got, want)
	}
	wantFerries := []Ferry{
		{ID: "f1", Name: "架空丸", Home: 1, Capacity: 4, SpeedPermille: 1200, Provenance: Fact},
		{ID: "f2", Name: "テスト丸", Home: 5, Capacity: 3, SpeedPermille: 1000, Provenance: Creative},
	}
	if !reflect.DeepEqual(st.Ferries, wantFerries) {
		t.Errorf("Ferries = %+v", st.Ferries)
	}
	wantEvents := []Event{
		{Kind: EventStorm, When: Window{60000, 60000}, DurationMs: 20000, Routes: [][2]uint64{{2, 1}, {3, 2}}, ForecastMs: 10000,
			Flavor: &Text{Text: "架空の台風が近づいています", Provenance: Derived,
				Source: &Source{Dataset: "fixture-2026", Region: "灯台地域", Field: "DISASTER_LAND_CONSERVATION", Quote: "架空の台風で灯台が壊れた"}}},
		{Kind: EventCancellation, DurationMs: 10000, Island: 3, ProbabilityPermille: 170, SlotMs: 30000, Provenance: Derived,
			Source: &Source{Dataset: "fixture-2026", Region: "灯台地域", Field: "DISASTER_LAND_CONSERVATION"}},
		{Kind: EventBreakdown, When: Window{80000, 120000}, DurationMs: 30000, Ferry: "any"},
		{Kind: EventRush, When: Window{100000, 110000}, ForecastMs: 5000, Requests: 4},
		{Kind: EventDrift, When: Window{130000, 130000}, Letters: 3, MinAgeMs: 30000},
	}
	if !reflect.DeepEqual(st.Events, wantEvents) {
		t.Errorf("Events =\n%+v\nwant\n%+v", st.Events, wantEvents)
	}
	wantDemo := &Demo{EndOnAllCriticalSecured: true, Storm: &DemoStorm{
		Routes: [][2]uint64{{5, 1}}, DurationMs: 20000,
		Flavor: &Text{Text: "星砂島と大石島の間の海が荒れています", Provenance: Creative},
	}}
	if !reflect.DeepEqual(st.Demo, wantDemo) {
		t.Errorf("Demo = %+v", st.Demo)
	}
	if st.Raft.PreVote || !st.Raft.CheckQuorum || st.Transport.RelayCap != 0 {
		t.Errorf("Raft・Transport = %+v %+v", st.Raft, st.Transport)
	}
	if !reflect.DeepEqual(st.SpeedAllowedPermille, []int64{1000, 1500, 2250}) {
		t.Errorf("SpeedAllowedPermille = %v", st.SpeedAllowedPermille)
	}
	if st.Resources[2].Capacity != 3 || st.Resources[1].Critical {
		t.Errorf("Resources = %+v", st.Resources)
	}
	if st.Warnings != nil {
		t.Errorf("Warnings = %v", st.Warnings)
	}
}

// P-3 検査。
func TestParseValidation(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	fixtures := map[string]string{
		"alpha": readTestdata(t, "stages/alpha.yaml"),
		"beta":  readTestdata(t, "stages/beta.yaml"),
	}
	const (
		introSource = "\n  source: { dataset: fixture-2026, region: 星砂地域, field: NATURAL_CLIMATE_FEATURES }"
		route0Src   = "\n    source: { dataset: fixture-2026, region: 星砂地域, field: MARITIME_TRANSPORT, quote: \"月影~星砂(3km・10分\" }"
		todaiIsland = "  - { ref: hama-todai, layout: { x: 200, y: 300 }, color: \"#81B29A\" }\n"
		route1      = "  - { a: hama-tsukikage, b: hama-todai, travelMs: 4000, provenance: creative }\n"
		ferry0      = "ferries:\n  - { id: f1, name: テスト丸, home: hama-hoshizuna, capacity: 4, speed: 1.0 }\n"
		resources   = "resources:\n  - { id: \"firework:a\", kind: firework, label: 大花火, critical: true }\n  - { id: \"stall:main\", kind: stall, label: 屋台の出店枠, capacity: 2 }\n"
		script0     = "{ at: 5000, island: hama-todai, kind: firework.reserve, resource: \"firework:a\" }"
		stormRoutes = "routes: [[hama-tsukikage, hama-hoshizuna], [hama-todai, hama-tsukikage]]"
		flavorSrc   = "\n      source: { dataset: fixture-2026, region: 灯台地域, field: DISASTER_LAND_CONSERVATION, quote: \"架空の台風で灯台が壊れた\" }"
		cancelProv  = "    provenance: derived\n    source: { dataset: fixture-2026, region: 灯台地域, field: DISASTER_LAND_CONSERVATION }"
	)
	cases := []struct {
		name  string
		base  string
		edits []edit
		path  string
	}{
		// 共通
		{"format", "alpha", []edit{{"format: 1", "format: 2"}}, "format"},
		{"formatなし", "alpha", []edit{{"format: 1\n", ""}}, "format"},
		{"id大文字", "alpha", []edit{{"id: alpha", "id: Alpha"}}, "id"},
		{"title空", "alpha", []edit{{"title: テストの海", `title: ""`}}, "title"},
		{"regionなし", "alpha", []edit{{"region: 波間県（架空）\n", ""}}, "region"},
		{"introなし", "alpha", []edit{{"intro:\n  text: 架空の穏やかな海です。\n  provenance: derived" + introSource + "\n", ""}}, "intro"},
		{"intro.text空白", "alpha", []edit{{"text: 架空の穏やかな海です。", "text: \"　 \""}}, "intro.text"},
		{"intro.provenance", "alpha", []edit{{"  provenance: derived" + introSource, "  provenance: made-up" + introSource}}, "intro.provenance"},
		{"intro.sourceなし", "alpha", []edit{{introSource, ""}}, "intro.source"},
		{"dataset", "alpha", []edit{{"dataset: fixture-2026, region: 星砂地域, field: NATURAL", "dataset: nope, region: 星砂地域, field: NATURAL"}}, "intro.source.dataset"},
		{"source.region", "alpha", []edit{{"region: 星砂地域, field: NATURAL", "region: 架空地域, field: NATURAL"}}, "intro.source.region"},
		{"source.field空", "alpha", []edit{{"field: NATURAL_CLIMATE_FEATURES }", `field: "" }`}}, "intro.source.field"},
		{"learning形式", "alpha", []edit{{"durationMs: 180000\n", "learning: [Bad-Key]\ndurationMs: 180000\n"}}, "learning[0]"},
		{"learning重複", "alpha", []edit{{"durationMs: 180000\n", "learning: [commit, commit]\ndurationMs: 180000\n"}}, "learning[1]"},
		{"durationMs=0", "alpha", []edit{{"durationMs: 180000", "durationMs: 0"}}, "durationMs"},
		{"durationMs大", "alpha", []edit{{"durationMs: 180000", "durationMs: 1000000001"}}, "durationMs"},
		{"durationMsなし", "alpha", []edit{{"durationMs: 180000\n", ""}}, "durationMs"},
		{"unlock自分", "alpha", []edit{{"unlock: null", "unlock: alpha"}}, "unlock"},
		{"unlock形式", "alpha", []edit{{"unlock: null", "unlock: Bad"}}, "unlock"},
		{"tutorial", "alpha", []edit{{"unlock: null", "unlock: null\ntutorial: \"Bad Key\""}}, "tutorial"},
		// 島
		{"2島", "alpha", []edit{{todaiIsland, ""}}, "islands"},
		{"8島", "alpha", []edit{{todaiIsland, todaiIsland +
			"  - { ref: shiomi-koishi, layout: { x: 1, y: 1 }, color: \"#000000\" }\n" +
			"  - { ref: shiomi-ooishi, layout: { x: 1, y: 1 }, color: \"#000000\" }\n" +
			"  - { ref: shiomi-nami, layout: { x: 1, y: 1 }, color: \"#000000\" }\n" +
			"  - { ref: kaze-hayate, layout: { x: 1, y: 1 }, color: \"#000000\" }\n" +
			"  - { ref: kaze-nagi, layout: { x: 1, y: 1 }, color: \"#000000\" }\n"}}, "islands"},
		{"未知のref", "alpha", []edit{{"ref: hama-tsukikage", "ref: hama-nowhere"}}, "islands[1].ref"},
		{"ref重複", "alpha", []edit{{"ref: hama-todai", "ref: hama-hoshizuna"}}, "islands[2].ref"},
		{"x負", "alpha", []edit{{"x: 600", "x: -1"}}, "islands[0].layout.x"},
		{"yなし", "alpha", []edit{{"x: 600, y: 200", "x: 600"}}, "islands[0].layout.y"},
		{"layoutなし", "alpha", []edit{{"layout: { x: 600, y: 200 }, ", ""}}, "islands[0].layout"},
		{"color名前", "alpha", []edit{{`"#E07A5F"`, "red"}}, "islands[0].color"},
		{"color5桁", "alpha", []edit{{`"#E07A5F"`, `"#12345"`}}, "islands[0].color"},
		// 航路
		{"未知のa", "alpha", []edit{{"  - a: hama-hoshizuna\n", "  - a: hama-nowhere\n"}}, "routes[0].a"},
		{"a==b", "alpha", []edit{{"{ a: hama-tsukikage, b: hama-todai,", "{ a: hama-todai, b: hama-todai,"}}, "routes[1].b"},
		{"逆向きの重複", "alpha", []edit{{route1, route1 + "  - { a: hama-todai, b: hama-tsukikage, travelMs: 10, provenance: creative }\n"}}, "routes[2]"},
		{"travelMs=0", "alpha", []edit{{"travelMs: 1500", "travelMs: 0"}}, "routes[0].travelMs"},
		{"provenanceなし", "alpha", []edit{{", provenance: creative }", " }"}}, "routes[1].provenance"},
		{"derivedのsourceなし", "alpha", []edit{{route0Src, ""}}, "routes[0].source"},
		{"quoteの全角", "alpha", []edit{{`"月影~星砂(3km・10分"`, `"月影～星砂(3km・10分"`}}, "routes[0].source.quote"},
		{"非連結", "alpha", []edit{{route1, ""}}, "routes"},
		// 船
		{"船なし", "alpha", []edit{{ferry0, "ferries: []\n"}}, "ferries"},
		{"船id重複", "alpha", []edit{{ferry0, ferry0 + "  - { id: f1, name: 二号丸, home: hama-todai, capacity: 1, speed: 2 }\n"}}, "ferries[1].id"},
		{"船id=any", "alpha", []edit{{"id: f1", "id: any"}}, "ferries[0].id"},
		{"船name空", "alpha", []edit{{"name: テスト丸", `name: ""`}}, "ferries[0].name"},
		{"船home", "alpha", []edit{{"home: hama-hoshizuna", "home: nowhere"}}, "ferries[0].home"},
		{"船capacity", "alpha", []edit{{"capacity: 4, speed", "capacity: 0, speed"}}, "ferries[0].capacity"},
		{"船speed=0", "alpha", []edit{{"speed: 1.0 }", "speed: 0 }"}}, "ferries[0].speed"},
		{"船speedなし", "alpha", []edit{{", speed: 1.0 }", " }"}}, "ferries[0].speed"},
		{"船derived", "alpha", []edit{{"speed: 1.0 }", "speed: 1.0, provenance: derived }"}}, "ferries[0].provenance"},
		// Raft・輸送
		{"initialLeader", "alpha", []edit{{"initialLeader: hama-hoshizuna", "initialLeader: nowhere"}}, "raft.initialLeader"},
		{"tickMs=0", "alpha", []edit{{"tickMs: 500", "tickMs: 0"}}, "raft.tickMs"},
		{"heartbeatTick=0", "alpha", []edit{{"heartbeatTick: 2", "heartbeatTick: 0"}}, "raft.heartbeatTick"},
		{"electionTick=hb", "alpha", []edit{{"electionTick: 40", "electionTick: 2"}}, "raft.electionTick"},
		{"checkQuorumなし", "alpha", []edit{{"  checkQuorum: true\n", ""}}, "raft.checkQuorum"},
		{"preVoteなし", "alpha", []edit{{"  preVote: true\n", ""}}, "raft.preVote"},
		{"maxInflightMsgs=0", "alpha", []edit{{"maxInflightMsgs: 4", "maxInflightMsgs: 0"}}, "raft.maxInflightMsgs"},
		{"maxSizePerMsg=0", "alpha", []edit{{"maxSizePerMsg: 4096", "maxSizePerMsg: 0"}}, "raft.maxSizePerMsg"},
		{"dockMsなし", "alpha", []edit{{"dockMs: 1500, ", ""}}, "transport.dockMs"},
		{"portCap=0", "alpha", []edit{{"portCap: 24", "portCap: 0"}}, "transport.portCap"},
		{"perLinkAppCap=0", "alpha", []edit{{"perLinkAppCap: 4", "perLinkAppCap: 0"}}, "transport.perLinkAppCap"},
		{"relayCapなし", "alpha", []edit{{", relayCap: 12", ""}}, "transport.relayCap"},
		// 資源
		{"資源なし", "alpha", []edit{{resources, "resources: []\n"}}, "resources"},
		{"資源id重複", "alpha", []edit{{`id: "stall:main", kind: stall`, `id: "firework:a", kind: stall`}}, "resources[1].id"},
		{"資源budget", "alpha", []edit{{"kind: firework, label", "kind: budget, label"}}, "resources[0].kind"},
		{"fireworkにcapacity", "alpha", []edit{{"critical: true }", "critical: true, capacity: 1 }"}}, "resources[0].capacity"},
		{"stallのcapacityなし", "alpha", []edit{{", capacity: 2 }", " }"}}, "resources[1].capacity"},
		{"stallのcapacity=0", "alpha", []edit{{"capacity: 2 }", "capacity: 0 }"}}, "resources[1].capacity"},
		{"資源label空", "alpha", []edit{{"label: 大花火", `label: ""`}}, "resources[0].label"},
		// 依頼
		{"normal=0", "alpha", []edit{{"normal: 60000", "normal: 0"}}, "requests.deadlineMs.normal"},
		{"criticalなし", "alpha", []edit{{", critical: 90000", ""}}, "requests.deadlineMs.critical"},
		{"retryMs=0", "alpha", []edit{{"retryMs: 25000", "retryMs: 0"}}, "requests.retryMs"},
		{"at超過", "alpha", []edit{{"at: 5000", "at: 180001"}}, "requests.script[0].at"},
		{"script島", "alpha", []edit{{"island: hama-todai, kind", "island: nowhere, kind"}}, "requests.script[0].island"},
		{"script種類", "alpha", []edit{{script0, `{ at: 5000, island: hama-todai, kind: budget.spend, resource: "firework:a" }`}}, "requests.script[0].kind"},
		{"script資源なし", "alpha", []edit{{script0, `{ at: 5000, island: hama-todai, kind: firework.reserve, resource: "firework:z" }`}}, "requests.script[0].resource"},
		{"script資源の種類", "alpha", []edit{{script0, `{ at: 5000, island: hama-todai, kind: firework.reserve, resource: "stall:main" }`}}, "requests.script[0].resource"},
		{"slotMs=0", "alpha", []edit{{"slotMs: 10000", "slotMs: 0"}}, "requests.random.slotMs"},
		{"kinds空", "alpha", []edit{{`kinds: [ { kind: stall.reserve, resource: "stall:main", weight: 1 } ]`, "kinds: []"}}, "requests.random.kinds"},
		{"指数0.7", "alpha", []edit{{"populationExponent: 0.5", "populationExponent: 0.7"}}, "requests.random.populationExponent"},
		{"指数なし", "alpha", []edit{{"    populationExponent: 0.5\n", ""}}, "requests.random.populationExponent"},
		{"weight=0", "alpha", []edit{{"weight: 1", "weight: 0"}}, "requests.random.kinds[0].weight"},
		{"kinds資源", "alpha", []edit{{`resource: "stall:main", weight: 1`, `resource: "firework:a", weight: 1`}}, "requests.random.kinds[0].resource"},
		{"rate過大", "alpha", []edit{{"rateMilliPerMin: 500", "rateMilliPerMin: 7000"}}, "requests.random.rateMilliPerMin"},
		{"rateなし", "alpha", []edit{{"    rateMilliPerMin: 500\n", ""}}, "requests.random.rateMilliPerMin"},
		// イベント（beta）
		{"kind未知", "beta", []edit{{"kind: storm", "kind: quake"}}, "events[0].kind"},
		{"atとwindow", "beta", []edit{{"    at: 60000\n", "    at: 60000\n    window: [1, 2]\n"}}, "events[0].window"},
		{"atもwindowもない", "beta", []edit{{"    at: 60000\n", ""}}, "events[0].at"},
		{"at超過", "beta", []edit{{"at: 60000", "at: 240001"}}, "events[0].at"},
		{"window逆", "beta", []edit{{"    at: 60000\n", "    window: [5000, 3000]\n"}}, "events[0].window"},
		{"window3要素", "beta", []edit{{"    at: 60000\n", "    window: [1000, 2000, 3000]\n"}}, "events[0].window"},
		{"window超過", "beta", []edit{{"window: [80000, 120000]", "window: [80000, 240001]"}}, "events[2].window"},
		{"storm durationMsなし", "beta", []edit{{"    at: 60000\n    durationMs: 20000\n", "    at: 60000\n"}}, "events[0].durationMs"},
		{"routes空", "beta", []edit{{stormRoutes, "routes: []"}}, "events[0].routes"},
		{"routesなし", "beta", []edit{{"    " + stormRoutes + "\n", ""}}, "events[0].routes"},
		{"航路のない組", "beta", []edit{{stormRoutes, "routes: [[hama-todai, hama-hoshizuna]]"}}, "events[0].routes[0]"},
		{"未知の島の組", "beta", []edit{{stormRoutes, "routes: [[hama-todai, nowhere]]"}}, "events[0].routes[0]"},
		{"3つの組", "beta", []edit{{stormRoutes, "routes: [[hama-todai, hama-tsukikage, hama-hoshizuna]]"}}, "events[0].routes[0]"},
		{"航路の重複", "beta", []edit{{stormRoutes, "routes: [[hama-tsukikage, hama-hoshizuna], [hama-hoshizuna, hama-tsukikage]]"}}, "events[0].routes[1]"},
		{"forecast超過", "beta", []edit{{"forecastMs: 10000", "forecastMs: 60001"}}, "events[0].forecastMs"},
		{"forecastなし", "beta", []edit{{"    forecastMs: 10000\n", ""}}, "events[0].forecastMs"},
		{"stormにisland", "beta", []edit{{"    forecastMs: 10000\n", "    forecastMs: 10000\n    island: hama-todai\n"}}, "events[0].island"},
		{"flavorのsourceなし", "beta", []edit{{flavorSrc, ""}}, "events[0].flavor.source"},
		{"cancellationにat", "beta", []edit{{"  - kind: cancellation\n", "  - kind: cancellation\n    at: 1000\n"}}, "events[1].at"},
		{"cancellationにforecast", "beta", []edit{{"  - kind: cancellation\n", "  - kind: cancellation\n    forecastMs: 1000\n"}}, "events[1].forecastMs"},
		{"probability過大", "beta", []edit{{"probabilityPermille: 170", "probabilityPermille: 1001"}}, "events[1].probabilityPermille"},
		{"cancel slotMs=0", "beta", []edit{{"slotMs: 30000", "slotMs: 0"}}, "events[1].slotMs"},
		{"cancel provenanceなし", "beta", []edit{{cancelProv, "    source: { dataset: fixture-2026, region: 灯台地域, field: DISASTER_LAND_CONSERVATION }"}}, "events[1].provenance"},
		{"cancel islandなし", "beta", []edit{{"    island: hama-todai\n", ""}}, "events[1].island"},
		{"cancel derivedのsourceなし", "beta", []edit{{cancelProv, "    provenance: derived"}}, "events[1].source"},
		{"breakdown ferry", "beta", []edit{{"ferry: any", "ferry: f9"}}, "events[2].ferry"},
		{"breakdownにletters", "beta", []edit{{"    ferry: any\n", "    ferry: any\n    letters: 1\n"}}, "events[2].letters"},
		{"rush requests=0", "beta", []edit{{"requests: 4", "requests: 0"}}, "events[3].requests"},
		{"rushにdurationMs", "beta", []edit{{"    requests: 4\n", "    requests: 4\n    durationMs: 1\n"}}, "events[3].durationMs"},
		{"drift letters=0", "beta", []edit{{"letters: 3", "letters: 0"}}, "events[4].letters"},
		{"drift minAgeMsなし", "beta", []edit{{"\n    minAgeMs: 30000", ""}}, "events[4].minAgeMs"},
		// デモ（beta）
		{"endOnAllCriticalSecuredなし", "beta", []edit{{"  endOnAllCriticalSecured: true\n", ""}}, "demo.endOnAllCriticalSecured"},
		{"demo航路のない組", "beta", []edit{{"routes: [[shiomi-ooishi, hama-hoshizuna]]", "routes: [[shiomi-koishi, hama-hoshizuna]]"}}, "demo.storm.routes[0]"},
		{"demo routesなし", "beta", []edit{{"    routes: [[shiomi-ooishi, hama-hoshizuna]]\n", ""}}, "demo.storm.routes"},
		{"demo durationMs=0", "beta", []edit{{"    routes: [[shiomi-ooishi, hama-hoshizuna]]\n    durationMs: 20000", "    routes: [[shiomi-ooishi, hama-hoshizuna]]\n    durationMs: 0"}}, "demo.storm.durationMs"},
		{"demo flavor", "beta", []edit{{"      provenance: creative\n\nscoring", "      provenance: nope\n\nscoring"}}, "demo.storm.flavor.provenance"},
		// 得点・満足度・速度
		{"scoringなし", "alpha", []edit{{"scoring:\n  target: { successes: 2, score3: 500 }\n", ""}}, "scoring"},
		{"successes=0", "alpha", []edit{{"successes: 2", "successes: 0"}}, "scoring.target.successes"},
		{"score3なし", "alpha", []edit{{", score3: 500", ""}}, "scoring.target.score3"},
		{"initial=101", "alpha", []edit{{"initial: 60", "initial: 101"}}, "satisfaction.initial"},
		{"silenceAfterMs=0", "alpha", []edit{{"silenceAfterMs: 30000", "silenceAfterMs: 0"}}, "satisfaction.silenceAfterMs"},
		{"everyMs=0", "alpha", []edit{{"everyMs: 10000", "everyMs: 0"}}, "satisfaction.silencePenalty.everyMs"},
		{"amountなし", "alpha", []edit{{", amount: 2", ""}}, "satisfaction.silencePenalty.amount"},
		{"silencePenaltyなし", "alpha", []edit{{"  silencePenalty: { everyMs: 10000, amount: 2 }\n", ""}}, "satisfaction.silencePenalty"},
		{"satisfactionなし", "alpha", []edit{{"satisfaction:\n  initial: 60\n  silenceAfterMs: 30000\n  silencePenalty: { everyMs: 10000, amount: 2 }\n", ""}}, "satisfaction"},
		{"allowed空", "alpha", []edit{{"allowed: [0.5, 1, 2, 4]", "allowed: []"}}, "speed.allowed"},
		{"allowed降順", "alpha", []edit{{"allowed: [0.5, 1, 2, 4]", "allowed: [1, 0.5]"}}, "speed.allowed[1]"},
		{"allowed同値", "alpha", []edit{{"allowed: [0.5, 1, 2, 4]", "allowed: [1, 1]"}}, "speed.allowed[1]"},
		{"allowedに1なし", "alpha", []edit{{"allowed: [0.5, 1, 2, 4]", "allowed: [0.5, 2]"}}, "speed.allowed"},
		{"allowedに0", "alpha", []edit{{"allowed: [0.5, 1, 2, 4]", "allowed: [0, 1]"}}, "speed.allowed[0]"},
		{"speedなし", "alpha", []edit{{"speed:\n  allowed: [0.5, 1, 2, 4]\n", ""}}, "speed"},
	}
	for _, tc := range cases {
		t.Run(tc.base+"/"+tc.name, func(t *testing.T) {
			t.Parallel()
			data := applyEdits(t, fixtures[tc.base], tc.edits...)
			st, err := Parse("stages/x.yaml", []byte(data), is)
			if st != nil {
				t.Fatal("エラーなのに *Stage を返した")
			}
			requireIssue(t, err, "stages/x.yaml", tc.path)
			// 1 つの欄には Issue を最大 1 件だけ出す。
			seen := map[string]bool{}
			for _, iss := range asError(t, err).Issues {
				if seen[iss.Path] {
					t.Errorf("Path %q の Issue が 2 件以上ある: %v", iss.Path, err)
				}
				seen[iss.Path] = true
			}
		})
	}
}

// 必須のマッピングがないときは、そのマッピングの Path で 1 件だけ出す。
func TestParseMissingMappings(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	alpha := readTestdata(t, "stages/alpha.yaml")
	cases := []struct {
		edits []edit
		path  string
	}{
		{[]edit{{"raft:\n  initialLeader: hama-hoshizuna\n  tickMs: 500\n  heartbeatTick: 2\n  electionTick: 40\n  checkQuorum: true\n  preVote: true\n  maxInflightMsgs: 4\n  maxSizePerMsg: 4096\n", ""}}, "raft"},
		{[]edit{{"transport: { dockMs: 1500, perLinkAppCap: 4, portCap: 24, relayCap: 12 }\n", ""}}, "transport"},
		{[]edit{{"  deadlineMs: { normal: 60000, critical: 90000 }\n", ""}}, "requests.deadlineMs"},
		{[]edit{{"  random:\n    rateMilliPerMin: 500\n    slotMs: 10000\n    populationExponent: 0.5\n    kinds: [ { kind: stall.reserve, resource: \"stall:main\", weight: 1 } ]\n", ""}}, "requests.random"},
		{[]edit{{"  target: { successes: 2, score3: 500 }\n", "  target: ~\n"}}, "scoring.target"},
	}
	for _, tc := range cases {
		_, err := Parse("a.yaml", []byte(applyEdits(t, alpha, tc.edits...)), is)
		se := asError(t, err)
		if len(se.Issues) != 1 || se.Issues[0].Path != tc.path {
			t.Errorf("%s: Issues = %+v", tc.path, se.Issues)
		}
	}
}

// 全島の人口が 0 だと重みを決められない。
func TestParseZeroPopulation(t *testing.T) {
	t.Parallel()
	islandsJSON := applyEdits(t, readTestdata(t, "islands.json"),
		edit{`"value": 19134,`, `"value": 0,`}, edit{`"populationSqrt": 138.33,`, `"populationSqrt": 0,`},
		edit{`"value": 768,`, `"value": 0,`}, edit{`"populationSqrt": 27.71,`, `"populationSqrt": 0,`},
		edit{`"value": 169,`, `"value": 0,`}, edit{`"populationSqrt": 13,`, `"populationSqrt": 0,`},
	)
	is, err := ParseIslands("islands.json", []byte(islandsJSON))
	if err != nil {
		t.Fatal(err)
	}
	st, err := Parse("stages/alpha.yaml", []byte(readTestdata(t, "stages/alpha.yaml")), is)
	if st != nil {
		t.Fatal("エラーなのに *Stage を返した")
	}
	requireIssue(t, err, "stages/alpha.yaml", "islands")
}

// P-4 複数の誤り。
func TestParseMultipleIssues(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	data := []byte(applyEdits(t, readTestdata(t, "stages/alpha.yaml"),
		edit{"initial: 60", "initial: 101"},
		edit{"format: 1", "format: 2"},
		edit{"travelMs: 1500", "travelMs: 0"},
	))
	_, err1 := Parse("a.yaml", data, is)
	_, err2 := Parse("a.yaml", data, is)
	se := asError(t, err1)
	var paths []string
	for _, iss := range se.Issues {
		paths = append(paths, iss.Path)
	}
	if want := []string{"format", "routes[0].travelMs", "satisfaction.initial"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("Paths = %v, want %v", paths, want)
	}
	if err2 == nil || err1.Error() != err2.Error() {
		t.Errorf("Error() が毎回同じでない:\n%v\n%v", err1, err2)
	}
}

// P-5 警告。
func TestParseElectionWarning(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "stages/alpha.yaml"), edit{"electionTick: 40", "electionTick: 8"})
	st, err := Parse("a.yaml", []byte(data), fixtureIslands(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Warnings) != 1 || st.Warnings[0].Path != "raft.electionTick" || st.Warnings[0].Message == "" {
		t.Errorf("Warnings = %+v", st.Warnings)
	}
}

// P-6 指数。
func TestParsePopulationExponent(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	alpha := readTestdata(t, "stages/alpha.yaml")
	for exp, want := range map[string][]int64{"0": {334, 333, 333}, "0.5": {773, 155, 72}, "1": {953, 38, 9}, "1.0": {953, 38, 9}} {
		data := applyEdits(t, alpha, edit{"populationExponent: 0.5", "populationExponent: " + exp})
		st, err := Parse("a.yaml", []byte(data), is)
		if err != nil {
			t.Fatalf("%s: %v", exp, err)
		}
		if got := weightsOf(st); !reflect.DeepEqual(got, want) {
			t.Errorf("populationExponent %s: 重み = %v, want %v", exp, got, want)
		}
		if st.Requests.Random.PopulationExponentPermille != map[string]int64{"0": 0, "0.5": 500, "1": 1000, "1.0": 1000}[exp] {
			t.Errorf("PopulationExponentPermille = %d", st.Requests.Random.PopulationExponentPermille)
		}
	}
}

// P-7 ハッシュ。
func TestParseHash(t *testing.T) {
	t.Parallel()
	islandsJSON := []byte(readTestdata(t, "islands.json"))
	alpha := []byte(readTestdata(t, "stages/alpha.yaml"))
	is := fixtureIslands(t)
	hash := func(t *testing.T, data []byte, is *Islands) string {
		t.Helper()
		st, err := Parse("a.yaml", data, is)
		if err != nil {
			t.Fatal(err)
		}
		return st.Hash
	}
	h := hash(t, alpha, is)
	if h2 := hash(t, alpha, is); h2 != h {
		t.Errorf("同じ入力で Hash が違う: %s %s", h, h2)
	}
	if h2 := hash(t, append([]byte("# コメント\n"), alpha...), is); h2 == h {
		t.Error("コメントを足しても Hash が変わらない")
	}
	is2, err := ParseIslands("islands.json", append(append([]byte{}, islandsJSON...), ' ', '\n'))
	if err != nil {
		t.Fatal(err)
	}
	if h2 := hash(t, alpha, is2); h2 == h {
		t.Error("islands.json に空白を足しても Hash が変わらない")
	}
	y := sha256.Sum256(alpha)
	j := sha256.Sum256(islandsJSON)
	sum := sha256.Sum256(append(y[:], j[:]...))
	if want := "sha256:" + hex.EncodeToString(sum[:]); h != want {
		t.Errorf("Hash = %s, want %s", h, want)
	}
}

// P-8 並行。
func TestParseConcurrent(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	inputs := [][]byte{[]byte(readTestdata(t, "stages/alpha.yaml")), []byte(readTestdata(t, "stages/beta.yaml"))}
	copies := [][]byte{bytes.Clone(inputs[0]), bytes.Clone(inputs[1])}
	want := make([]*Stage, len(inputs))
	for i, in := range inputs {
		st, err := Parse("s.yaml", in, is)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = st
	}
	const workers = 8
	got := make([][]*Stage, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				for _, in := range inputs {
					st, err := Parse("s.yaml", in, is)
					if err != nil {
						t.Error(err)
						return
					}
					got[w] = append(got[w], st)
				}
			}
		}()
	}
	wg.Wait()
	for w := range workers {
		for k, st := range got[w] {
			if !reflect.DeepEqual(st, want[k%len(inputs)]) {
				t.Errorf("worker %d の結果 %d が違う", w, k)
			}
		}
	}
	for i := range inputs {
		if !bytes.Equal(inputs[i], copies[i]) {
			t.Errorf("入力 %d のバイト列が変わった", i)
		}
	}
	if fresh := fixtureIslands(t); !reflect.DeepEqual(is, fresh) {
		t.Error("*Islands が変わった")
	}
}

// どの入力でも panic しない（壊れた YAML・型の違う値）。
func TestParseNoPanic(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	for _, src := range []string{
		"~\n", "format: 1\n", "events: [{kind: storm, window: []}]\n", "events: [{kind: storm, routes: [[]]}]\n",
		"events: [{kind: breakdown, ferry: ''}]\n", "demo: {storm: {routes: [[a]]}}\n", "islands: [{}]\nroutes: [{}]\n",
		"speed: {allowed: [~]}\n", "requests: {script: [{}], random: {kinds: [{}]}}\n",
	} {
		if _, err := Parse("p.yaml", []byte(src), is); err == nil {
			t.Errorf("%q: エラーにならない", src)
		}
	}
	if _, err := Parse("p.yaml", []byte("format: 1\n"), nil); err == nil {
		t.Error("is が nil でエラーにならない")
	}
}
