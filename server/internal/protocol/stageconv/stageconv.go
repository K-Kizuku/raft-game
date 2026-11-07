// Package stageconv は、stage の型と protocol の型を変換する（T-311）。
//
// protocol（標準ライブラリだけ）と stage（ほかのパッケージを import しない）を変えずに、両方を import する。
// GET /api/stages の 1 行の形は、ここだけで作る（Worker に同梱する一覧を作る cmd/stagecatalog と、
// standalone の /api/stages が使う）。浮動小数点・実時間・乱数・map の走査を使わない。
package stageconv

import (
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// Summary は検証済みの Stage から、GET /api/stages の 1 行を作る。st を変えず、st と記憶を共有しない。
func Summary(st *stage.Stage) protocol.StageSummary {
	s := protocol.StageSummary{
		ID:         st.ID,
		Title:      st.Title,
		Region:     st.Region,
		Islands:    len(st.Islands),
		Ferries:    len(st.Ferries),
		DurationMs: st.DurationMs,
		Intro:      Text(st.Intro),
		Learning:   []string{},
		Demo:       st.Demo != nil,
	}
	if st.Unlock != "" {
		unlock := st.Unlock
		s.Unlock = &unlock
	}
	if len(st.Learning) > 0 {
		s.Learning = slices.Clone(st.Learning)
	}
	return s
}

// Summaries は sts の順のまま Summary を並べる。sts が空でも nil でなく長さ 0 の列を返す（JSON で []）。
func Summaries(sts []*stage.Stage) []protocol.StageSummary {
	out := make([]protocol.StageSummary, 0, len(sts))
	for _, st := range sts {
		out = append(out, Summary(st))
	}
	return out
}
