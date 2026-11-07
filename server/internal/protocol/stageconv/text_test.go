package stageconv

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// TX-1 Text（T-305b）。
func TestText(t *testing.T) {
	t.Parallel()
	got := Text(stage.Text{Text: " 海が荒れています ", Provenance: stage.Provenance("creative")})
	want := protocol.StageText{Text: " 海が荒れています ", Provenance: "creative"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Text(no source) = %+v, want %+v", got, want)
	}
	if got.Source != nil {
		t.Errorf("Source = %+v, want nil", got.Source)
	}

	src := &stage.Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "DISASTER_LAND_CONSERVATION"}
	in := stage.Text{Text: "海が荒れています", Provenance: stage.Provenance("derived"), Source: src}
	got = Text(in)
	if got.Source == nil || got.Source.Quote != nil {
		t.Fatalf("Text(quote \"\").Source = %+v, want quote nil", got.Source)
	}
	if got.Source.Dataset != src.Dataset || got.Source.Region != src.Region || got.Source.Field != src.Field {
		t.Errorf("Source = %+v, want %+v", got.Source, src)
	}

	src.Quote = " 引用 "
	got = Text(in)
	if got.Source == nil || got.Source.Quote == nil || *got.Source.Quote != " 引用 " {
		t.Fatalf("Text(quote).Source = %+v, want quote %q", got.Source, " 引用 ")
	}
	if got.Text != "海が荒れています" || got.Provenance != "derived" {
		t.Errorf("Text = %q, Provenance = %q", got.Text, got.Provenance)
	}
	// 記憶を共有しない。
	*got.Source.Quote = "x"
	got.Source.Dataset = "y"
	if src.Quote != " 引用 " || src.Dataset != "fixture-2026" {
		t.Errorf("Text shares memory with its input: %+v", src)
	}
}
