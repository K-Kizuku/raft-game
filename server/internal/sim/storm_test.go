package sim

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

// topLevelKeys は JSON のオブジェクト b の最上位のキーを順に返す。
func topLevelKeys(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("オブジェクトでない: %s", b)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// SE-11 JSON の形。
func TestStormEventJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ev   DomainEvent
		want string
		keys []string
	}{
		{
			"forecast",
			DomainEvent{Seq: 12, T: 5000, Type: EvForecast, Data: Forecast{Event: "storm", At: 10000, RouteIDs: []string{"1-2"}}},
			`{"seq":12,"t":5000,"type":"forecast","event":"storm","at":10000,"routeIds":["1-2"]}`,
			[]string{"event", "at", "routeIds"},
		},
		{
			"route.blocked",
			DomainEvent{Seq: 20, T: 10000, Type: EvRouteBlocked, Data: RouteBlocked{RouteIDs: []string{"1-2"}, Until: 30000}},
			`{"seq":20,"t":10000,"type":"route.blocked","routeIds":["1-2"],"until":30000}`,
			[]string{"routeIds", "until"},
		},
		{
			"route.unblocked",
			DomainEvent{Seq: 51, T: 30000, Type: EvRouteUnblocked, Data: RouteUnblocked{RouteIDs: []string{"1-2"}, Until: 30000}},
			`{"seq":51,"t":30000,"type":"route.unblocked","routeIds":["1-2"],"until":30000}`,
			[]string{"routeIds", "until"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.ev.Type != tt.ev.Data.EventType() {
				t.Errorf("EventType() = %s, want %s", tt.ev.Data.EventType(), tt.ev.Type)
			}
			b, err := json.Marshal(tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("json:\n got  %s\n want %s", b, tt.want)
			}
			d, err := json.Marshal(tt.ev.Data)
			if err != nil {
				t.Fatal(err)
			}
			keys := topLevelKeys(t, d)
			for _, k := range []string{"seq", "t", "type"} {
				if slices.Contains(keys, k) {
					t.Errorf("%T の JSON に %q がある: %s", tt.ev.Data, k, d)
				}
			}
			if !slices.Equal(keys, tt.keys) {
				t.Errorf("%T の JSON のキー = %v, want %v", tt.ev.Data, keys, tt.keys)
			}
		})
	}
}
