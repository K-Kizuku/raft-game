package stage

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

// fixtureDisplay は testdata/islands.json を ParseDisplay で読む。
func fixtureDisplay(t *testing.T) *Display {
	t.Helper()
	d, err := ParseDisplay("islands.json", []byte(readTestdata(t, "islands.json")))
	if err != nil {
		t.Fatalf("ParseDisplay: %v", err)
	}
	return d
}

// fixtureStage は testdata の id のステージを LoadDir で読む。
func fixtureStage(t *testing.T, id string) *Stage {
	t.Helper()
	stages, err := LoadDir(os.DirFS("testdata"), "islands.json", "stages")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for _, st := range stages {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("stage %s がない", id)
	return nil
}

func mustLookup(t *testing.T, d *Display, ref string) IslandDisplay {
	t.Helper()
	isl, ok := d.Lookup(ref)
	if !ok {
		t.Fatalf("Lookup(%s) がない", ref)
	}
	return isl
}

// DP-1 正常。
func TestParseDisplay(t *testing.T) {
	t.Parallel()
	d := fixtureDisplay(t)

	hoshi := mustLookup(t, d, "hama-hoshizuna")
	if hoshi.Name != "星砂島" || hoshi.Prefecture != "波間県" || hoshi.DesignatedArea != "星砂諸島" {
		t.Errorf("hama-hoshizuna = %+v", hoshi)
	}
	if !reflect.DeepEqual(hoshi.Municipalities, []string{"星砂町", "月影町"}) {
		t.Errorf("Municipalities = %v", hoshi.Municipalities)
	}
	wantPop := Measure{19134, "19134", "令和8年4月1日", Derived, "星砂町 12,345 人と月影町 6,789 人の合計", "fixture-2026"}
	if hoshi.Population != wantPop {
		t.Errorf("Population = %+v, want %+v", hoshi.Population, wantPop)
	}
	wantArea := Measure{303000, "30.3", "令和8年4月1日", Derived, "星砂町 10.1 km²と月影町 20.2 km²の合計", "fixture-2026"}
	if hoshi.Area != wantArea {
		t.Errorf("Area = %+v, want %+v", hoshi.Area, wantArea)
	}
	if hoshi.Plan == nil || hoshi.Plan.Region != "星砂地域" {
		t.Fatalf("Plan = %+v", hoshi.Plan)
	}
	var keys []string
	for _, pt := range hoshi.Plan.Texts {
		keys = append(keys, pt.Key)
	}
	if !reflect.DeepEqual(keys, []string{"climate", "maritime"}) {
		t.Errorf("Plan.Texts の Key = %v", keys)
	}
	wantClimate := PlanText{
		Key: "climate", Field: "NATURAL_CLIMATE_FEATURES",
		Text:       "温暖で、雨の少ない架空の気候である。\n冬は北風が強い。",
		Provenance: Fact, Source: "fixture-2026",
	}
	if hoshi.Plan.Texts[0] != wantClimate {
		t.Errorf("Plan.Texts[0] = %+v", hoshi.Plan.Texts[0])
	}
	if got := hoshi.MunicipalityLabel(); got != "星砂町・月影町" {
		t.Errorf("MunicipalityLabel() = %q", got)
	}

	tsuki := mustLookup(t, d, "hama-tsukikage")
	if tsuki.Area.Value != 145000 || tsuki.Area.Text != "14.5" || tsuki.MunicipalityLabel() != "月影町" {
		t.Errorf("hama-tsukikage = %+v", tsuki)
	}

	todai := mustLookup(t, d, "hama-todai")
	wantTodaiArea := Measure{20000, "2", "令和8年4月1日", Fact, "", "fixture-2026"}
	if todai.Area != wantTodaiArea {
		t.Errorf("hama-todai Area = %+v, want %+v", todai.Area, wantTodaiArea)
	}
	if todai.Population.Note != "" {
		t.Errorf("hama-todai Population.Note = %q", todai.Population.Note)
	}

	koishi := mustLookup(t, d, "shiomi-koishi")
	if koishi.Area.Value != 5000 || koishi.Area.Text != "0.5" {
		t.Errorf("shiomi-koishi Area = %+v", koishi.Area)
	}
	if koishi.Plan != nil {
		t.Errorf("shiomi-koishi Plan = %+v", koishi.Plan)
	}
	if _, ok := d.Lookup("nope"); ok {
		t.Error("Lookup(nope) found")
	}

	if len(d.Islands) != 8 || d.Islands[0].Ref != "hama-hoshizuna" || d.Islands[7].Ref != "kaze-nagi" {
		t.Errorf("Islands の順 = %v", d.Islands)
	}

	wantSources := []SourceInfo{{
		Key:         "fixture-2026",
		Title:       "架空の離島のデータ（テスト用）",
		Publisher:   "架空の提供者",
		License:     "架空の利用規約",
		TermsURL:    "https://example.com/terms",
		PublishedAt: "2026-10-31",
	}}
	if !reflect.DeepEqual(d.Sources, wantSources) {
		t.Errorf("Sources = %+v", d.Sources)
	}

	if len(d.Excerpts) != 3 {
		t.Fatalf("Excerpts = %+v", d.Excerpts)
	}
	for _, e := range d.Excerpts {
		if e.ID == "tsukikage-hoshizuna-minutes" {
			if e.Value == nil || *e.Value != 10 || e.Unit != "分" || e.Provenance != Derived ||
				e.Note != "架空の航路の所要時間" || e.Source != "fixture-2026" {
				t.Errorf("%s = %+v", e.ID, e)
			}
			continue
		}
		if e.Value != nil || e.Unit != "" {
			t.Errorf("%s: Value = %v, Unit = %q", e.ID, e.Value, e.Unit)
		}
	}
	if e := d.Excerpts[1]; e.ID != "todai-storm" || e.Note != "架空の注記" || e.Provenance != Fact {
		t.Errorf("Excerpts[1] = %+v", e)
	}
	if d.Excerpts[0].Note != "" {
		t.Errorf("Excerpts[0].Note = %q", d.Excerpts[0].Note)
	}
}

// DP-2 誤りの表。
func TestParseDisplayErrors(t *testing.T) {
	t.Parallel()
	base := readTestdata(t, "islands.json")
	tests := []struct {
		name  string
		edits []edit
		path  string
	}{
		{"municipalities が空", []edit{{"\"municipalities\": [\n        \"星砂町\",\n        \"月影町\"\n      ],", `"municipalities": [],`}}, "islands[0].municipalities"},
		{"municipalities がない", []edit{{"\"municipalities\": [\n        \"灯台村\"\n      ],\n", ""}}, "islands[2].municipalities"},
		{"municipalities の重複", []edit{{"\"星砂町\",\n        \"月影町\"", "\"月影町\",\n        \"月影町\""}}, "islands[0].municipalities[1]"},
		{"municipalities の空の要素", []edit{{"\"星砂町\",\n        \"月影町\"", "\"星砂町\",\n        \"\""}}, "islands[0].municipalities[1]"},
		{"municipalities の要素が文字列でない", []edit{{"\"星砂町\",\n        \"月影町\"", "\"星砂町\",\n        1"}}, "islands[0].municipalities[1]"},
		{"designatedArea がない", []edit{{"\"designatedArea\": \"灯台島\",\n", ""}}, "islands[2].designatedArea"},
		{"designatedArea が空", []edit{{`"designatedArea": "灯台島"`, `"designatedArea": ""`}}, "islands[2].designatedArea"},
		{"population.asOf がない", []edit{{"\"value\": 19134,\n        \"asOf\": \"令和8年4月1日\",", `"value": 19134,`}}, "islands[0].population.asOf"},
		{"population.provenance が creative", []edit{{"\"value\": 768,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\"", "\"value\": 768,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"creative\""}}, "islands[1].population.provenance"},
		{"population.note が文字列でない", []edit{{`"note": "星砂町 12,345 人と月影町 6,789 人の合計"`, `"note": 1`}}, "islands[0].population.note"},
		{"population.source が sources にない", []edit{{"\"value\": 169,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\",\n        \"source\": \"fixture-2026\"", "\"value\": 169,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\",\n        \"source\": \"x\""}}, "islands[2].population.source"},
		{"areaKm2 がない", []edit{{"\"areaKm2\": {\n        \"value\": 0.5,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\",\n        \"source\": \"fixture-2026\"\n      },\n", ""}}, "islands[3].areaKm2"},
		{"areaKm2 がオブジェクトでない", []edit{{"\"areaKm2\": {\n        \"value\": 0.5,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\",\n        \"source\": \"fixture-2026\"\n      },\n", "\"areaKm2\": 0.5,\n"}}, "islands[3].areaKm2"},
		{"areaKm2.value がない", []edit{{"\"value\": 0.5,\n", ""}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が小数第 5 位", []edit{{`"value": 0.5,`, `"value": 1.23456,`}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が 2.00001", []edit{{`"value": 0.5,`, `"value": 2.00001,`}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が負", []edit{{`"value": 0.5,`, `"value": -1,`}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が文字列", []edit{{`"value": 0.5,`, `"value": "2",`}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が指数", []edit{{`"value": 0.5,`, `"value": 1e2,`}}, "islands[3].areaKm2.value"},
		{"areaKm2.value が大きすぎる", []edit{{`"value": 0.5,`, `"value": 1000000001,`}}, "islands[3].areaKm2.value"},
		{"areaKm2.provenance が creative", []edit{{"\"value\": 0.5,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"fact\"", "\"value\": 0.5,\n        \"asOf\": \"令和8年4月1日\",\n        \"provenance\": \"creative\""}}, "islands[3].areaKm2.provenance"},
		{"areaKm2.asOf がない", []edit{{"\"value\": 0.5,\n        \"asOf\": \"令和8年4月1日\",", `"value": 0.5,`}}, "islands[3].areaKm2.asOf"},
		{"plan.climate.field が違う", []edit{{"\"text\": \"温暖で、雨の少ない架空の気候である。\",\n          \"provenance\": \"fact\",\n          \"source\": \"fixture-2026\",\n          \"field\": \"NATURAL_CLIMATE_FEATURES\"", "\"text\": \"温暖で、雨の少ない架空の気候である。\",\n          \"provenance\": \"fact\",\n          \"source\": \"fixture-2026\",\n          \"field\": \"PORTS\""}}, "islands[1].plan.climate.field"},
		{"plan.climate.text が空", []edit{{`"text": "温暖で、雨の少ない架空の気候である。",`, `"text": "",`}}, "islands[1].plan.climate.text"},
		{"plan.disaster がオブジェクトでない", []edit{{"\"disaster\": {\n          \"text\": \"架空の台風で灯台が壊れた。\",\n          \"provenance\": \"fact\",\n          \"source\": \"fixture-2026\",\n          \"field\": \"DISASTER_LAND_CONSERVATION\"\n        }", `"disaster": "x"`}}, "islands[2].plan.disaster"},
		{"plan.disaster.source が sources にない", []edit{{"\"text\": \"架空の台風で灯台が壊れた。\",\n          \"provenance\": \"fact\",\n          \"source\": \"fixture-2026\"", "\"text\": \"架空の台風で灯台が壊れた。\",\n          \"provenance\": \"fact\",\n          \"source\": \"y\""}}, "islands[2].plan.disaster.source"},
		{"excerpts の unit だけ", []edit{{"\"quote\": \"温暖で、雨の少ない架空の気候\",\n", "\"quote\": \"温暖で、雨の少ない架空の気候\",\n      \"unit\": \"分\",\n"}}, "excerpts[0].unit"},
		{"excerpts の value だけ", []edit{{"\"unit\": \"分\",\n", ""}}, "excerpts[2].unit"},
		{"excerpts の unit が空", []edit{{`"unit": "分",`, `"unit": "",`}}, "excerpts[2].unit"},
		{"excerpts の value が負", []edit{{`"value": 10,`, `"value": -10,`}}, "excerpts[2].value"},
		{"excerpts の provenance が creative", []edit{{"\"quote\": \"架空の台風で灯台が壊れた\",\n      \"provenance\": \"fact\"", "\"quote\": \"架空の台風で灯台が壊れた\",\n      \"provenance\": \"creative\""}}, "excerpts[1].provenance"},
		{"excerpts の source が sources にない", []edit{{"\"note\": \"架空の注記\",\n      \"source\": \"fixture-2026\"", "\"note\": \"架空の注記\",\n      \"source\": \"z\""}}, "excerpts[1].source"},
		{"sources.title が空", []edit{{`"title": "架空の離島のデータ（テスト用）"`, `"title": ""`}}, "sources.fixture-2026.title"},
		{"sources.termsUrl がない", []edit{{"\"termsUrl\": \"https://example.com/terms\",\n", ""}}, "sources.fixture-2026.termsUrl"},
		{"sources の項目がオブジェクトでない", []edit{{"\"fixture-2026\": {\n      \"title\": \"架空の離島のデータ（テスト用）\",\n      \"publisher\": \"架空の提供者\",\n      \"license\": \"架空の利用規約\",\n      \"termsUrl\": \"https://example.com/terms\",\n      \"publishedAt\": \"2026-10-31\"\n    }", `"fixture-2026": "x"`}}, "sources.fixture-2026"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := applyEdits(t, base, tc.edits...)
			if _, err := ParseIslands("x/islands.json", []byte(data)); err != nil {
				t.Fatalf("ParseIslands が誤りを返した（表示用の欄の検査にならない）: %v", err)
			}
			d, err := ParseDisplay("x/islands.json", []byte(data))
			if d != nil {
				t.Errorf("Display = %+v, want nil", d)
			}
			requireIssue(t, err, "x/islands.json", tc.path)
		})
	}
}

// DP-3 ParseIslands の誤りはそのまま返す。
func TestParseDisplayIslandsError(t *testing.T) {
	t.Parallel()
	data := []byte(applyEdits(t, readTestdata(t, "islands.json"), edit{"\"value\": 19134,\n", ""}))
	_, want := ParseIslands("islands.json", data)
	wantErr := asError(t, want)
	_, err := ParseDisplay("islands.json", data)
	got := asError(t, err)
	if got.File != wantErr.File || !reflect.DeepEqual(got.Issues, wantErr.Issues) {
		t.Errorf("ParseDisplay = %v, want %v", got, wantErr)
	}
}

// DP-4 誤りが 2 つ → 1 つの *Error に検査の順で 2 件。
func TestParseDisplayMultipleErrors(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "islands.json"),
		edit{"\"quote\": \"温暖で、雨の少ない架空の気候\",\n      \"provenance\": \"fact\"", "\"quote\": \"温暖で、雨の少ない架空の気候\",\n      \"provenance\": \"creative\""},
		edit{`"designatedArea": "星砂諸島",
      "municipalities": [
        "星砂町",`, `"municipalities": [
        "星砂町",`},
	)
	_, err := ParseDisplay("islands.json", []byte(data))
	se := asError(t, err)
	var paths []string
	for _, is := range se.Issues {
		paths = append(paths, is.Path)
	}
	if want := []string{"islands[0].designatedArea", "excerpts[0].provenance"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("Issues の Path = %v, want %v（%v）", paths, want, err)
	}
	if se.File != "islands.json" {
		t.Errorf("File = %q", se.File)
	}
}

// DP-5 ForStage。
func TestDisplayForStage(t *testing.T) {
	t.Parallel()
	d := fixtureDisplay(t)
	alpha := fixtureStage(t, "alpha")
	got, err := d.ForStage(alpha)
	if err != nil {
		t.Fatalf("ForStage: %v", err)
	}
	var refs []string
	for _, isl := range got {
		refs = append(refs, isl.Ref)
	}
	if want := []string{"hama-hoshizuna", "hama-tsukikage", "hama-todai"}; !reflect.DeepEqual(refs, want) {
		t.Errorf("Ref = %v, want %v", refs, want)
	}
	if got[0].MunicipalityLabel() != "星砂町・月影町" || got[0].Population.Value != alpha.Islands[0].Population {
		t.Errorf("ForStage[0] = %+v", got[0])
	}

	base := readTestdata(t, "islands.json")
	tests := []struct {
		name string
		edit edit
		want string
	}{
		{"人口が違う", edit{`"value": 19134,`, `"value": 19135,`}, "stage alpha: islands[0]: hama-hoshizuna の name・population.value が、ステージを読んだ islands.json と違う"},
		{"名前が違う", edit{`"name": "月影島",`, `"name": "月影島2",`}, "stage alpha: islands[1]: hama-tsukikage の name・population.value が、ステージを読んだ islands.json と違う"},
		{"ref がない", edit{`"id": "hama-todai",`, `"id": "hama-todai2",`}, "stage alpha: islands[2]: hama-todai が islands.json の表示用の値にない"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			other, err := ParseDisplay("islands.json", []byte(applyEdits(t, base, tc.edit)))
			if err != nil {
				t.Fatalf("ParseDisplay: %v", err)
			}
			out, err := other.ForStage(alpha)
			if err == nil || err.Error() != tc.want {
				t.Errorf("ForStage err = %v, want %q", err, tc.want)
			}
			if out != nil {
				t.Errorf("ForStage = %v, want nil", out)
			}
		})
	}

	if _, err := d.ForStage(nil); err == nil || err.Error() != "stage: ForStage: nil stage" {
		t.Errorf("ForStage(nil) err = %v", err)
	}
}

// DP-6 Lookup・ForStage の結果は複製。
func TestDisplayCopies(t *testing.T) {
	t.Parallel()
	d := fixtureDisplay(t)
	a := mustLookup(t, d, "hama-hoshizuna")
	a.Municipalities[0] = "変更"
	a.Plan.Texts[0].Text = "変更"
	a.Plan.Region = "変更"

	list, err := d.ForStage(fixtureStage(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	list[0].Municipalities[0] = "変更"
	list[0].Plan.Texts[0].Text = "変更"

	b := mustLookup(t, d, "hama-hoshizuna")
	if b.Municipalities[0] != "星砂町" || b.Plan.Texts[0].Text != "温暖で、雨の少ない架空の気候である。\n冬は北風が強い。" || b.Plan.Region != "星砂地域" {
		t.Errorf("Lookup の値が変わった: %+v %+v", b, b.Plan)
	}

	e, ok := d.Excerpt(&Source{Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "月影~星砂(3km・10分"})
	if !ok {
		t.Fatal("Excerpt がない")
	}
	*e.Value = 99
	if e2, _ := d.Excerpt(&Source{Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "月影~星砂(3km・10分"}); *e2.Value != 10 {
		t.Errorf("Excerpt の Value が変わった: %d", *e2.Value)
	}
}

// DP-7 Excerpt。
func TestDisplayExcerpt(t *testing.T) {
	t.Parallel()
	d := fixtureDisplay(t)
	alpha := fixtureStage(t, "alpha")
	e, ok := d.Excerpt(alpha.Routes[0].Source)
	if !ok || e.ID != "tsukikage-hoshizuna-minutes" {
		t.Errorf("Excerpt(routes[0].source) = %+v, %v", e, ok)
	}
	if alpha.Intro.Source == nil {
		t.Fatal("intro.source がない")
	}
	if _, ok := d.Excerpt(alpha.Intro.Source); ok {
		t.Error("Excerpt(intro.source) found")
	}
	if _, ok := d.Excerpt(nil); ok {
		t.Error("Excerpt(nil) found")
	}
	if _, ok := d.Excerpt(&Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "ない引用"}); ok {
		t.Error("Excerpt(ない引用) found")
	}
}

// DP-8 決定性。
func TestParseDisplayDeterministic(t *testing.T) {
	t.Parallel()
	data := []byte(readTestdata(t, "islands.json"))
	orig := bytes.Clone(data)
	a, err := ParseDisplay("islands.json", data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseDisplay("islands.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("2 回の ParseDisplay の結果が違う")
	}
	if !bytes.Equal(data, orig) {
		t.Error("入力のバイト列が変わった")
	}
}

// excerpts がない islands.json → Excerpts は nil。plan に 7 つの文章が 1 つもない → Texts は nil。
func TestParseDisplayOptional(t *testing.T) {
	t.Parallel()
	base := readTestdata(t, "islands.json")
	i := strings.Index(base, `,
  "excerpts": [`)
	if i < 0 {
		t.Fatal("excerpts がない")
	}
	noExcerpts := base[:i] + "\n}\n"
	d, err := ParseDisplay("islands.json", []byte(noExcerpts))
	if err != nil {
		t.Fatal(err)
	}
	if d.Excerpts != nil {
		t.Errorf("Excerpts = %v", d.Excerpts)
	}

	data := applyEdits(t, base, edit{"\"region\": \"灯台地域\",\n        \"disaster\": {\n          \"text\": \"架空の台風で灯台が壊れた。\",\n          \"provenance\": \"fact\",\n          \"source\": \"fixture-2026\",\n          \"field\": \"DISASTER_LAND_CONSERVATION\"\n        }", "\"region\": \"灯台地域\",\n        \"other\": 1"})
	d, err = ParseDisplay("islands.json", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	todai := mustLookup(t, d, "hama-todai")
	if todai.Plan == nil || todai.Plan.Region != "灯台地域" || todai.Plan.Texts != nil {
		t.Errorf("hama-todai Plan = %+v", todai.Plan)
	}
}

// LoadDisplay は読めなければ LoadDir と同じ文の *Error。
func TestLoadDisplay(t *testing.T) {
	t.Parallel()
	d, err := LoadDisplay(os.DirFS("testdata"), "islands.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, fixtureDisplay(t)) {
		t.Error("LoadDisplay と ParseDisplay の結果が違う")
	}
	_, err = LoadDisplay(os.DirFS("testdata"), "nope.json")
	se := asError(t, err)
	if se.File != "nope.json" || len(se.Issues) != 1 || !strings.HasPrefix(se.Issues[0].Message, "読めない: ") {
		t.Errorf("LoadDisplay(nope) = %v", err)
	}
}
