package stageconv

import (
	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// Text は stage の出典区分つきの文（intro・flavor）を protocol.StageText にする。t を変えず、記憶を共有しない。
// Source が nil なら nil、Quote が "" なら nil（#113 の Summary の intro と同じ規則）。文字列は加工しない。
func Text(t stage.Text) protocol.StageText {
	out := protocol.StageText{Text: t.Text, Provenance: protocol.Provenance(t.Provenance)}
	if t.Source != nil {
		src := &protocol.TextSource{Dataset: t.Source.Dataset, Region: t.Source.Region, Field: t.Source.Field}
		if t.Source.Quote != "" {
			quote := t.Source.Quote
			src.Quote = &quote
		}
		out.Source = src
	}
	return out
}
