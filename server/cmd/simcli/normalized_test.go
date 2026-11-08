package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/simtrace"
)

// readNormalized は --normalized-trace の出力を読む。
func readNormalized(t *testing.T, path string) simtrace.Trace {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var tr simtrace.Trace
	if err := dec.Decode(&tr); err != nil {
		t.Fatalf("正規化トレースを読めない: %v", err)
	}
	return tr
}

// NT-1: record と、その入力ログの replay の --normalized-trace は Compare で一致する。round.result も入る。
func TestNormalizedTraceRecordAndReplay(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	recPath := filepath.Join(tmp, "rec.json")
	inDir := filepath.Join(tmp, "in")
	r, rec := recordOK(t, "--inputs-dir", inDir, "--normalized-trace", recPath)
	if !strings.Contains(r.stderr, "simcli record: 正規化トレースを "+recPath+" に書いた") {
		t.Errorf("stderr = %s", r.stderr)
	}
	ref := readNormalized(t, recPath)
	if len(ref.Streams) == 0 || len(ref.Final.Result) == 0 || string(ref.Final.Result) == "null" {
		t.Fatalf("正規化トレースが空: streams=%d result=%s", len(ref.Streams), ref.Final.Result)
	}
	n := 0
	for _, s := range ref.Streams {
		n += len(s.Events)
	}
	for _, g := range ref.Global {
		n += len(g.Events)
	}
	if n == 0 || n > rec.EventCount {
		t.Errorf("列と global のイベントの数 %d（記録は %d 件）", n, rec.EventCount)
	}

	repPath := filepath.Join(tmp, "rep.json")
	rp := replayFile(t, inputLogPath(inDir, testStage, "greedy", 1), "--normalized-trace", repPath)
	rp.wantCode(t, exitOK)
	if d := simtrace.Compare(ref, readNormalized(t, repPath)); d != nil {
		t.Fatalf("record と replay の正規化トレースが食い違う: %s", d)
	}
}

// NT-2: --normalized-trace に - は使えない。失敗した replay では書かない。
func TestNormalizedTraceUsage(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil, recordArgs("--normalized-trace", "-")...)
	r.wantCode(t, exitUsage)
	if r.stdout != "" || !strings.Contains(r.stderr, "--normalized-trace に - は使えない") {
		t.Errorf("record: stdout = %q, stderr = %s", r.stdout, r.stderr)
	}
	r = runCLI(t, nil, "replay", "--data", testData, "--input", "x", "--normalized-trace", "-")
	r.wantCode(t, exitUsage)

	path := filepath.Join(t.TempDir(), "n.json")
	r = replayFile(t, filepath.Join(t.TempDir(), "missing.json"), "--normalized-trace", path)
	r.wantCode(t, exitFail)
	wantNoFile(t, path)
}
