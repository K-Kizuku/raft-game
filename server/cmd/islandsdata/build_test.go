package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E-1: fixture から作ったバイト列が golden と一致し、2 回作っても同じ。
func TestBuildGolden(t *testing.T) {
	t.Parallel()
	got, err := build(fixtureRaw, fixtureCurated, fixtureSelect)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if *update {
		if err := os.WriteFile(fixtureGolden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := readFile(t, fixtureGolden)
	if !bytes.Equal(got, want) {
		t.Fatalf("golden と違う（go test -run TestBuildGolden -update で更新する）\n--- got\n%s\n--- want\n%s", got, want)
	}
	again, err := build(fixtureRaw, fixtureCurated, fixtureSelect)
	if err != nil {
		t.Fatalf("build（2 回目）: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("2 回目の build のバイト列が違う")
	}
	if !bytes.HasSuffix(got, []byte("}\n")) || bytes.HasSuffix(got, []byte("\n\n")) {
		t.Fatal("末尾が改行 1 つではない")
	}
}

func fixtureOutput(t *testing.T) map[string]any {
	t.Helper()
	b, err := build(fixtureRaw, fixtureCurated, fixtureSelect)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return decodeJSON(t, b)
}

// E-2: 最新の基準日の行の値だけを使い、asOf は最新の行の文字列。
func TestBuildLatestReferenceDate(t *testing.T) {
	t.Parallel()
	v := fixtureOutput(t)
	cases := []struct{ id, pop, area string }{
		{"hama-hoshizuna", "19134", "30.3"},
		{"hama-tsukikage", "768", "14.5"},
		{"hama-todai", "169", "2.0001"},
		{"shiomi-koishi", "2", "12"},
	}
	for _, c := range cases {
		is := islandByID(t, v, c.id)
		if got := str(t, is, "population", "value"); got != c.pop {
			t.Errorf("%s: population = %s, want %s", c.id, got, c.pop)
		}
		if got := str(t, is, "areaKm2", "value"); got != c.area {
			t.Errorf("%s: areaKm2 = %s, want %s", c.id, got, c.area)
		}
		for _, k := range []string{"population", "areaKm2"} {
			if got := str(t, is, k, "asOf"); got != "令和8年4月1日" {
				t.Errorf("%s: %s.asOf = %s", c.id, k, got)
			}
		}
	}

	// 古い基準日の行（令和元年5月1日、甲町 100 人・9.5 km²）が混ざらない。
	b := baseInputs().mustBuild(t)
	is := islandByID(t, decodeJSON(t, b), "ko-jima")
	if got := str(t, is, "population", "value"); got != "1200" {
		t.Errorf("population = %s, want 1200", got)
	}
	if got := str(t, is, "areaKm2", "value"); got != "3.75" {
		t.Errorf("areaKm2 = %s, want 3.75", got)
	}
}

// E-3: 複数の自治体の人口・面積を整数で合計し、derived と note を付ける。
func TestBuildSumOverMunicipalities(t *testing.T) {
	t.Parallel()
	is := islandByID(t, fixtureOutput(t), "hama-hoshizuna")
	want := map[string][2]string{
		"population": {"derived", "星砂町 12,345 人と月影町 6,789 人の合計"},
		"areaKm2":    {"derived", "星砂町 10.1 km²と月影町 20.2 km²の合計"},
	}
	for k, w := range want {
		if got := str(t, is, k, "provenance"); got != w[0] {
			t.Errorf("%s.provenance = %s", k, got)
		}
		if got := str(t, is, k, "note"); got != w[1] {
			t.Errorf("%s.note = %q, want %q", k, got, w[1])
		}
	}
	if got := str(t, is, "areaKm2", "value"); got != "30.3" {
		t.Errorf("areaKm2 = %s, want 30.3", got)
	}
	if got := str(t, is, "populationSqrt"); got != "138.33" {
		t.Errorf("populationSqrt = %s, want 138.33", got)
	}

	// 3 つの自治体: 「、」と「と」。0.1 + 0.2 は 0.3 になる（浮動小数点の誤差がない）。
	in := baseInputs()
	in.d1 = replaceOnce(t, in.d1, "3,令和8年4月1日,甲県,甲諸島,甲島,乙町,200,2.25\n",
		"3,令和8年4月1日,甲県,甲諸島,甲島,乙町,2000,0.2\n5,令和8年4月1日,甲県,甲諸島,甲島,丙町,3,0\n")
	in.d1 = replaceOnce(t, in.d1, `"1,000",1.5`, `"1,000",0.1`)
	in.sel = replaceOnce(t, in.sel, "[甲町, 乙町]", "[甲町, 乙町, 丙町]")
	is = islandByID(t, decodeJSON(t, in.mustBuild(t)), "ko-jima")
	checks := map[string]string{
		"population.value": "3003",
		"population.note":  "甲町 1,000 人、乙町 2,000 人と丙町 3 人の合計",
		"areaKm2.value":    "0.3",
		"areaKm2.note":     "甲町 0.1 km²、乙町 0.2 km²と丙町 0 km²の合計",
	}
	for path, w := range checks {
		k, f, _ := strings.Cut(path, ".")
		if got := str(t, is, k, f); got != w {
			t.Errorf("%s = %q, want %q", path, got, w)
		}
	}
}

// E-4: 1 つの自治体の島は fact で note がない。
func TestBuildSingleMunicipality(t *testing.T) {
	t.Parallel()
	v := fixtureOutput(t)
	for _, id := range []string{"hama-tsukikage", "hama-todai", "shiomi-koishi"} {
		is := islandByID(t, v, id)
		for _, k := range []string{"population", "areaKm2"} {
			if got := str(t, is, k, "provenance"); got != "fact" {
				t.Errorf("%s: %s.provenance = %s", id, k, got)
			}
			if _, ok := at(is, k, "note"); ok {
				t.Errorf("%s: %s.note がある", id, k)
			}
			if got := str(t, is, k, "source"); got != "fixture-2026" {
				t.Errorf("%s: %s.source = %s", id, k, got)
			}
		}
	}
}

// E-5: 同じ島名で都道県・指定地域が違う行が混ざらない。
func TestBuildSameIslandName(t *testing.T) {
	t.Parallel()
	is := islandByID(t, fixtureOutput(t), "hama-todai")
	if got := str(t, is, "population", "value"); got != "169" {
		t.Errorf("population = %s, want 169（北灯台諸島・汐見県の灯台島が混ざっている）", got)
	}
	muni, _ := at(is, "municipalities")
	if a, _ := muni.([]any); len(a) != 1 || a[0] != "灯台村" {
		t.Errorf("municipalities = %v", muni)
	}
}

// E-6: 入力の誤り。どれもファイル名と、CSV なら行番号、YAML なら id をメッセージに含める。
func TestBuildErrors(t *testing.T) {
	t.Parallel()
	const d1 = "01_ritoushinkoutaisakujisshichiikiichiran.csv"
	const d2 = "01_zentaijouhou.csv"
	const sel = "islands-select.yaml"
	type tc struct {
		name string
		mod  func(t *testing.T, in *inputs)
		want []string
	}
	setD1 := func(old, new string) func(t *testing.T, in *inputs) {
		return func(t *testing.T, in *inputs) { in.d1 = replaceOnce(t, in.d1, old, new) }
	}
	setD2 := func(old, new string) func(t *testing.T, in *inputs) {
		return func(t *testing.T, in *inputs) { in.d2 = replaceOnce(t, in.d2, old, new) }
	}
	setSel := func(old, new string) func(t *testing.T, in *inputs) {
		return func(t *testing.T, in *inputs) { in.sel = replaceOnce(t, in.sel, old, new) }
	}
	setCur := func(old, new string) func(t *testing.T, in *inputs) {
		return func(t *testing.T, in *inputs) { in.curated["a.yaml"] = replaceOnce(t, in.curated["a.yaml"], old, new) }
	}
	cases := []tc{
		{"島がない（候補の指定地域）", setSel("designatedArea: 甲諸島", "designatedArea: 甲列島"), []string{d1, "ko-jima", "候補: 甲諸島"}},
		{"島がない（候補なし）", setSel("name: 丙島", "name: 戊島"), []string{d1, "otsu-heijima", "行がない"}},
		{"自治体が足りない", setSel("[甲町, 乙町]", "[甲町, 乙町, 戊町]"), []string{d1, "ko-jima", "足りない: [戊町]"}},
		{"自治体が余分", setSel("[甲町, 乙町]", "[甲町]"), []string{d1, "ko-jima", "余分: [乙町（4 行目）]"}},
		{"同じ自治体の行が 2 つ", setD1("4,令和8年4月1日", "5,令和8年4月1日,甲県,甲諸島,甲島,甲町,1,1\n4,令和8年4月1日"), []string{d1, "ko-jima", "甲町", "3 行目と 5 行目"}},
		{"最新の基準日に行がない", setD1("4,令和8年4月1日", "4,令和9年4月1日"), []string{d1, "ko-jima", "最新の基準日（2027-04-01）"}},
		{"人口が空", setD1(`"1,000"`, ``), []string{d1 + ":3:", "POPULATION", "ko-jima"}},
		{"人口が負", setD1(`"1,000"`, `-1`), []string{d1 + ":3:", "POPULATION", "ko-jima"}},
		{"人口が小数", setD1(`"1,000"`, `1.5`), []string{d1 + ":3:", "POPULATION", "ko-jima"}},
		{"人口が文字", setD1(`"1,000"`, `abc`), []string{d1 + ":3:", "POPULATION", "ko-jima"}},
		{"面積が空", setD1(`,2.25`, `,`), []string{d1 + ":4:", "AREA", "ko-jima"}},
		{"面積が小数第 5 位まで", setD1(`2.25`, `1.23456`), []string{d1 + ":4:", "AREA", "ko-jima"}},
		{"面積が文字", setD1(`2.25`, `x`), []string{d1 + ":4:", "AREA", "ko-jima"}},
		{"面積が負", setD1(`2.25`, `-1`), []string{d1 + ":4:", "AREA", "ko-jima"}},
		{"使う列がない", setD1(",POPULATION,AREA\n", ",POPULATION,AREA_X\n"), []string{d1 + ":1:", "AREA"}},
		{"使う列がない（D2）", setD2(",PORTS,", ",PORTS_X,"), []string{d2 + ":1:", "PORTS"}},
		{"列数の違う行", setD1(`"1,000",1.5`, `"1,000",1.5,余分`), []string{d1 + ":3:"}},
		{"壊れた引用符", setD1(`"1,000"`, `"1,0"00"`), []string{d1 + ":3:"}},
		{"UTF-8 ではない", setD1("乙町,200", "乙町\xff,200"), []string{d1 + ":4:", "UTF-8"}},
		{"D1 がない", func(_ *testing.T, in *inputs) { in.noD1 = true }, []string{d1}},
		{"D2 がない", func(_ *testing.T, in *inputs) { in.noD2 = true }, []string{d2}},
		{"計画の地域がない", setSel("region: 甲地域", "region: 戊地域"), []string{sel, "ko-jima", d2, "戊地域"}},
		{"計画の地域が 2 行", func(t *testing.T, in *inputs) {
			in.d2 += "2,甲地域,甲島,a,b,c,d,e,f,g\n"
		}, []string{sel, "ko-jima", d2 + ":2,4:", "甲地域"}},
		{"ISLAND_NAME に島がない", setD2("甲地域,甲島、丁島", "甲地域,甲ノ島、丁島"), []string{sel, "ko-jima", d2 + ":2:", "甲島"}},
		{"select: 未知のキー", setSel("    name: 甲島\n", "    name: 甲島\n    color: red\n"), []string{sel, "ko-jima", "color"}},
		{"select: 重複したキー", setSel("    name: 甲島\n", "    name: 甲島\n    name: 甲島\n"), []string{sel, "ko-jima", "already defined"}},
		{"select: 型の違い", setSel("municipalities: [丙村]", "municipalities: 丙村"), []string{sel, "otsu-heijima"}},
		{"select: format: 2", setSel("format: 1", "format: 2"), []string{sel, "format"}},
		{"select: format が小数", setSel("format: 1", "format: 1.0"), []string{sel, "整数"}},
		{"select: 重複した id", setSel("id: otsu-heijima", "id: ko-jima"), []string{sel, "ko-jima", "重複"}},
		{"select: 形式に合わない id", setSel("id: otsu-heijima", "id: Otsu"), []string{sel, "Otsu", "形式"}},
		{"select: 空の municipalities", setSel("[丙村]", "[]"), []string{sel, "otsu-heijima", "municipalities"}},
		{"select: sources にない dataset", setSel("dataset: test", "dataset: nope"), []string{sel, "nope"}},
		{"select: 不正な generatedAt", setSel(`"2026-11-07"`, `"2026-02-30"`), []string{sel, "generatedAt"}},
		{"select: termsUrl が http://", setSel("https://example.com/", "http://example.com/"), []string{sel, "termsUrl"}},
		{"抜き出し: 未知のキー", setCur("    unit: 分\n", "    unit: 分\n    color: red\n"), []string{"a.yaml", "ko-minutes", "color"}},
		{"抜き出し: ファイルをまたぐ id の重複", func(_ *testing.T, in *inputs) {
			in.curated["b.yaml"] = baseCuratedA
		}, []string{"b.yaml", "ko-minutes", "a.yaml", "重複"}},
		{"抜き出し: 引用が文章にない（~ を ～ に）", setCur(`"甲~丁(5km・20分"`, `"甲～丁(5km・20分"`), []string{"a.yaml", "ko-minutes", "quote", d2 + ":2:"}},
		{"抜き出し: ない地域", setCur("region: 甲地域", "region: 戊地域"), []string{"a.yaml", "ko-minutes", "戊地域"}},
		{"抜き出し: ない列", setCur("field: MARITIME_TRANSPORT", "field: AIR_TRANSPORT"), []string{"a.yaml", "ko-minutes", "AIR_TRANSPORT"}},
		{"抜き出し: field: _id", setCur("field: MARITIME_TRANSPORT", "field: _id"), []string{"a.yaml", "ko-minutes", "_id"}},
		{"抜き出し: provenance: creative", setCur("provenance: derived", "provenance: creative"), []string{"a.yaml", "ko-minutes", "creative"}},
		{"抜き出し: unit だけ", setCur("    value: 20\n", ""), []string{"a.yaml", "ko-minutes", "unit"}},
		{"抜き出し: value だけ", setCur("    unit: 分\n", ""), []string{"a.yaml", "ko-minutes", "unit"}},
		{"抜き出し: value: 1.5", setCur("value: 20", "value: 1.5"), []string{"a.yaml", "ko-minutes", "整数"}},
		{"抜き出し: 空の quote", setCur(`quote: "甲~丁(5km・20分"`, `quote: ""`), []string{"a.yaml", "ko-minutes", "quote"}},
		{"抜き出し: format: 2", setCur("format: 1", "format: 2"), []string{"a.yaml", "format"}},
		{"抜き出し: *.yml", func(_ *testing.T, in *inputs) { in.curated["c.yml"] = baseCuratedA }, []string{"c.yml"}},
	}
	for _, d := range []string{"令和年4月1日", "2026-04-01", "昭和60年1月1日", "令和8年2月30日", "令和8年13月1日", ""} {
		cases = append(cases, tc{"基準日が不正: " + d, setD1("令和元年5月1日", d), []string{d1 + ":2:", "REFERENCE_DATE"}})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			in := baseInputs()
			c.mod(t, in)
			b, err := in.build(t)
			if err == nil {
				t.Fatalf("エラーにならない:\n%s", b)
			}
			t.Log(err)
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("メッセージに %q がない: %v", w, err)
				}
			}
		})
	}
}

