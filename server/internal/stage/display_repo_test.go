package stage

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func repoDisplay(t *testing.T) *Display {
	t.Helper()
	if _, err := os.Stat("../../../data/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない。提供データを置いて islandsdata で作る（data/raw/README.md）")
	}
	d, err := LoadDisplay(os.DirFS("../../../data"), "islands.json")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// DR-1 リポジトリの data/islands.json の表示用の値。
func TestRepoDisplay(t *testing.T) {
	t.Parallel()
	d := repoDisplay(t)

	sho, ok := d.Lookup("kagawa-shodoshima")
	if !ok {
		t.Fatal("kagawa-shodoshima がない")
	}
	if got := sho.MunicipalityLabel(); got != "小豆島町・土庄町" {
		t.Errorf("MunicipalityLabel() = %q", got)
	}
	if sho.Population.Provenance != Derived {
		t.Errorf("Population.Provenance = %q", sho.Population.Provenance)
	}
	if sho.Area.Value != 1532700 || sho.Area.Text != "153.27" || sho.Area.Provenance != Derived {
		t.Errorf("Area = %+v", sho.Area)
	}

	nao, ok := d.Lookup("kagawa-naoshima")
	if !ok || nao.Plan != nil {
		t.Errorf("kagawa-naoshima = %+v, %v", nao, ok)
	}

	dogo, ok := d.Lookup("shimane-dogo")
	if !ok || dogo.Plan == nil || len(dogo.Plan.Texts) == 0 {
		t.Fatalf("shimane-dogo = %+v, %v", dogo, ok)
	}
	for _, pt := range dogo.Plan.Texts {
		if pt.Key == "geography" {
			t.Error("shimane-dogo に geography がある")
		}
	}

	if len(d.Excerpts) != 11 {
		t.Errorf("Excerpts = %d 件", len(d.Excerpts))
	}
	if len(d.Sources) != 1 || d.Sources[0].Key != "mlit-ritou-2026" {
		t.Errorf("Sources = %+v", d.Sources)
	}
}

// DR-2 すべての島で Population.Value が ParseIslands の Population と同じ。
func TestRepoDisplayPopulation(t *testing.T) {
	t.Parallel()
	d := repoDisplay(t)
	data, err := os.ReadFile("../../../data/islands.json")
	if err != nil {
		t.Fatal(err)
	}
	is, err := ParseIslands("islands.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Islands) != len(is.Islands) {
		t.Fatalf("島の数 = %d, want %d", len(d.Islands), len(is.Islands))
	}
	for _, base := range is.Islands {
		got, ok := d.Lookup(base.ID)
		if !ok {
			t.Errorf("%s がない", base.ID)
			continue
		}
		want, _ := is.Lookup(base.ID)
		if got.Population.Value != want.Population || got.Name != want.Name {
			t.Errorf("%s: Population.Value = %d, want %d", base.ID, got.Population.Value, want.Population)
		}
	}
}
