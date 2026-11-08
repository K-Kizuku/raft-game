package stage_test

import (
	"os"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// リポジトリのステージ定義の郵便局長（islands[].postmaster。ゲームデザイン §7.3、T-406c）。

// loadRepoStagesForPostmaster はリポジトリの data/islands.json と data/stages/*.yaml を読む。
func loadRepoStagesForPostmaster(t *testing.T) []*stage.Stage {
	t.Helper()
	stages, err := stage.LoadDir(os.DirFS("../../.."), "data/islands.json", "data/stages")
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) == 0 {
		t.Fatal("data/stages にステージがない")
	}
	return stages
}

// PM-R1 すべてのステージの、すべての島に郵便局長がいる。
func TestRepoPostmasterEveryIsland(t *testing.T) {
	t.Parallel()
	for _, st := range loadRepoStagesForPostmaster(t) {
		for _, isl := range st.Islands {
			if isl.Postmaster == nil {
				t.Errorf("%s: %s（%s）に postmaster がない", st.ID, isl.Ref, isl.Name)
			}
		}
	}
}

// PM-R2 同じ島は、どのステージでも同じ郵便局長。
func TestRepoPostmasterSameAcrossStages(t *testing.T) {
	t.Parallel()
	type seen struct {
		stageID string
		pm      stage.Postmaster
	}
	first := map[string]seen{}
	for _, st := range loadRepoStagesForPostmaster(t) {
		for _, isl := range st.Islands {
			if isl.Postmaster == nil {
				continue // PM-R1 で検出する
			}
			pm := *isl.Postmaster
			pm.Source = nil // 比べるのは Name・Tone・Portrait・Provenance
			prev, ok := first[isl.Ref]
			if !ok {
				first[isl.Ref] = seen{st.ID, pm}
				continue
			}
			if prev.pm != pm {
				t.Errorf("%s: %s の郵便局長 %+v が %s の %+v と違う", st.ID, isl.Ref, pm, prev.stageID, prev.pm)
			}
		}
	}
}

// PM-R3 名前の由来が計画書の記述にある島（小豆島・八丈島）。
func TestRepoPostmasterDerived(t *testing.T) {
	t.Parallel()
	want := map[string]struct{ name, quote string }{
		"kagawa-shodoshima": {"オリーブ局長", "オリーブ製品"},
		"tokyo-hachijojima": {"フルーツレモン局長", "八丈フルーツレモン"},
	}
	foundShodoshima := false
	for _, st := range loadRepoStagesForPostmaster(t) {
		for _, isl := range st.Islands {
			w, ok := want[isl.Ref]
			if !ok || isl.Postmaster == nil {
				continue
			}
			if isl.Ref == "kagawa-shodoshima" {
				foundShodoshima = true
			}
			pm := isl.Postmaster
			if pm.Name != w.name || pm.Provenance != stage.Derived {
				t.Errorf("%s: %s の郵便局長 = %q（%s）, want %q（derived）", st.ID, isl.Ref, pm.Name, pm.Provenance, w.name)
			}
			if pm.Source == nil || pm.Source.Quote != w.quote {
				t.Errorf("%s: %s の郵便局長の source = %+v, want quote %q", st.ID, isl.Ref, pm.Source, w.quote)
			}
		}
	}
	if !foundShodoshima {
		t.Error("小豆島（kagawa-shodoshima）の郵便局長がどのステージにもない")
	}
}

// PM-R4 1 つのステージの中で、郵便局長の名前が重複しない。
func TestRepoPostmasterUniqueNames(t *testing.T) {
	t.Parallel()
	for _, st := range loadRepoStagesForPostmaster(t) {
		names := map[string]string{}
		for _, isl := range st.Islands {
			if isl.Postmaster == nil {
				continue
			}
			if prev, dup := names[isl.Postmaster.Name]; dup {
				t.Errorf("%s: 郵便局長の名前 %q が %s と %s で重複している", st.ID, isl.Postmaster.Name, prev, isl.Ref)
			}
			names[isl.Postmaster.Name] = isl.Ref
		}
	}
}