// E-6: plan.islandName を書くと、ISLAND_NAME の書き方の違いを吸収できる。
func TestBuildPlanIslandName(t *testing.T) {
	t.Parallel()
	in := baseInputs()
	in.d2 = replaceOnce(t, in.d2, "甲地域,甲島、丁島", "甲地域, 甲ノ島 、丁島")
	in.sel = replaceOnce(t, in.sel, "region: 甲地域", "region: 甲地域\n      islandName: 甲ノ島")
	is := islandByID(t, decodeJSON(t, in.mustBuild(t)), "ko-jima")
	if got := str(t, is, "plan", "region"); got != "甲地域" {
		t.Errorf("plan.region = %s", got)
	}
}

// E-7: BOM の有無 × CRLF・LF で同じ結果。先頭の列が REFERENCE_DATE でも読める。引用符の中の改行は \n。
func TestBuildCSVVariants(t *testing.T) {
	t.Parallel()
	want := baseInputs().mustBuild(t)
	const bom = "\xEF\xBB\xBF"
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	variants := map[string]func(string) string{
		"BOM なし・LF":   func(s string) string { return s },
		"BOM なし・CRLF": crlf,
		"BOM あり・LF":   func(s string) string { return bom + s },
		"BOM あり・CRLF": func(s string) string { return bom + crlf(s) },
	}
	for name, f := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := baseInputs()
			in.d1, in.d2 = f(in.d1), f(in.d2)
			got := in.mustBuild(t)
			if !bytes.Equal(got, want) {
				t.Fatalf("結果が違う:\n%s", got)
			}
			climate := str(t, islandByID(t, decodeJSON(t, got), "ko-jima"), "plan", "climate", "text")
			if climate != "甲の気候\n二行目" {
				t.Errorf("引用符の中の改行: %q", climate)
			}
		})
	}
	t.Run("先頭の列が REFERENCE_DATE（BOM あり）", func(t *testing.T) {
		t.Parallel()
		in := baseInputs()
		h := header(t, in.d1)
		order := append([]string{"REFERENCE_DATE"}, h[0])
		order = append(order, h[2:]...)
		in.d1 = bom + crlf(rewriteCSV(t, in.d1, order, false))
		if got := in.mustBuild(t); !bytes.Equal(got, want) {
			t.Fatalf("結果が違う:\n%s", got)
		}
	})
}

