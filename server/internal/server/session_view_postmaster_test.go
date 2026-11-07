package server

import (
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// snapshot の stage.islands[].postmaster（プロトコル §5.1。T-406c）。

func postmasterTestStage() *stage.Stage {
	return &stage.Stage{
		ID:    "x",
		Title: "題",
		Islands: []stage.Island{
			{NodeID: 1, Name: "一", X: 10, Y: 20, Color: "#000000", Population: 100,
				Postmaster: &stage.Postmaster{Name: "いち局長", Tone: "polite", Provenance: stage.Creative}},
			{NodeID: 2, Name: "二", X: 30, Y: 40, Color: "#FFFFFF", Population: 200},
			{NodeID: 3, Name: "三", X: 50, Y: 60, Color: "#123456", Population: 300,
				Postmaster: &stage.Postmaster{Name: "さん局長", Tone: "elder", Portrait: "san.webp", Provenance: stage.Derived,
					Source: &stage.Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "LOCAL_INDUSTRY_TOURISM", Quote: "引用"}}},
		},
	}
}

func TestStageViewPostmaster(t *testing.T) { // SV-P1
	t.Parallel()
	st := postmasterTestStage()
	v := stageView(st, nil)

	if got, want := v.Islands[0].Postmaster, (protocol.Postmaster{Name: "いち局長", Tone: "polite"}); got == nil || *got != want {
		t.Errorf("Islands[0].Postmaster = %+v, want %+v", got, want)
	}
	if got, want := v.Islands[2].Postmaster, (protocol.Postmaster{Name: "さん局長", Tone: "elder", Portrait: "san.webp"}); got == nil || *got != want {
		t.Errorf("Islands[2].Postmaster = %+v, want %+v", got, want)
	}
	if got := mustJSON(t, v.Islands[0]); !strings.HasSuffix(got, `"color":"#000000","postmaster":{"name":"いち局長","tone":"polite"}}`) {
		t.Errorf("islands[0] = %s", got)
	}
	if got := mustJSON(t, v.Islands[2]); !strings.HasSuffix(got, `"postmaster":{"name":"さん局長","tone":"elder","portrait":"san.webp"}}`) {
		t.Errorf("islands[2] = %s", got)
	}
	// provenance・source はプロトコルに載せない。
	if got := mustJSON(t, v.Islands[2]); strings.Contains(got, "derived") || strings.Contains(got, "引用") {
		t.Errorf("islands[2] に postmaster の出典が出ている: %s", got)
	}

	// stage.Postmaster と記憶を共有しない。
	v.Islands[0].Postmaster.Name = "変更"
	if st.Islands[0].Postmaster.Name != "いち局長" {
		t.Errorf("stage.Postmaster が変わった: %+v", st.Islands[0].Postmaster)
	}
}

func TestStageViewNoPostmaster(t *testing.T) { // SV-P2
	t.Parallel()
	v := stageView(postmasterTestStage(), nil)
	if v.Islands[1].Postmaster != nil {
		t.Errorf("Islands[1].Postmaster = %+v, want nil", v.Islands[1].Postmaster)
	}
	if got := mustJSON(t, v.Islands[1]); strings.Contains(got, "postmaster") {
		t.Errorf("islands[1] に postmaster のキーがある: %s", got)
	}
}
