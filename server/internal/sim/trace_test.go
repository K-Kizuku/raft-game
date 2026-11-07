package sim

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

// sha256OfTrace は §10.2 の定義どおりにハッシュを計算する。
// 各イベントの json.Marshal に改行を 1 つ付けて連結し、その SHA-256 を取る。
func sha256OfTrace(t *testing.T, evs []DomainEvent) [32]byte {
	t.Helper()
	var b []byte
	for _, ev := range evs {
		j, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		b = append(b, j...)
		b = append(b, '\n')
	}
	return sha256.Sum256(b)
}

func TestTraceHashDefinition(t *testing.T) { // V-3
	t.Parallel()
	e := newEngine(t, testConfig(), Options{})
	var all []DomainEvent

	mustApply(t, e, Command{Type: CmdGameStart}) // S-1
	h := e.TraceHash()
	all = append(all, e.DrainEvents()...)
	if e.TraceHash() != h {
		t.Error("DrainEvents changed TraceHash")
	}
	if got := sha256OfTrace(t, all); got != h {
		t.Errorf("after start: TraceHash %x, want %x", h, got)
	}

	e.RunUntil(2000) // T-1
	h = e.TraceHash()
	if e.TraceHash() != h {
		t.Error("TraceHash changed by calling it")
	}
	all = append(all, e.DrainEvents()...)
	if e.TraceHash() != h {
		t.Error("DrainEvents changed TraceHash")
	}
	if got := sha256OfTrace(t, all); got != h {
		t.Errorf("after heartbeats: TraceHash %x, want %x", h, got)
	}
}