// E-8: 列の順序の違い・使わない列があっても同じ結果。
func TestBuildColumnOrder(t *testing.T) {
	t.Parallel()
	want := baseInputs().mustBuild(t)
	for _, extra := range []bool{false, true} {
		in := baseInputs()
		in.d1 = rewriteCSV(t, in.d1, reversed(header(t, in.d1)), extra)
		in.d2 = rewriteCSV(t, in.d2, reversed(header(t, in.d2)), extra)
		if got := in.mustBuild(t); !bytes.Equal(got, want) {
			t.Errorf("extra=%v: 結果が違う:\n%s", extra, got)
		}
	}
}

// E-9: 計画書の空の列はキーを出さず、plan のない島に plan を出さず、文章をそのまま入れる。
func TestBuildPlan(t *testing.T) {
	t.Parallel()
	v := fixtureOutput(t)
	todai := islandByID(t, v, "hama-todai")
	for _, k := range []string{"climate", "populationHouseholds", "ports", "tourism"} {
		if _, ok := at(todai, "plan", k); ok {
			t.Errorf("hama-todai: 空の列の plan.%s がある", k)
		}
	}
	if got := str(t, todai, "plan", "maritime", "field"); got != "MARITIME_TRANSPORT" {
		t.Errorf("plan.maritime.field = %s", got)
	}
	if _, ok := at(islandByID(t, v, "shiomi-koishi"), "plan"); ok {
		t.Error("shiomi-koishi に plan がある")
	}
	hz := islandByID(t, v, "hama-hoshizuna")
	if got := str(t, hz, "plan", "tourism", "text"); got != " 星砂の浜と、月影灯台がある。 " {
		t.Errorf("TrimSpace されている: %q", got)
	}
	if _, ok := at(hz, "plan", "disaster"); ok {
		t.Error("空白だけの列の plan.disaster がある")
	}
	if got := str(t, hz, "plan", "ports", "text"); got != "星砂港, 月影港がある。" {
		t.Errorf("plan.ports.text = %q", got)
	}
	if got := str(t, hz, "plan", "climate", "text"); got != "温暖で、雨の少ない架空の気候である。\n冬は北風が強い。" {
		t.Errorf("plan.climate.text = %q", got)
	}
	for _, k := range []string{"provenance", "source"} {
		want := map[string]string{"provenance": "fact", "source": "fixture-2026"}[k]
		if got := str(t, hz, "plan", "geography", k); got != want {
			t.Errorf("plan.geography.%s = %s", k, got)
		}
	}
}

