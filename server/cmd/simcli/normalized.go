package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/simtrace"
)

// normalizedTraceUsage は --normalized-trace の説明（record・replay で共通）。
const normalizedTraceUsage = "正規化トレース（docs/dtn/simulation-time.md §5、simtrace.Normalize）を JSON で FILE に書く"

// writeNormalizedTrace は events（と、あれば round.result）の正規化トレースを、2 字下げの JSON にして path に書く。
// 終了コードを返す。name はサブコマンドの名前（stderr のメッセージに使う）。
func writeNormalizedTrace(name, path string, events []sim.DomainEvent, result *sim.RoundResult, stderr io.Writer) int {
	tr := simtrace.Normalize(events)
	if result != nil {
		tr = tr.WithResult(*result)
	}
	out, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 正規化トレースを作れない（内部の誤り）: %v\n", err)
		return exitFail
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "simcli: 正規化トレースを書けない: %v\n", err)
		return exitFail
	}
	_, _ = fmt.Fprintf(stderr, "simcli %s: 正規化トレースを %s に書いた（streams=%d）\n", name, path, len(tr.Streams))
	return exitOK
}

// engineResult は e の round.result を返す（採点しないステージ・終わっていないラウンドなら nil）。
func engineResult(e *sim.Engine) *sim.RoundResult {
	if e == nil {
		return nil
	}
	r, ok := e.Result()
	if !ok {
		return nil
	}
	return &r
}
