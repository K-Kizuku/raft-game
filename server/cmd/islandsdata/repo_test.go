package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// リポジトリのデータ（読むだけ）。
var (
	repoData    = filepath.Join("..", "..", "..", "data")
	repoRaw     = filepath.Join(repoData, "raw")
	repoCurated = filepath.Join(repoData, "curated")
	repoSelect  = filepath.Join(repoData, "islands-select.yaml")
	repoOut     = filepath.Join(repoData, "islands.json")
)

// repoIsland は、データ仕様 §3.2 の表の 1 行。
type repoIsland struct {
	id, name, prefecture, area string
	munis                      []string
	pop, km2                   string
}

var repoIslands = []repoIsland{
	{"kagawa-shodoshima", "小豆島", "香川県", "小豆島", []string{"小豆島町", "土庄町"}, "25881", "153.27"},
	{"kagawa-teshima", "豊島", "香川県", "小豆島", []string{"土庄町"}, "768", "14.5"},
	{"kagawa-naoshima", "直島", "香川県", "直島諸島", []string{"直島町"}, "3071", "7.82"},
	{"nagasaki-fukue", "福江島", "長崎県", "五島列島", []string{"五島市"}, "31945", "326.36"},
	{"nagasaki-naru", "奈留島", "長崎県", "五島列島", []string{"五島市"}, "1927", "23.68"},
	{"nagasaki-nakadori", "中通島", "長崎県", "五島列島", []string{"新上五島町"}, "16112", "168.39"},
	{"tokyo-oshima", "大島", "東京都", "伊豆諸島", []string{"大島町"}, "7102", "90.73"},
	{"tokyo-toshima", "利島", "東京都", "伊豆諸島", []string{"利島村"}, "327", "4.12"},
	{"tokyo-miyakejima", "三宅島", "東京都", "伊豆諸島", []string{"三宅村"}, "2273", "55.2"},
	{"tokyo-mikurajima", "御蔵島", "東京都", "伊豆諸島", []string{"御蔵島村"}, "323", "20.51"},
	{"tokyo-hachijojima", "八丈島", "東京都", "伊豆諸島", []string{"八丈町"}, "7042", "69.11"},
	{"tokyo-aogashima", "青ヶ島", "東京都", "伊豆諸島", []string{"青ヶ島村"}, "169", "5.96"},
	{"niigata-sado", "佐渡島", "新潟県", "佐渡島", []string{"佐渡市"}, "51492", "854.81"},
	{"hokkaido-rishiri", "利尻島", "北海道", "利尻島", []string{"利尻町", "利尻富士町"}, "4462", "182.08"},
	{"shimane-dogo", "島後", "島根県", "隠岐島", []string{"隠岐の島町"}, "13433", "241.53"},
	{"nagasaki-tsushima", "対馬島", "長崎県", "対馬島", []string{"対馬市"}, "28374", "695.74"},
}

// S-1: data/islands-select.yaml が検証を通り、§3.2 の 16 島を同じ順・同じ値で持つ。
func TestRepoSelect(t *testing.T) {
	t.Parallel()
	s, err := loadSelect(repoSelect)
	if err != nil {
		t.Fatalf("loadSelect: %v", err)
	}
	if len(s.Islands) != len(repoIslands) {
		t.Fatalf("島の数 = %d, want %d", len(s.Islands), len(repoIslands))
	}
	for i, w := range repoIslands {
		g := s.Islands[i]
		if g.ID != w.id || g.Name != w.name || g.Prefecture != w.prefecture || g.DesignatedArea != w.area ||
			!slices.Equal(g.Municipalities, w.munis) {
			t.Errorf("%d 番目 = %+v, want %+v", i+1, g, w)
		}
	}
}

// R-1: 提供データがあるとき、data/islands.json が最新で、§3.2 の値と同じ。
func TestRepoRealData(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(filepath.Join(repoRaw, d1File)); err != nil {
		t.Skipf("提供データがない（%v）。data/raw/README.md の手順で提供データを置く", err)
	}
	got, err := build(repoRaw, repoCurated, repoSelect)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want, err := os.ReadFile(repoOut)
	if err != nil {
		t.Fatalf("data/islands.json がない。data/raw/README.md の手順で生成する: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("data/islands.json が生成結果と違う。data/raw/README.md の手順で再生成する")
	}
	v := decodeJSON(t, got)
	for _, w := range repoIslands {
		is := islandByID(t, v, w.id)
		if p := str(t, is, "population", "value"); p != w.pop {
			t.Errorf("%s: 人口 = %s, want %s", w.id, p, w.pop)
		}
		if a := str(t, is, "areaKm2", "value"); a != w.km2 {
			t.Errorf("%s: 面積 = %s, want %s", w.id, a, w.km2)
		}
		munis, _ := at(is, "municipalities")
		list, _ := munis.([]any)
		var names []string
		for _, m := range list {
			s, _ := m.(string)
			names = append(names, s)
		}
		if !slices.Equal(names, w.munis) {
			t.Errorf("%s: 自治体 = %v, want %v", w.id, names, w.munis)
		}
	}
}
