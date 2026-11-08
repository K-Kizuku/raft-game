package game

import (
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

func TestTrackStatus(t *testing.T) { // G-2
	t.Parallel()
	ok := &Result{Status: ledger.StatusOK}
	rejected := &Result{Status: ledger.StatusRejected, Reason: ledger.ReasonAlreadyReserved}
	tests := []struct {
		name  string
		track Track
		want  Status
	}{
		// 規則の表の各行
		{"result ok", Track{Result: ok}, StatusSucceeded},
		{"result rejected", Track{Result: rejected}, StatusRejected},
		{"committed", Track{Committed: true}, StatusCommitted},
		{"proposed", Track{Outcome: OutcomeProposed}, StatusProposed},
		{"letter on ferry", Track{Outcome: OutcomeLetter, OnFerry: true}, StatusInTransit},
		{"letter in port", Track{Outcome: OutcomeLetter}, StatusWaiting},
		{"waiting", Track{Outcome: OutcomeWaiting}, StatusWaiting},
		{"none", Track{}, StatusReceived},
		// 優先の組み合わせ
		{"committed and letter", Track{Outcome: OutcomeLetter, OnFerry: true, Committed: true}, StatusCommitted},
		{"committed and proposed", Track{Outcome: OutcomeProposed, Committed: true}, StatusCommitted},
		{"ok and committed", Track{Outcome: OutcomeProposed, Committed: true, Result: ok}, StatusSucceeded},
		{"rejected and committed", Track{Outcome: OutcomeLetter, Committed: true, Result: rejected}, StatusRejected},
		{"rejected and waiting", Track{Outcome: OutcomeWaiting, Result: rejected}, StatusRejected},
		{"on ferry but waiting", Track{Outcome: OutcomeWaiting, OnFerry: true}, StatusWaiting},
	}
	for _, tt := range tests {
		if got := tt.track.Status(); got != tt.want {
			t.Errorf("%s: Status() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRequestCommand(t *testing.T) { // G-3
	t.Parallel()
	r := Request{ID: "t-0001", Origin: 2, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 1, Deadline: 2}
	b, err := ledger.Encode(r.Command())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"reqId":"t-0001","origin":2,"kind":"stall.reserve","resource":"stall:main","amount":0}`
	if string(b) != want {
		t.Errorf("Encode(Command()) = %s, want %s", b, want)
	}
}

func TestReqID(t *testing.T) { // G-4
	t.Parallel()
	tests := []struct {
		stage string
		n     int
		want  string
	}{
		{"stage2", 7, "stage2-0007"},
		{"t", 1, "t-0001"},
		{"t", 9999, "t-9999"},
		{"t", 12345, "t-12345"},
	}
	for _, tt := range tests {
		if got := ReqID(tt.stage, tt.n); got != tt.want {
			t.Errorf("ReqID(%q, %d) = %q, want %q", tt.stage, tt.n, got, tt.want)
		}
	}
}
