package bpv7

import "testing"

func TestLetterIDRoundTrip(t *testing.T) {
	for _, tc := range []struct{ node, seq, id uint64 }{
		{1, 1, 1_000_000_001},
		{3, 999_999_999, 3_999_999_999},
		{6, 42, 6_000_000_042},
		{999, 999_999_999, 999_999_999_999},
	} {
		for _, svc := range []uint64{ServiceRaft, ServiceRequest} {
			id, err := LetterID(IPN(tc.node, svc), tc.seq)
			if err != nil || id != tc.id {
				t.Fatalf("LetterID(%d.%d, %d) = %d, %v; want %d", tc.node, svc, tc.seq, id, err, tc.id)
			}
			bid, err := BundleIDOf(id, svc)
			if err != nil || bid.Source != IPN(tc.node, svc) || bid.Seq != tc.seq {
				t.Fatalf("BundleIDOf(%d) = %+v, %v", id, bid, err)
			}
			back, err := bid.LetterID()
			if err != nil || back != id {
				t.Fatalf("BundleID.LetterID = %d, %v", back, err)
			}
		}
		node, seq, err := SplitLetterID(tc.id)
		if err != nil || node != tc.node || seq != tc.seq {
			t.Fatalf("SplitLetterID(%d) = %d, %d, %v", tc.id, node, seq, err)
		}
	}
	bad := []struct {
		name string
		src  EID
		seq  uint64
		want Code
	}{
		{"seq 0", IslandRaftEID(1), 0, CodeTimestamp},
		{"seq 1e9", IslandRaftEID(1), 1_000_000_000, CodeTimestamp},
		{"ferry", FerryEID(1), 1, CodeEID},
		{"island 0", IslandRaftEID(0), 1, CodeEID},
		{"island 1000", IslandRaftEID(1000), 1, CodeEID},
		{"dtn:none", DTNNone, 1, CodeEID},
	}
	for _, tc := range bad {
		if _, err := LetterID(tc.src, tc.seq); codeOf(err) != tc.want {
			t.Errorf("LetterID %s = %v, want %s", tc.name, err, tc.want)
		}
	}
	for _, tc := range []struct {
		id   uint64
		want Code
	}{
		{0, CodeEID},
		{999_999_999, CodeEID},         // 島 0
		{1_000_000_000, CodeTimestamp}, // 連番 0
		{1_000_000_000_000, CodeEID},   // 島 1000
		{^uint64(0), CodeEID},
	} {
		if _, _, err := SplitLetterID(tc.id); codeOf(err) != tc.want {
			t.Errorf("SplitLetterID(%d) = %v, want %s", tc.id, err, tc.want)
		}
	}
	if _, err := BundleIDOf(1_000_000_001, ServiceFerry); codeOf(err) != CodeEID {
		t.Errorf("BundleIDOf(service 0) = %v", err)
	}
}

func TestEIDs(t *testing.T) {
	for _, tc := range []struct {
		e    EID
		want string
	}{
		{IslandRaftEID(3), "ipn:3.1"},
		{IslandRequestEID(3), "ipn:3.2"},
		{FerryEID(1), "ipn:1001.0"},
		{FerryEID(12), "ipn:1012.0"},
		{DTNNone, "dtn:none"},
		{EID{Scheme: 9, Node: 1, Service: 2}, "eid(9,1,2)"},
	} {
		if got := tc.e.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}
