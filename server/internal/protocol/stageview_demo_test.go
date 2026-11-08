package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// PV-1 StageView の demoStorm の JSON（T-305b）。
func TestStageViewDemoStormJSON(t *testing.T) {
	t.Parallel()
	v := StageView{Title: "t", Islands: []Island{}, Routes: []Route{}, Resources: []Resource{}}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "demoStorm") {
		t.Errorf("JSON without DemoStorm = %s, want no demoStorm key", b)
	}

	v.DemoStorm = &DemoStormView{RouteIDs: []string{"1-2"}}
	b, err = json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"demoStorm":{"routeIds":["1-2"],"flavor":null}`; !strings.Contains(string(b), want) {
		t.Errorf("JSON = %s, want it to contain %s", b, want)
	}

	quote := "引用"
	v.DemoStorm.Flavor = &StageText{
		Text: "海が荒れています", Provenance: "derived",
		Source: &TextSource{Dataset: "fixture-2026", Region: "星砂地域", Field: "DISASTER_LAND_CONSERVATION", Quote: &quote},
	}
	b, err = json.Marshal(v.DemoStorm)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"routeIds":["1-2"],"flavor":{"text":"海が荒れています","provenance":"derived",` +
		`"source":{"dataset":"fixture-2026","region":"星砂地域","field":"DISASTER_LAND_CONSERVATION","quote":"引用"}}}`
	if string(b) != want {
		t.Errorf("JSON = %s, want %s", b, want)
	}
}
