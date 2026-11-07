package stageconv_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

func ptr[T any](v T) *T { return &v }

// fullStage は、すべての欄があるステージ（島 3・船 2）。呼ぶたびに新しく作る。
func fullStage() *stage.Stage {
	return &stage.Stage{
		ID:     "stage2",
		Title:  "嵐の群島",
		Region: "五島列島（長崎県）",
		Intro: stage.Text{
			Text:       " 世界遺産の島々。 ",
			Provenance: stage.Derived,
			Source:     &stage.Source{Dataset: "mlit-ritou-2026", Region: "離島振興対策実施地域", Field: "LOCAL_INDUSTRY_TOURISM", Quote: "世界遺産"},
		},
		Learning:   []string{"election", "term"},
		DurationMs: 240000,
		Unlock:     "stage1",
		Islands:    []stage.Island{{NodeID: 1}, {NodeID: 2}, {NodeID: 3}},
		Ferries:    []stage.Ferry{{ID: "f1"}, {ID: "f2"}},
		Demo:       &stage.Demo{EndOnAllCriticalSecured: true},
	}
}

// SC-1: すべての欄がある Stage。
func TestSummaryFull(t *testing.T) {
	t.Parallel()
	got := stageconv.Summary(fullStage())
	want := protocol.StageSummary{
		ID:         "stage2",
		Title:      "嵐の群島",
		Region:     "五島列島（長崎県）",
		Islands:    3,
		Ferries:    2,
		DurationMs: 240000,
		Unlock:     ptr("stage1"),
		Intro: protocol.StageText{
			Text:       " 世界遺産の島々。 ",
			Provenance: protocol.ProvenanceDerived,
			Source:     &protocol.TextSource{Dataset: "mlit-ritou-2026", Region: "離島振興対策実施地域", Field: "LOCAL_INDUSTRY_TOURISM", Quote: ptr("世界遺産")},
		},
		Learning: []string{"election", "term"},
		Demo:     true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Summary =\n%+v\nwant\n%+v", got, want)
	}
}

// SC-2: ない場合。
func TestSummaryAbsent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		modify func(st *stage.Stage)
		check  func(t *testing.T, s protocol.StageSummary)
	}{
		{"Unlock が空", func(st *stage.Stage) { st.Unlock = "" }, func(t *testing.T, s protocol.StageSummary) {
			if s.Unlock != nil {
				t.Errorf("Unlock = %q, want nil", *s.Unlock)
			}
		}},
		{"Intro.Source が nil", func(st *stage.Stage) { st.Intro.Source = nil }, func(t *testing.T, s protocol.StageSummary) {
			if s.Intro.Source != nil {
				t.Errorf("Intro.Source = %+v, want nil", s.Intro.Source)
			}
		}},
		{"Quote が空", func(st *stage.Stage) { st.Intro.Source.Quote = "" }, func(t *testing.T, s protocol.StageSummary) {
			if s.Intro.Source == nil || s.Intro.Source.Quote != nil {
				t.Errorf("Intro.Source = %+v, want Quote nil", s.Intro.Source)
			}
		}},
		{"Learning が nil", func(st *stage.Stage) { st.Learning = nil }, func(t *testing.T, s protocol.StageSummary) {
			if s.Learning == nil || len(s.Learning) != 0 {
				t.Errorf("Learning = %#v, want []string{}", s.Learning)
			}
			b, err := json.Marshal(s.Learning)
			if err != nil || string(b) != "[]" {
				t.Errorf("Learning の JSON = %s (%v), want []", b, err)
			}
		}},
		{"Learning が空", func(st *stage.Stage) { st.Learning = []string{} }, func(t *testing.T, s protocol.StageSummary) {
			if s.Learning == nil || len(s.Learning) != 0 {
				t.Errorf("Learning = %#v, want []string{}", s.Learning)
			}
		}},
		{"Demo が nil", func(st *stage.Stage) { st.Demo = nil }, func(t *testing.T, s protocol.StageSummary) {
			if s.Demo {
				t.Error("Demo = true, want false")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := fullStage()
			tc.modify(st)
			tc.check(t, stageconv.Summary(st))
		})
	}
}

// SC-3: Summaries が順序を保つ。nil・空は長さ 0 で nil でない。
func TestSummaries(t *testing.T) {
	t.Parallel()
	a, b, c := fullStage(), fullStage(), fullStage()
	a.ID, b.ID, c.ID = "c", "a", "b"
	got := stageconv.Summaries([]*stage.Stage{a, b, c})
	if len(got) != 3 || got[0].ID != "c" || got[1].ID != "a" || got[2].ID != "b" {
		t.Errorf("Summaries の順序 = %+v", got)
	}
	for name, in := range map[string][]*stage.Stage{"nil": nil, "空": {}} {
		out := stageconv.Summaries(in)
		if out == nil || len(out) != 0 {
			t.Errorf("%s: Summaries = %#v, want 長さ 0 で nil でない", name, out)
		}
		if b, err := json.Marshal(out); err != nil || string(b) != "[]" {
			t.Errorf("%s: JSON = %s (%v), want []", name, b, err)
		}
	}
}

// SC-4: 返した値を書き換えても、元の Stage が変わらない。
func TestSummaryNoAlias(t *testing.T) {
	t.Parallel()
	st := fullStage()
	s := stageconv.Summary(st)
	s.Learning[0] = "x"
	*s.Unlock = "x"
	s.Intro.Source.Dataset = "x"
	*s.Intro.Source.Quote = "x"
	if !reflect.DeepEqual(st, fullStage()) {
		t.Errorf("元の Stage が変わった: %+v", st)
	}
}

// SC-5: JSON の文字列（キーの順序と null の出方）。
func TestSummaryJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		modify func(st *stage.Stage)
		want   string
	}{
		{"すべての欄", func(*stage.Stage) {},
			`{"id":"stage2","title":"嵐の群島","region":"五島列島（長崎県）","islands":3,"ferries":2,"durationMs":240000,"unlock":"stage1",` +
				`"intro":{"text":" 世界遺産の島々。 ","provenance":"derived","source":{"dataset":"mlit-ritou-2026","region":"離島振興対策実施地域","field":"LOCAL_INDUSTRY_TOURISM","quote":"世界遺産"}},` +
				`"learning":["election","term"],"demo":true}`},
		{"ない欄", func(st *stage.Stage) {
			st.Unlock = ""
			st.Intro = stage.Text{Text: "紹介", Provenance: stage.Creative}
			st.Learning = nil
			st.Demo = nil
		},
			`{"id":"stage2","title":"嵐の群島","region":"五島列島（長崎県）","islands":3,"ferries":2,"durationMs":240000,"unlock":null,` +
				`"intro":{"text":"紹介","provenance":"creative","source":null},"learning":[],"demo":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := fullStage()
			tc.modify(st)
			b, err := json.Marshal(stageconv.Summary(st))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("JSON =\n%s\nwant\n%s", b, tc.want)
			}
		})
	}
}
