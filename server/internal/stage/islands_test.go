package stage

import (
	"reflect"
	"testing"
)

// I-1 fixture の islands.json を読む。
func TestParseIslandsFixture(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)

	want := []IslandData{
		{ID: "hama-hoshizuna", Name: "星砂島", Prefecture: "波間県", Population: 19134, PopulationSqrtCenti: 13833, PlanRegion: "星砂地域"},
		{ID: "hama-tsukikage", Name: "月影島", Prefecture: "波間県", Population: 768, PopulationSqrtCenti: 2771, PlanRegion: "星砂地域"},
		{ID: "hama-todai", Name: "灯台島", Prefecture: "波間県", Population: 169, PopulationSqrtCenti: 1300, PlanRegion: "灯台地域"},
		{ID: "shiomi-koishi", Name: "小石島", Prefecture: "潮見県", Population: 2, PopulationSqrtCenti: 141},
		{ID: "shiomi-ooishi", Name: "大石島", Prefecture: "潮見県", Population: 5000, PopulationSqrtCenti: 7071},
		{ID: "shiomi-nami", Name: "波島", Prefecture: "潮見県", Population: 300, PopulationSqrtCenti: 1732},
		{ID: "kaze-hayate", Name: "疾風島", Prefecture: "風見県", Population: 1000, PopulationSqrtCenti: 3162},
		{ID: "kaze-nagi", Name: "凪島", Prefecture: "風見県", Population: 0, PopulationSqrtCenti: 0},
	}
	if !reflect.DeepEqual(is.Islands, want) {
		t.Errorf("Islands =\n%+v\nwant\n%+v", is.Islands, want)
	}
	if !reflect.DeepEqual(is.Sources, []string{"fixture-2026"}) {
		t.Errorf("Sources = %v", is.Sources)
	}
	wantEx := []Excerpt{
		{ID: "hoshizuna-climate", Region: "星砂地域", Field: "NATURAL_CLIMATE_FEATURES", Quote: "温暖で、雨の少ない架空の気候"},
		{ID: "todai-storm", Region: "灯台地域", Field: "DISASTER_LAND_CONSERVATION", Quote: "架空の台風で灯台が壊れた"},
		{ID: "tsukikage-hoshizuna-minutes", Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "月影~星砂(3km・10分"},
	}
	if !reflect.DeepEqual(is.Excerpts, wantEx) {
		t.Errorf("Excerpts = %+v", is.Excerpts)
	}
	if d, ok := is.Lookup("shiomi-nami"); !ok || d.Name != "波島" {
		t.Errorf("Lookup(shiomi-nami) = %+v, %v", d, ok)
	}
	if _, ok := is.Lookup("nowhere"); ok {
		t.Error("Lookup(nowhere) が見つかった")
	}
}