// E-10: 抜き出し。
func TestBuildExcerpts(t *testing.T) {
	t.Parallel()
	v := fixtureOutput(t)
	wantIDs := []string{"aa-todai-geography", "hoshizuna-climate", "zz-hoshizuna-tsukikage-minutes"}
	for i, id := range wantIDs {
		if got := str(t, v, "excerpts", i, "id"); got != id {
			t.Errorf("excerpts[%d].id = %s, want %s", i, got, id)
		}
		if got := str(t, v, "excerpts", i, "source"); got != "fixture-2026" {
			t.Errorf("excerpts[%d].source = %s", i, got)
		}
	}
	if _, ok := at(v, "excerpts", len(wantIDs)); ok {
		t.Error("excerpts が多い")
	}
	for _, k := range []string{"value", "unit", "note"} {
		if _, ok := at(v, "excerpts", 1, k); ok {
			t.Errorf("hoshizuna-climate に %s がある", k)
		}
	}
	if got := str(t, v, "excerpts", 2, "value"); got != "10" {
		t.Errorf("value = %s", got)
	}
	if got := str(t, v, "excerpts", 2, "unit"); got != "分" {
		t.Errorf("unit = %s", got)
	}

	// *.yaml がない（.gitkeep だけ）。
	in := baseInputs()
	in.curated = map[string]string{".gitkeep": ""}
	b := in.mustBuild(t)
	if !bytes.Contains(b, []byte(`"excerpts": []`)) {
		t.Errorf(`"excerpts": [] がない:\n%s`, b)
	}

	// サブディレクトリは見ない。
	in = baseInputs()
	raw, cur, sel := in.write(t)
	writeFile(t, filepath.Join(cur, "sub", "x.yaml"), "壊れた: [")
	if _, err := build(raw, cur, sel); err != nil {
		t.Errorf("サブディレクトリを読んでいる: %v", err)
	}
}

// --curated のディレクトリがない。
func TestBuildCuratedDirMissing(t *testing.T) {
	t.Parallel()
	raw, cur, sel := baseInputs().write(t)
	if err := os.RemoveAll(cur); err != nil {
		t.Fatal(err)
	}
	if _, err := build(raw, cur, sel); err == nil || !strings.Contains(err.Error(), cur) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(cur); !os.IsNotExist(err) {
		t.Fatal("--curated のディレクトリを作っている")
	}
}
