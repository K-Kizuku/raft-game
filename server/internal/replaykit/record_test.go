package replaykit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// recordTestRun は data/ の stage1 を Engine で少し進め、記録に要る値を返す。
func recordTestRun(t *testing.T) (*stage.Stage, sim.Snapshot, []sim.DomainEvent, string) {
	t.Helper()
	stages, err := stage.LoadDir(os.DirFS("../../../data"), "islands.json", "stages")
	if err != nil {
		t.Fatal(err)
	}
	var st *stage.Stage
	for _, s := range stages {
		if s.ID == "stage1" {
			st = s
		}
	}
	if st == nil {
		t.Fatal("stage1 がない")
	}
	cfg, err := stage.SimConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sim.New(cfg, 1, sim.Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot()
	if ack := e.Apply(sim.Command{Type: sim.CmdGameStart}); !ack.OK {
		t.Fatalf("start: %+v", ack)
	}
	e.RunUntil(20000)
	events := e.DrainEvents()
	if len(events) < 2 {
		t.Fatalf("events = %d", len(events))
	}
	sum := e.TraceHash()
	return st, snap, events, "sha256:" + hex.EncodeToString(sum[:])
}

// RK-2: イベントの各行の末尾の , を除いて改行を付けて連結した SHA-256 が、ヘッダーの traceHash と一致する。
func TestEncodeRecordTraceHash(t *testing.T) {
	t.Parallel()
	st, snap, events, traceHash := recordTestRun(t)
	out, err := EncodeRecord(NewRecordHeader("test", st, 1, "", len(events), traceHash), snap, events)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || !bytes.HasSuffix(out, []byte("\n]}\n")) {
		t.Fatalf("記録の形が違う:\n%s", out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	// 1 行目: ヘッダー、2 行目: snapshot、3 行目: "events":[、最後: ]}
	if len(lines) != len(events)+4 {
		t.Fatalf("lines = %d, want %d", len(lines), len(events)+4)
	}
	var head RecordHeader
	if err := json.Unmarshal([]byte(strings.TrimSuffix(lines[0], ",")+"}"), &head); err != nil {
		t.Fatalf("header: %v: %s", err, lines[0])
	}
	want := RecordHeader{
		Format: RecordFormat, Kind: RecordKind, Build: "test", StageID: st.ID, StageHash: st.Hash, Seed: 1,
		DurationMs: st.DurationMs, EventCount: len(events), TraceHash: traceHash,
	}
	if head != want {
		t.Errorf("header = %+v, want %+v", head, want)
	}
	if !strings.HasPrefix(lines[0], `{"format":1,"kind":"simcli.record","build":"test",`) {
		t.Errorf("header line = %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], `"snapshot":`) || lines[2] != `"events":[` || lines[len(lines)-1] != "]}" {
		t.Errorf("lines[1:3] = %q, last = %q", lines[1:3], lines[len(lines)-1])
	}
	h := sha256.New()
	for _, l := range lines[3 : len(lines)-1] {
		h.Write([]byte(strings.TrimSuffix(l, ",")))
		h.Write([]byte{'\n'})
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != head.TraceHash {
		t.Errorf("hash = %s, traceHash = %s", got, head.TraceHash)
	}
}

// イベントが 0 件なら "events":[ の次の行が ]}。
func TestEncodeRecordNoEvents(t *testing.T) {
	t.Parallel()
	st, snap, _, _ := recordTestRun(t)
	out, err := EncodeRecord(NewRecordHeader("b", st, 2, "steady", 0, "sha256:x"), snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || !bytes.HasSuffix(out, []byte(",\n\"events\":[\n]}\n")) {
		t.Errorf("記録の形が違う:\n%s", out)
	}
}
