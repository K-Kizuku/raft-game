package protocol_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// PH-1: StageSummary のキーの順序と、null の出方。DisallowUnknownFields で読み戻すと同じ値。
func TestStageSummaryJSON(t *testing.T) {
	t.Parallel()
	unlock := "stage1"
	quote := "温暖"
	cases := []struct {
		name string
		in   protocol.StageSummary
		want string
	}{
		{
			name: "null の欄",
			in: protocol.StageSummary{
				ID: "stage1", Title: "t", Region: "r", Islands: 3, Ferries: 1, DurationMs: 180000,
				Intro:    protocol.StageText{Text: "紹介", Provenance: protocol.ProvenanceCreative},
				Learning: []string{},
			},
			want: `{"id":"stage1","title":"t","region":"r","islands":3,"ferries":1,"durationMs":180000,"unlock":null,` +
				`"intro":{"text":"紹介","provenance":"creative","source":null},"learning":[],"demo":false}`,
		},
		{
			name: "quote だけ null",
			in: protocol.StageSummary{
				ID: "stage2", Title: "t", Region: "r", Islands: 4, Ferries: 2, DurationMs: 240000, Unlock: &unlock,
				Intro: protocol.StageText{Text: "紹介", Provenance: protocol.ProvenanceDerived,
					Source: &protocol.TextSource{Dataset: "d", Region: "g", Field: "f"}},
				Learning: []string{"commit"},
				Demo:     true,
			},
			want: `{"id":"stage2","title":"t","region":"r","islands":4,"ferries":2,"durationMs":240000,"unlock":"stage1",` +
				`"intro":{"text":"紹介","provenance":"derived","source":{"dataset":"d","region":"g","field":"f","quote":null}},` +
				`"learning":["commit"],"demo":true}`,
		},
		{
			name: "すべての欄",
			in: protocol.StageSummary{
				ID: "stage2", Title: "t", Region: "r", Islands: 4, Ferries: 2, DurationMs: 240000, Unlock: &unlock,
				Intro: protocol.StageText{Text: "紹介", Provenance: protocol.ProvenanceFact,
					Source: &protocol.TextSource{Dataset: "d", Region: "g", Field: "f", Quote: &quote}},
				Learning: []string{"commit", "quorum"},
			},
			want: `{"id":"stage2","title":"t","region":"r","islands":4,"ferries":2,"durationMs":240000,"unlock":"stage1",` +
				`"intro":{"text":"紹介","provenance":"fact","source":{"dataset":"d","region":"g","field":"f","quote":"温暖"}},` +
				`"learning":["commit","quorum"],"demo":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("JSON =\n%s\nwant\n%s", got, tc.want)
			}
			var back protocol.StageSummary
			if err := decodeStrict(got, &back); err != nil {
				t.Fatalf("読み戻せない: %v", err)
			}
			if !reflect.DeepEqual(back, tc.in) {
				t.Errorf("読み戻した値 = %+v, want %+v", back, tc.in)
			}
		})
	}
}
