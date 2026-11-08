package bpv7

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func u64(v uint64) *uint64 { return &v }

// readHex は testdata の 16 進のファイルを読む。"#" で始まる行と空白は無視する。
func readHex(t testing.TB, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		sb.WriteString(strings.Join(strings.Fields(line), ""))
	}
	b, err := hex.DecodeString(sb.String())
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return b
}

// globSorted は、ファイル名の順に並べた一覧を返す。
func globSorted(t testing.TB, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

const testLifetime = 660_000 // durationMs 600,000 + 60,000

var (
	heartbeatMsg = &pb.Message{Type: pb.MsgHeartbeat.Enum(), To: u64(2), From: u64(1), Term: u64(3), Commit: u64(5)}
	appMsg       = &pb.Message{
		Type: pb.MsgApp.Enum(), To: u64(1), From: u64(3), Term: u64(2), LogTerm: u64(2), Index: u64(4),
		Entries: []*pb.Entry{
			{Term: u64(2), Index: u64(5), Data: []byte("abc")},
			{Term: u64(2), Index: u64(6), Data: []byte("de")},
		},
		Commit: u64(4),
	}
	testCommand = []byte(`{"v":1,"reqId":"r-1","origin":2,"kind":"firework.reserve","resource":"firework:a","amount":0}`)
)

func mustRaft(t testing.TB, m *pb.Message) []byte {
	t.Helper()
	b, err := EncodeRaft(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustRequest(t testing.TB, r RequestPayload) []byte {
	t.Helper()
	b, err := EncodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixtureBundles は testdata/<name>.hex の中身を表す Bundle。
func fixtureBundles(t testing.TB) map[string]Bundle {
	return map[string]Bundle{
		"heartbeat": {
			Dest: IslandRaftEID(2), Source: IslandRaftEID(1), ReportTo: DTNNone,
			Seq: 1, LifetimeMs: testLifetime, AgeMs: 1500, Payload: mustRaft(t, heartbeatMsg),
		},
		"msgapp": {
			Dest: IslandRaftEID(1), Source: IslandRaftEID(3), ReportTo: DTNNone,
			Seq: 999_999_999, LifetimeMs: testLifetime, AgeMs: 0, Payload: mustRaft(t, appMsg),
		},
		"request": {
			Dest: IslandRequestEID(3), Source: IslandRequestEID(2), ReportTo: DTNNone,
			Seq: 7, LifetimeMs: testLifetime, AgeMs: 1<<32 + 1,
			Payload: mustRequest(t, RequestPayload{ReqID: "r-1", Hops: 1, Command: testCommand}),
		},
	}
}

func bundleEqual(a, b Bundle) bool {
	return a.Dest == b.Dest && a.Source == b.Source && a.ReportTo == b.ReportTo &&
		a.CreatedDTN == b.CreatedDTN && a.Seq == b.Seq && a.LifetimeMs == b.LifetimeMs &&
		a.AgeMs == b.AgeMs && bytes.Equal(a.Payload, b.Payload)
}
