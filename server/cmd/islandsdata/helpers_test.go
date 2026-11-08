package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "testdata/golden/islands.json を更新する")

const (
	fixtureRaw     = "testdata/raw"
	fixtureCurated = "testdata/curated"
	fixtureSelect  = "testdata/select.yaml"
	fixtureGolden  = "testdata/golden/islands.json"
)

// 小さな入力（テストの中で書き換えて使う）。値はすべて架空。
const baseD1 = `_id,REFERENCE_DATE,PREFECTURE_NAME_CODE,DESIGNATED_AREA_NAME_CODE,ISLAND_NAME_CODE,MUNICIPALITY_NAME_CODE,POPULATION,AREA
1,令和元年5月1日,甲県,甲諸島,甲島,甲町,100,9.5
2,令和8年4月1日,甲県,甲諸島,甲島,甲町,"1,000",1.5
3,令和8年4月1日,甲県,甲諸島,甲島,乙町,200,2.25
4,令和8年4月1日,乙県,乙諸島,丙島,丙村,30,0.3
`

const baseD2 = `_id,DESIGNATED_REGION_NAME,ISLAND_NAME,GEO_LOCATION_AREA,NATURAL_CLIMATE_FEATURES,POPULATION_HOUSEHOLDS,MARITIME_TRANSPORT,PORTS,LOCAL_INDUSTRY_TOURISM,DISASTER_LAND_CONSERVATION
1,甲地域,甲島、丁島,甲の地理,"甲の気候
二行目",甲の人口,甲~丁(5km・20分,甲港,甲の観光,甲の防災
`

const baseSelect = `format: 1
generatedAt: "2026-11-07"
dataset: test
sources:
  test:
    title: t
    publisher: p
    license: l
    termsUrl: https://example.com/
    publishedAt: "2026-10-31"
islands:
  - id: ko-jima
    prefecture: 甲県
    name: 甲島
    designatedArea: 甲諸島
    municipalities: [甲町, 乙町]
    plan:
      region: 甲地域
  - id: otsu-heijima
    prefecture: 乙県
    name: 丙島
    designatedArea: 乙諸島
    municipalities: [丙村]
`

const baseCuratedA = `format: 1
excerpts:
  - id: ko-minutes
    region: 甲地域
    field: MARITIME_TRANSPORT
    quote: "甲~丁(5km・20分"
    provenance: derived
    value: 20
    unit: 分
`

// inputs は、テストの中で作る入力。
type inputs struct {
	d1, d2, sel string
	curated     map[string]string // ファイル名 → 中身
	noD1, noD2  bool
}

func baseInputs() *inputs {
	return &inputs{d1: baseD1, d2: baseD2, sel: baseSelect, curated: map[string]string{"a.yaml": baseCuratedA}}
}

// write は入力を t.TempDir() に書き、build に渡すパスを返す。
func (in *inputs) write(t *testing.T) (raw, curated, sel string) {
	t.Helper()
	dir := t.TempDir()
	raw = filepath.Join(dir, "raw")
	curated = filepath.Join(dir, "curated")
	sel = filepath.Join(dir, "islands-select.yaml")
	mustMkdir(t, curated)
	if !in.noD1 {
		writeFile(t, filepath.Join(raw, d1File), in.d1)
	}
	if !in.noD2 {
		writeFile(t, filepath.Join(raw, d2File), in.d2)
	}
	mustMkdir(t, raw)
	writeFile(t, sel, in.sel)
	for _, name := range slices.Sorted(maps.Keys(in.curated)) {
		writeFile(t, filepath.Join(curated, name), in.curated[name])
	}
	return raw, curated, sel
}

func (in *inputs) build(t *testing.T) ([]byte, error) {
	t.Helper()
	raw, cur, sel := in.write(t)
	return build(raw, cur, sel)
}

func (in *inputs) mustBuild(t *testing.T) []byte {
	t.Helper()
	b, err := in.build(t)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return b
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// replaceOnce は old がちょうど 1 回現れることを確かめてから置き換える。
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if n := strings.Count(s, old); n != 1 {
		t.Fatalf("%q が %d 回ある（1 回のはず）", old, n)
	}
	return strings.Replace(s, old, new, 1)
}

// decodeJSON は数を json.Number のまま読む。
func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("JSON を読めない: %v", err)
	}
	return v
}

// at は、キー（string）か添字（int）をたどって値を返す。ないときは nil と false。
func at(v any, path ...any) (any, bool) {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			if v, ok = m[k]; !ok {
				return nil, false
			}
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil, false
			}
			v = a[k]
		}
	}
	return v, true
}

// str は at の値を文字列にする（json.Number は数の文字列）。
func str(t *testing.T, v any, path ...any) string {
	t.Helper()
	x, ok := at(v, path...)
	if !ok {
		t.Fatalf("%v がない", path)
	}
	switch s := x.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	}
	t.Fatalf("%v は文字列でも数でもない: %#v", path, x)
	return ""
}

// islandByID は、出力の islands から id の島を返す。
func islandByID(t *testing.T, v map[string]any, id string) map[string]any {
	t.Helper()
	list, _ := v["islands"].([]any)
	for _, x := range list {
		m := x.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("島 %s がない", id)
	return nil
}

// rewriteCSV は CSV を読み、列を order の順に並べ替えて書き直す（order にない列は落とす）。
// extra を true にすると、使わない列を最後に足す。
func rewriteCSV(t *testing.T, s string, order []string, extra bool) string {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, h := range recs[0] {
		idx[h] = i
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for i, r := range recs {
		var out []string
		for _, c := range order {
			j, ok := idx[c]
			if !ok {
				t.Fatalf("列 %s がない", c)
			}
			out = append(out, r[j])
		}
		if extra {
			if i == 0 {
				out = append(out, "UNUSED_COLUMN")
			} else {
				out = append(out, "使わない値")
			}
		}
		if err := w.Write(out); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	return buf.String()
}

func reversed(s []string) []string {
	r := slices.Clone(s)
	slices.Reverse(r)
	return r
}

func header(t *testing.T, s string) []string {
	t.Helper()
	line, _, _ := strings.Cut(s, "\n")
	return strings.Split(line, ",")
}
