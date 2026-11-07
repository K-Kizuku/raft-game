package stage_test

import (
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// リポジトリのステージ定義 data/stages/*.yaml の中身が docs のとおりかを確かめる（T-207。データ仕様 §5.6）。
// 期待値は docs（進行表 §1・§3・§4・§8、データ仕様 §4.1・§5.2・§5.3・§5.5、ゲームデザイン §4.2・§5）の値を写したもの。

// loadRepoStages はリポジトリの data/islands.json と data/stages/*.yaml を読む。エラーなら t.Fatal。
func loadRepoStages(t *testing.T) []*stage.Stage {
	t.Helper()
	stages, err := stage.LoadDir(os.DirFS("../../.."), "data/islands.json", "data/stages")
	if err != nil {
		t.Fatal(err)
	}
	return stages
}

// repoStage は、リポジトリのステージのうち ID が id のものを返す。なければ t.Fatalf。
func repoStage(t *testing.T, id string) *stage.Stage {
	t.Helper()
	for _, st := range loadRepoStages(t) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("data/stages にステージ %q がない", id)
	return nil
}

// data/curated の抜き出しの引用（データ仕様 §3.3）。
const (
	quoteSetouchiClimate      = "温暖、小雨の典型的な瀬戸内式気候"               // setouchi-climate
	quoteSetouchiTeshimaTono  = "豊島~小豊島~土庄(8km・25分"              // setouchi-teshima-tonosho-minutes
	quoteSetouchiTakamatsu    = "高松~直島~豊島(22.6km・35分"            // setouchi-takamatsu-naoshima-teshima-minutes
	quoteNagasakiHeritage     = "世界遺産登録された「長崎と天草地方の潜伏キリシタン関連遺産」" // nagasaki-world-heritage
	quoteNagasakiTyphoon      = "台風等の自然災害の影響を受けやすい"              // nagasaki-typhoon
	quoteIzuCancellation      = "令和2(2020)年利島17%、御蔵島・青ヶ島41%"     // izu-cancellation-2020
	quoteIzuTyphoon2013       = "平成25年台風26号による大島町の被害"            // izu-typhoon-2013
	repoStageDataset          = "mlit-ritou-2026"
	repoStageRegionShodoshima = "小豆島地域"
)

type wantRoute struct {
	A, B       uint64
	TravelMs   int64
	Provenance stage.Provenance
}

type wantFerry struct {
	ID            string
	Name          string
	Home          uint64
	Capacity      int
	SpeedPermille int64
	Provenance    stage.Provenance
}

type wantStage struct {
	id              string
	title           string
	durationMs      int64
	unlock          string
	tutorial        string
	refs            []string
	weights         []int64
	electionTick    int
	routes          []wantRoute
	ferries         []wantFerry
	critical        []string
	stallCapacity   int
	script          []stage.ScriptRequest
	rateMilliPerMin int64
	targetSuccesses int64
	targetScore3    int64
}

func sr(at int64, island uint64, kind, resource string) stage.ScriptRequest {
	return stage.ScriptRequest{At: at, Island: island, Kind: kind, Resource: resource}
}

const (
	fw    = stage.RequestFireworkReserve
	stall = stage.RequestStallReserve
	fwA   = "firework:a"
	fwB   = "firework:b"
	stMn  = "stall:main"
)

var (
	gotoRefs    = []string{"nagasaki-fukue", "nagasaki-naru", "nagasaki-nakadori"}
	gotoWeights = []int64{511, 126, 363}
	gotoFerries = []wantFerry{
		{"f1", "ごとう丸", 1, 4, 1000, stage.Creative},
		{"f2", "しまかぜ", 2, 3, 800, stage.Creative},
	}
)

var wantStages = []wantStage{
	{
		id: "stage1", title: "はじめての郵便船", durationMs: 180000, unlock: "", tutorial: "stage1",
		refs:         []string{"kagawa-shodoshima", "kagawa-teshima", "kagawa-naoshima"},
		weights:      []int64{659, 114, 227},
		electionTick: 120,
		routes: []wantRoute{
			{1, 2, 3750, stage.Derived},
			{2, 3, 5250, stage.Derived},
		},
		ferries:       []wantFerry{{"f1", "せとうち丸", 1, 4, 1000, stage.Creative}},
		critical:      []string{fwA},
		stallCapacity: 5,
		script: []stage.ScriptRequest{
			sr(5000, 1, stall, stMn),
			sr(30000, 3, fw, fwA),
			sr(60000, 2, stall, stMn),
			sr(80000, 1, stall, stMn),
			sr(105000, 3, stall, stMn),
			sr(130000, 2, stall, stMn),
		},
		rateMilliPerMin: 0, targetSuccesses: 3, targetScore3: 900, // 目標は novice で決める（T-501f #311）
	},
	{
		id: "stage2", title: "島から返事が来ない！", durationMs: 210000, unlock: "stage1", tutorial: "",
		refs: gotoRefs, weights: gotoWeights, electionTick: 40,
		routes: []wantRoute{
			{1, 2, 4000, stage.Creative},
			{2, 3, 6000, stage.Creative},
			{1, 3, 9000, stage.Creative},
		},
		ferries:       gotoFerries,
		critical:      []string{fwA, fwB},
		stallCapacity: 3,
		script: []stage.ScriptRequest{
			sr(10000, 3, stall, stMn),
			sr(20000, 1, fw, fwA),
			sr(35000, 2, stall, stMn),
			sr(45000, 3, fw, fwA),
			sr(110000, 2, fw, fwB),
			sr(120000, 3, stall, stMn),
			sr(140000, 1, stall, stMn),
			sr(160000, 2, stall, stMn),
			sr(180000, 3, stall, stMn),
		},
		rateMilliPerMin: 500, targetSuccesses: 2, targetScore3: 1400, // 目標は novice で決める（T-501g #332）
	},
	{
		id: "demo-izu", title: "嵐の離島共和国（デモ・伊豆版）", durationMs: 120000, unlock: "", tutorial: "",
		refs:         []string{"tokyo-oshima", "tokyo-toshima", "tokyo-miyakejima", "tokyo-mikurajima", "tokyo-hachijojima", "tokyo-aogashima"},
		weights:      []int64{318, 68, 180, 68, 317, 49},
		electionTick: 28, // T-501e で 16 から 28 に
		// 6 島のすべての組（フルメッシュ。ゲームデザイン §3.5）。65 + 1.1 × 配置の距離、10 ms 単位（データ仕様 §4.5）。
		routes: []wantRoute{
			{1, 2, 220, stage.Creative},
			{1, 3, 260, stage.Creative},
			{1, 4, 380, stage.Creative},
			{1, 5, 580, stage.Creative},
			{1, 6, 730, stage.Creative},
			{2, 3, 210, stage.Creative},
			{2, 4, 290, stage.Creative},
			{2, 5, 480, stage.Creative},
			{2, 6, 620, stage.Creative},
			{3, 4, 190, stage.Creative},
			{3, 5, 390, stage.Creative},
			{3, 6, 540, stage.Creative},
			{4, 5, 270, stage.Creative},
			{4, 6, 410, stage.Creative},
			{5, 6, 210, stage.Creative},
		},
		// 計画書の 6 つの船名をすべて使い、各島に 1 隻（T-501e）。
		ferries: []wantFerry{
			{"f1", "橘丸", 1, 4, 1000, stage.Fact},
			{"f2", "くろしお丸", 3, 3, 1000, stage.Fact},
			{"f3", "あおがしま丸", 5, 3, 1000, stage.Fact},
			{"f4", "フェリーあぜりあ", 4, 3, 1000, stage.Fact},
			{"f5", "新さるびあ丸", 2, 3, 1000, stage.Fact},
			{"f6", "セブンアイランド結", 6, 3, 1000, stage.Fact},
		},
		critical:      []string{fwA, fwB},
		stallCapacity: 3,
		script: []stage.ScriptRequest{
			sr(1000, 2, fw, fwA),
			sr(24000, 1, stall, stMn),
			sr(26000, 2, stall, stMn),
			sr(45000, 5, fw, fwB),
		},
		rateMilliPerMin: 0, targetSuccesses: 2, targetScore3: 700,
	},
	{
		id: "demo-goto", title: "嵐の五島列島（デモ・五島版）", durationMs: 120000, unlock: "", tutorial: "",
		refs: gotoRefs, weights: gotoWeights, electionTick: 32,
		routes: []wantRoute{
			{1, 2, 2400, stage.Creative},
			{2, 3, 1800, stage.Creative},
			{1, 3, 5400, stage.Creative},
		},
		// Stage 2 の 2 隻に、嵐の間も奈留島・中通島の側に残る f3 を足す（T-501b）。
		ferries: append(append([]wantFerry{}, gotoFerries...),
			wantFerry{"f3", "なかどおり丸", 3, 3, 1000, stage.Creative}),
		critical:      []string{fwA, fwB},
		stallCapacity: 3,
		script: []stage.ScriptRequest{
			sr(1000, 2, fw, fwA),
			sr(24000, 1, stall, stMn),
			sr(45000, 3, fw, fwB),
		},
		rateMilliPerMin: 0, targetSuccesses: 2, targetScore3: 700,
	},
}

// ST-1 リポジトリのステージが読める。ID の列が demo-goto・demo-izu・stage1・stage2 をこの順で含む。
func TestRepoStagesLoad(t *testing.T) {
	t.Parallel()
	stages := loadRepoStages(t)
	want := []string{"demo-goto", "demo-izu", "stage1", "stage2"}
	var got []string
	for _, st := range stages {
		if slices.Contains(want, st.ID) {
			got = append(got, st.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		var all []string
		for _, st := range stages {
			all = append(all, st.ID)
		}
		t.Fatalf("ID の列 = %q、%q をこの順で含まない", all, want)
	}
}

// ST-2 docs の値。
func TestRepoStagesValues(t *testing.T) {
	t.Parallel()
	stages := loadRepoStages(t)
	byID := map[string]*stage.Stage{}
	for _, st := range stages {
		byID[st.ID] = st
	}
	for _, w := range wantStages {
		t.Run(w.id, func(t *testing.T) {
			t.Parallel()
			st, ok := byID[w.id]
			if !ok {
				t.Fatalf("ステージ %q がない", w.id)
			}
			if st.Title != w.title {
				t.Errorf("Title = %q, want %q", st.Title, w.title)
			}
			if st.DurationMs != w.durationMs {
				t.Errorf("DurationMs = %d, want %d", st.DurationMs, w.durationMs)
			}
			if st.Unlock != w.unlock {
				t.Errorf("Unlock = %q, want %q", st.Unlock, w.unlock)
			}
			if st.Tutorial != w.tutorial {
				t.Errorf("Tutorial = %q, want %q", st.Tutorial, w.tutorial)
			}

			var refs []string
			var weights []int64
			for _, isl := range st.Islands {
				refs = append(refs, isl.Ref)
				weights = append(weights, isl.WeightPermille)
			}
			if !reflect.DeepEqual(refs, w.refs) {
				t.Errorf("島の Ref = %q, want %q", refs, w.refs)
			}
			if !reflect.DeepEqual(weights, w.weights) {
				t.Errorf("島の WeightPermille = %v, want %v", weights, w.weights)
			}

			r := st.Raft
			if r.InitialLeader != 1 || r.TickMs != 500 || r.HeartbeatTick != 2 || r.ElectionTick != w.electionTick ||
				!r.CheckQuorum || !r.PreVote {
				t.Errorf("Raft = %+v, want InitialLeader 1・TickMs 500・HeartbeatTick 2・ElectionTick %d・CheckQuorum・PreVote", r, w.electionTick)
			}
			if r.MaxInflightMsgs != 4 || r.MaxSizePerMsg != 4096 {
				t.Errorf("Raft = %+v, want MaxInflightMsgs 4・MaxSizePerMsg 4096", r)
			}

			var routes []wantRoute
			for _, rt := range st.Routes {
				routes = append(routes, wantRoute{rt.A, rt.B, rt.TravelMs, rt.Provenance})
			}
			if !reflect.DeepEqual(routes, w.routes) {
				t.Errorf("航路 = %+v, want %+v", routes, w.routes)
			}

			var ferries []wantFerry
			for _, f := range st.Ferries {
				ferries = append(ferries, wantFerry{f.ID, f.Name, f.Home, f.Capacity, f.SpeedPermille, f.Provenance})
			}
			if !reflect.DeepEqual(ferries, w.ferries) {
				t.Errorf("船 = %+v, want %+v", ferries, w.ferries)
			}

			var critical []string
			stallCap := -1
			for _, res := range st.Resources {
				if res.Critical {
					critical = append(critical, res.ID)
				}
				if res.ID == stMn {
					stallCap = res.Capacity
				}
			}
			if !reflect.DeepEqual(critical, w.critical) {
				t.Errorf("重要な資源 = %q, want %q", critical, w.critical)
			}
			if stallCap != w.stallCapacity {
				t.Errorf("屋台の定員 = %d, want %d", stallCap, w.stallCapacity)
			}

			if !reflect.DeepEqual(st.Requests.Script, w.script) {
				t.Errorf("台本 = %+v, want %+v", st.Requests.Script, w.script)
			}
			if got := st.Requests.Random.RateMilliPerMin; got != w.rateMilliPerMin {
				t.Errorf("Random.RateMilliPerMin = %d, want %d", got, w.rateMilliPerMin)
			}
			if got := st.Requests.Random.PopulationExponentPermille; got != 500 {
				t.Errorf("Random.PopulationExponentPermille = %d, want 500", got)
			}
			if st.Scoring.TargetSuccesses != w.targetSuccesses || st.Scoring.TargetScore3 != w.targetScore3 {
				t.Errorf("Scoring.Target = (%d, %d), want (%d, %d)",
					st.Scoring.TargetSuccesses, st.Scoring.TargetScore3, w.targetSuccesses, w.targetScore3)
			}

			// 4 つのステージに共通する値（データ仕様 §5.2、ゲームデザイン §4.2・§4.3・§5.1・§5.2）。
			rq := st.Requests
			if rq.DeadlineNormalMs != 60000 || rq.DeadlineCriticalMs != 90000 || rq.RetryMs != 25000 {
				t.Errorf("Requests = (%d, %d, %d), want (60000, 90000, 25000)", rq.DeadlineNormalMs, rq.DeadlineCriticalMs, rq.RetryMs)
			}
			if want := (stage.Transport{DockMs: 1500, PerLinkAppCap: 4, PortCap: 24, RelayCap: 12}); st.Transport != want {
				t.Errorf("Transport = %+v, want %+v", st.Transport, want)
			}
			wantScoring := stage.Scoring{
				TargetSuccesses: w.targetSuccesses, TargetScore3: w.targetScore3,
				PointsNormal: 100, PointsCritical: 300, PointsLate: 30, PointsRejected: 0, PointsExpiredPenalty: 50,
				FerryCostMsPerPoint: 2000, FairnessPerSatisfaction: 5,
			}
			if st.Scoring != wantScoring {
				t.Errorf("Scoring = %+v, want %+v", st.Scoring, wantScoring)
			}
			wantSat := stage.Satisfaction{
				Initial: 60, SilenceAfterMs: 30000, SilencePenaltyEveryMs: 10000, SilencePenaltyAmount: 2,
				Succeeded: 10, RejectedPenalty: 3, ExpiredPenalty: 10,
			}
			if st.Satisfaction != wantSat {
				t.Errorf("Satisfaction = %+v, want %+v", st.Satisfaction, wantSat)
			}
			if want := []int64{500, 1000, 2000, 4000}; !reflect.DeepEqual(st.SpeedAllowedPermille, want) {
				t.Errorf("SpeedAllowedPermille = %v, want %v", st.SpeedAllowedPermille, want)
			}
		})
	}
}

// ST-3 出典。どの抜き出しを使うかを固定する（LoadDir の検査と二重）。
func TestRepoStagesSources(t *testing.T) {
	t.Parallel()
	stages := loadRepoStages(t)
	byID := map[string]*stage.Stage{}
	for _, st := range stages {
		byID[st.ID] = st
	}
	get := func(t *testing.T, id string) *stage.Stage {
		t.Helper()
		st, ok := byID[id]
		if !ok {
			t.Fatalf("ステージ %q がない", id)
		}
		return st
	}
	checkSource := func(t *testing.T, what string, src *stage.Source, region, field, quote string) {
		t.Helper()
		want := stage.Source{Dataset: repoStageDataset, Region: region, Field: field, Quote: quote}
		if src == nil {
			t.Errorf("%s: Source = nil, want %+v", what, want)
			return
		}
		if *src != want {
			t.Errorf("%s: Source = %+v, want %+v", what, *src, want)
		}
	}
	const (
		regionGoto = "離島振興対策実施地域"
		regionIzu  = "伊豆諸島地域"
	)

	t.Run("stage1", func(t *testing.T) {
		t.Parallel()
		st := get(t, "stage1")
		checkSource(t, "intro", st.Intro.Source, repoStageRegionShodoshima, "NATURAL_CLIMATE_FEATURES", quoteSetouchiClimate)
		if len(st.Routes) != 2 {
			t.Fatalf("航路の数 = %d, want 2", len(st.Routes))
		}
		checkSource(t, "routes[0]", st.Routes[0].Source, repoStageRegionShodoshima, "MARITIME_TRANSPORT", quoteSetouchiTeshimaTono)
		checkSource(t, "routes[1]", st.Routes[1].Source, repoStageRegionShodoshima, "MARITIME_TRANSPORT", quoteSetouchiTakamatsu)
	})
	t.Run("stage2", func(t *testing.T) {
		t.Parallel()
		st := get(t, "stage2")
		checkSource(t, "intro", st.Intro.Source, regionGoto, "LOCAL_INDUSTRY_TOURISM", quoteNagasakiHeritage)
		for i, rt := range st.Routes {
			if rt.Source != nil {
				t.Errorf("routes[%d]: Source = %+v, want nil（creative）", i, *rt.Source)
			}
		}
		if len(st.Events) != 1 || st.Events[0].Flavor == nil {
			t.Fatalf("Events = %+v, want flavor のある嵐 1 つ", st.Events)
		}
		checkSource(t, "events[0].flavor", st.Events[0].Flavor.Source, regionGoto, "NATURAL_CLIMATE_FEATURES", quoteNagasakiTyphoon)
	})
	t.Run("demo-izu", func(t *testing.T) {
		t.Parallel()
		st := get(t, "demo-izu")
		checkSource(t, "intro", st.Intro.Source, regionIzu, "MARITIME_TRANSPORT", quoteIzuCancellation)
		// 航路は creative（T-501e）。ヘリコミューターの引用は、つながりの根拠に使わない（データ仕様 §4.5）。
		if len(st.Routes) != 15 {
			t.Fatalf("航路の数 = %d, want 15", len(st.Routes))
		}
		for i, rt := range st.Routes {
			if rt.Source != nil {
				t.Errorf("routes[%d]: Source = %+v, want nil（creative）", i, *rt.Source)
			}
		}
		if st.Demo == nil || st.Demo.Storm == nil || st.Demo.Storm.Flavor == nil {
			t.Fatalf("Demo = %+v, want flavor のある demo.storm", st.Demo)
		}
		checkSource(t, "demo.storm.flavor", st.Demo.Storm.Flavor.Source, regionIzu, "DISASTER_LAND_CONSERVATION", quoteIzuTyphoon2013)
	})
	t.Run("demo-goto", func(t *testing.T) {
		t.Parallel()
		st := get(t, "demo-goto")
		checkSource(t, "intro", st.Intro.Source, regionGoto, "LOCAL_INDUSTRY_TOURISM", quoteNagasakiHeritage)
		for i, rt := range st.Routes {
			if rt.Source != nil {
				t.Errorf("routes[%d]: Source = %+v, want nil（creative）", i, *rt.Source)
			}
		}
		if st.Demo == nil || st.Demo.Storm == nil || st.Demo.Storm.Flavor == nil {
			t.Fatalf("Demo = %+v, want flavor のある demo.storm", st.Demo)
		}
		checkSource(t, "demo.storm.flavor", st.Demo.Storm.Flavor.Source, regionGoto, "NATURAL_CLIMATE_FEATURES", quoteNagasakiTyphoon)
	})
}

// ST-4 警告。4 つのステージとも、なし（demo-goto は T-501b で electionTick を 32 に、demo-izu は T-501e で
// 航路をフルメッシュにして electionTick を 28 にし、警告が消えた。データ仕様 §5.4）。
func TestRepoStagesWarnings(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"stage1", "stage2", "demo-goto", "demo-izu"} {
		if st := repoStage(t, id); len(st.Warnings) != 0 {
			t.Errorf("%s: Warnings = %+v, want なし", id, st.Warnings)
		}
	}
}

// ST-5 イベントとデモ、SimConfig。
func TestRepoStagesEventsAndDemo(t *testing.T) {
	t.Parallel()
	stages := loadRepoStages(t)
	byID := map[string]*stage.Stage{}
	for _, st := range stages {
		byID[st.ID] = st
	}
	get := func(t *testing.T, id string) *stage.Stage {
		t.Helper()
		st, ok := byID[id]
		if !ok {
			t.Fatalf("ステージ %q がない", id)
		}
		return st
	}

	st2 := get(t, "stage2")
	if len(st2.Events) != 1 {
		t.Fatalf("stage2: Events = %+v, want 嵐 1 つ", st2.Events)
	}
	ev := st2.Events[0]
	if ev.Kind != stage.EventStorm || ev.When != (stage.Window{Start: 60000, End: 60000}) || ev.DurationMs != 40000 ||
		!reflect.DeepEqual(ev.Routes, [][2]uint64{{1, 2}, {1, 3}}) || ev.ForecastMs != 10000 {
		t.Errorf("stage2: Events[0] = %+v, want storm・When {60000 60000}・40000・[[1 2] [1 3]]・10000", ev)
	}

	for _, id := range []string{"stage1", "demo-izu", "demo-goto"} {
		if st := get(t, id); len(st.Events) != 0 {
			t.Errorf("%s: Events = %+v, want 空", id, st.Events)
		}
	}
	for _, id := range []string{"stage1", "stage2"} {
		if st := get(t, id); st.Demo != nil {
			t.Errorf("%s: Demo = %+v, want nil", id, st.Demo)
		}
	}
	for _, d := range []struct {
		id         string
		routes     [][2]uint64
		durationMs int64
	}{
		{"demo-izu", [][2]uint64{{1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6}}, 40000}, // T-501e で大島を孤立させる 5 本・40 秒に
		{"demo-goto", [][2]uint64{{1, 2}, {1, 3}}, 40000},                        // T-501b で 40 秒に延ばした
	} {
		id, routes, durationMs := d.id, d.routes, d.durationMs
		st := get(t, id)
		if st.Demo == nil || st.Demo.Storm == nil {
			t.Errorf("%s: Demo = %+v, want demo.storm あり", id, st.Demo)
			continue
		}
		if !st.Demo.EndOnAllCriticalSecured {
			t.Errorf("%s: Demo.EndOnAllCriticalSecured = false, want true", id)
		}
		if st.Demo.Storm.DurationMs != durationMs {
			t.Errorf("%s: Demo.Storm.DurationMs = %d, want %d", id, st.Demo.Storm.DurationMs, durationMs)
		}
		if !reflect.DeepEqual(st.Demo.Storm.Routes, routes) {
			t.Errorf("%s: Demo.Storm.Routes = %v, want %v", id, st.Demo.Storm.Routes, routes)
		}
	}

	for _, w := range wantStages {
		st := get(t, w.id)
		cfg, err := stage.SimConfig(st)
		if err != nil {
			t.Errorf("%s: SimConfig: %v", w.id, err)
			continue
		}
		wantStorms := 0
		if w.id == "stage2" {
			wantStorms = 1 // デモの嵐は T-305 が変換する
		}
		if len(cfg.Storms) != wantStorms {
			t.Errorf("%s: len(cfg.Storms) = %d, want %d", w.id, len(cfg.Storms), wantStorms)
		}
		if cfg.Score.TargetSuccesses != w.targetSuccesses {
			t.Errorf("%s: cfg.Score.TargetSuccesses = %d, want %d", w.id, cfg.Score.TargetSuccesses, w.targetSuccesses)
		}
	}
}

// ST-6 demo-izu の航路の形（T-501e。ゲームデザイン §3.5・データ仕様 §4.5）。
// 6 島のすべての組に航路が 1 本ずつあり、travelMs は「65 + 1.1 × 配置（layout）の距離」を 10 ms 単位に丸めた値で、
// 一番短い航路と一番長い航路の比が 4 倍以内。小数を使わないよう、距離は 2 乗のまま整数で比べる。
func TestRepoStagesDemoIzuFullMesh(t *testing.T) {
	t.Parallel()
	st := repoStage(t, "demo-izu")
	n := uint64(len(st.Islands))
	seen := map[[2]uint64]bool{}
	var minMs, maxMs int64
	for i, rt := range st.Routes {
		a, b := min(rt.A, rt.B), max(rt.A, rt.B)
		if seen[[2]uint64{a, b}] {
			t.Errorf("routes[%d]: %d-%d が 2 本ある", i, a, b)
		}
		seen[[2]uint64{a, b}] = true
		ia, ib := st.Islands[a-1], st.Islands[b-1]
		dx, dy := ia.X-ib.X, ia.Y-ib.Y
		d2 := dx*dx + dy*dy
		// |travelMs - 65 - 1.1 × d| ≤ 5 ⇔ (travelMs - 70)² × 100 ≤ 121 × d² ≤ (travelMs - 60)² × 100（travelMs > 70 のとき）
		lo, hi := rt.TravelMs-70, rt.TravelMs-60
		if lo < 0 || lo*lo*100 > 121*d2 || 121*d2 > hi*hi*100 {
			t.Errorf("routes[%d] %d-%d: travelMs = %d, want 65 + 1.1 × 距離（距離の 2 乗 %d）を 10 ms 単位に丸めた値", i, a, b, rt.TravelMs, d2)
		}
		if i == 0 || rt.TravelMs < minMs {
			minMs = rt.TravelMs
		}
		if i == 0 || rt.TravelMs > maxMs {
			maxMs = rt.TravelMs
		}
	}
	if want := int(n * (n - 1) / 2); len(seen) != want {
		t.Errorf("航路のある島の組 = %d, want %d（6 島のすべての組）", len(seen), want)
	}
	if maxMs > 4*minMs {
		t.Errorf("travelMs の最長 %d が最短 %d の 4 倍より長い", maxMs, minMs)
	}
}