// I-2 未知のキーは無視する。
func TestParseIslandsUnknownKeys(t *testing.T) {
	t.Parallel()
	base := fixtureIslands(t)
	data := applyEdits(t, readTestdata(t, "islands.json"),
		edit{`"generatedAt": "2026-11-07",`, `"generatedAt": "2026-11-07", "unknownTop": [1, {"x": 2.5}],`},
		edit{`"id": "hama-hoshizuna",`, `"id": "hama-hoshizuna", "unknownIsland": {"a": 1e3},`},
		edit{`"value": 19134,`, `"value": 19134, "unknownPopulation": "x",`},
	)
	is, err := ParseIslands("islands.json", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(is.Islands, base.Islands) || !reflect.DeepEqual(is.Sources, base.Sources) || !reflect.DeepEqual(is.Excerpts, base.Excerpts) {
		t.Errorf("未知のキーで結果が変わった: %+v", is)
	}
}

// I-3 islands.json の誤り。
func TestParseIslandsErrors(t *testing.T) {
	t.Parallel()
	fixture := readTestdata(t, "islands.json")
	cases := []struct {
		name  string
		raw   string // fixture を使わずに、この文字列を読む
		edits []edit
		path  string
	}{
		{name: "JSONでない", raw: `{"format": 1,`, path: ""},
		{name: "配列", raw: `[]`, path: ""},
		{name: "null", raw: `null`, path: ""},
		{name: "末尾のゴミ", raw: fixture + "x", path: ""},
		{name: "2つの値", raw: fixture + "{}", path: ""},
		{name: "format=2", edits: []edit{{`"format": 1,`, `"format": 2,`}}, path: "format"},
		{name: "format文字列", edits: []edit{{`"format": 1,`, `"format": "1",`}}, path: "format"},
		{name: "format小数", edits: []edit{{`"format": 1,`, `"format": 1.0,`}}, path: "format"},
		{name: "formatなし", edits: []edit{{`"format": 1,`, ``}}, path: "format"},
		{name: "sources空", edits: []edit{{`"sources": {`, `"sources": {}, "oldSources": {`}}, path: "sources"},
		{name: "sourcesなし", edits: []edit{{`"sources": {`, `"noSources": {`}}, path: "sources"},
		{name: "islands空", edits: []edit{{`"islands": [`, `"islands": [], "oldIslands": [`}}, path: "islands"},
		{name: "id空", edits: []edit{{`"id": "hama-hoshizuna"`, `"id": ""`}}, path: "islands[0].id"},
		{name: "id重複", edits: []edit{{`"id": "hama-tsukikage"`, `"id": "hama-hoshizuna"`}}, path: "islands[1].id"},
		{name: "人口なし", edits: []edit{{`"value": 19134,`, `"count": 19134,`}}, path: "islands[0].population.value"},
		{name: "人口負", edits: []edit{{`"value": 19134,`, `"value": -1,`}}, path: "islands[0].population.value"},
		{name: "人口小数", edits: []edit{{`"value": 19134,`, `"value": 1.5,`}}, path: "islands[0].population.value"},
		{name: "人口指数", edits: []edit{{`"value": 19134,`, `"value": 2.5e4,`}}, path: "islands[0].population.value"},
		{name: "人口文字列", edits: []edit{{`"value": 19134,`, `"value": "100",`}}, path: "islands[0].population.value"},
		{name: "sqrt3桁", edits: []edit{{`"populationSqrt": 138.33`, `"populationSqrt": 1.234`}}, path: "islands[0].populationSqrt"},
		{name: "sqrt負", edits: []edit{{`"populationSqrt": 138.33`, `"populationSqrt": -1`}}, path: "islands[0].populationSqrt"},
		{name: "sqrt文字列", edits: []edit{{`"populationSqrt": 138.33`, `"populationSqrt": "13"`}}, path: "islands[0].populationSqrt"},
		{name: "sqrtなし", edits: []edit{{`"populationSqrt": 138.33`, `"populationSqrtX": 138.33`}}, path: "islands[0].populationSqrt"},
		{name: "sqrt不一致", edits: []edit{{`"populationSqrt": 27.71`, `"populationSqrt": 27.70`}}, path: "islands[1].populationSqrt"},
		{name: "quote空", edits: []edit{{`"quote": "温暖で、雨の少ない架空の気候"`, `"quote": ""`}}, path: "excerpts[0].quote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := tc.raw
			if data == "" {
				data = applyEdits(t, fixture, tc.edits...)
			}
			is, err := ParseIslands("x/islands.json", []byte(data))
			if is != nil {
				t.Error("エラーなのに *Islands を返した")
			}
			requireIssue(t, err, "x/islands.json", tc.path)
		})
	}
}

// excerpts はなくてもよい。
func TestParseIslandsNoExcerpts(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "islands.json"), edit{`"excerpts": [`, `"oldExcerpts": [`})
	is, err := ParseIslands("islands.json", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(is.Excerpts) != 0 {
		t.Errorf("Excerpts = %v", is.Excerpts)
	}
}

func TestPopulationSqrtCenti(t *testing.T) {
	t.Parallel()
	for pop, want := range map[int64]int64{25881: 16088, 768: 2771, 169: 1300, 2: 141, 0: 0, 1: 100} {
		if got := populationSqrtCenti(pop); got != want {
			t.Errorf("populationSqrtCenti(%d) = %d, want %d", pop, got, want)
		}
	}
}
