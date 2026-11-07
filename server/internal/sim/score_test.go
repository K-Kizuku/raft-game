package sim

import (
	"encoding/json"
	"testing"
)

func TestScoreJSON(t *testing.T) { // S-15
	t.Parallel()
	for _, tc := range []struct {
		ev   DomainEvent
		want string
	}{
		{
			DomainEvent{Seq: 31, T: 3750, Type: EvIslandUpdated, Data: islandUp(2, 60, 3750)},
			`{"seq":31,"t":3750,"type":"island.updated","id":2,"satisfaction":60,"lastDeliveredAt":3750}`,
		},
		{
			DomainEvent{Seq: 58, T: 9000, Type: EvScoreUpdated, Data: scoreUp(96, 1)},
			`{"seq":58,"t":9000,"type":"score.updated","total":96,"successes":1,"criticalSecured":[]}`,
		},
	} {
		b, err := json.Marshal(tc.ev)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("json = %s, want %s", b, tc.want)
		}
		if tc.ev.Type != tc.ev.Data.EventType() {
			t.Errorf("EventType() = %s, want %s", tc.ev.Data.EventType(), tc.ev.Type)
		}
	}

	r := RoundResult{
		Score: ResultScore{Total: 396, Breakdown: ScoreBreakdown{Requests: 100, Expired: 0, FerryCost: -4, Fairness: 300}},
		Stars: 1,
		Stats: ResultStats{Succeeded: 1},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"score":{"total":396,"breakdown":{"requests":100,"expired":0,"ferryCost":-4,"fairness":300}},"stars":1,"stats":{"succeeded":1,"rejected":0,"expired":0}}`
	if string(b) != want {
		t.Errorf("Result json = %s, want %s", b, want)
	}

	for _, d := range []EventData{IslandUpdated{}, ScoreUpdated{CriticalSecured: []string{}}} {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var keys struct {
			Seq  *json.RawMessage `json:"seq"`
			T    *json.RawMessage `json:"t"`
			Type *json.RawMessage `json:"type"`
		}
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		if keys.Seq != nil || keys.T != nil || keys.Type != nil {
			t.Errorf("%T has a reserved key: %s", d, raw)
		}
	}
}

// S-1 の Result() の JSON（固定値と同じもの）。
func TestScoreResultJSONFromRun(t *testing.T) {
	t.Parallel()
	e, _ := e1Events(t, scoreTestConfig(stallReq(0, 1)), 120000)
	b, err := json.Marshal(mustResult(t, e))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"score":{"total":396,"breakdown":{"requests":100,"expired":0,"ferryCost":-4,"fairness":300}},"stars":1,"stats":{"succeeded":1,"rejected":0,"expired":0}}`
	if string(b) != want {
		t.Errorf("Result json = %s, want %s", b, want)
	}
}
