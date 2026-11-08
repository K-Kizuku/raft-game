package bpv7

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"google.golang.org/protobuf/proto"
)

func TestLetterCodecRoundTrip(t *testing.T) {
	c := LetterCodec{LifetimeMs: testLifetime}
	tests := []struct {
		name    string
		l       ferry.Letter
		command []byte
		age     int64
	}{
		{"heartbeat", ferry.Letter{ID: 1_000_000_001, Kind: ferry.LetterRaft, From: 1, To: 2, Raft: heartbeatMsg, CreatedAt: 99, Location: ferry.Location{Kind: ferry.LocPort, Island: 1}}, nil, 1500},
		{"msgapp", ferry.Letter{ID: 3_999_999_999, Kind: ferry.LetterRaft, From: 3, To: 1, Raft: appMsg}, nil, 0},
		{"request", ferry.Letter{ID: 2_000_000_007, Kind: ferry.LetterRequest, From: 2, To: 3, Req: &ferry.RequestNote{ReqID: "r-1", Hops: 1}}, testCommand, 1<<32 + 1},
		{"request max age", ferry.Letter{ID: 6_000_000_001, Kind: ferry.LetterRequest, From: 6, To: 1, Req: &ferry.RequestNote{ReqID: "x"}}, []byte("{}"), math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := c.Encode(tt.l, tt.command, tt.age)
			if err != nil {
				t.Fatal(err)
			}
			l, cmd, age, err := c.Decode(b)
			if err != nil {
				t.Fatal(err)
			}
			if l.ID != tt.l.ID || l.Kind != tt.l.Kind || l.From != tt.l.From || l.To != tt.l.To {
				t.Fatalf("header: got %+v, want %+v", l, tt.l)
			}
			if l.CreatedAt != 0 || l.Location != (ferry.Location{}) {
				t.Fatalf("CreatedAt/Location must be zero: %+v", l)
			}
			if age != tt.age {
				t.Fatalf("age = %d, want %d", age, tt.age)
			}
			switch tt.l.Kind {
			case ferry.LetterRaft:
				if l.Req != nil || cmd != nil || !proto.Equal(l.Raft, tt.l.Raft) {
					t.Fatalf("raft: got %v / %v / %q", l.Raft, l.Req, cmd)
				}
			case ferry.LetterRequest:
				if l.Raft != nil || *l.Req != *tt.l.Req || !bytes.Equal(cmd, tt.command) {
					t.Fatalf("request: got %+v / %q", l.Req, cmd)
				}
			}
			// WithAge は Age だけを変える。
			b2, err := c.WithAge(b, tt.age/2+7)
			if err != nil {
				t.Fatal(err)
			}
			_, _, age2, err := c.Decode(b2)
			if err != nil || age2 != tt.age/2+7 {
				t.Fatalf("WithAge: age %d, %v", age2, err)
			}
		})
	}
}

// TestLetterCodecFixtures は、Codec が fixture と同じ bytes を作ることを確かめる。
func TestLetterCodecFixtures(t *testing.T) {
	c := LetterCodec{LifetimeMs: testLifetime}
	for name, in := range map[string]struct {
		l   ferry.Letter
		cmd []byte
		age int64
	}{
		"heartbeat": {ferry.Letter{ID: 1_000_000_001, Kind: ferry.LetterRaft, From: 1, To: 2, Raft: heartbeatMsg}, nil, 1500},
		"msgapp":    {ferry.Letter{ID: 3_999_999_999, Kind: ferry.LetterRaft, From: 3, To: 1, Raft: appMsg}, nil, 0},
		"request":   {ferry.Letter{ID: 2_000_000_007, Kind: ferry.LetterRequest, From: 2, To: 3, Req: &ferry.RequestNote{ReqID: "r-1", Hops: 1}}, testCommand, 1<<32 + 1},
	} {
		got, err := c.Encode(in.l, in.cmd, in.age)
		if err != nil {
			t.Fatal(err)
		}
		if want := readHex(t, filepath.Join("testdata", name+".hex")); !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %x\nwant %x", name, got, want)
		}
	}
}

func TestLetterCodecErrors(t *testing.T) {
	c := LetterCodec{LifetimeMs: testLifetime}
	raft := ferry.Letter{ID: 1_000_000_001, Kind: ferry.LetterRaft, From: 1, To: 2, Raft: heartbeatMsg}
	req := ferry.Letter{ID: 2_000_000_001, Kind: ferry.LetterRequest, From: 2, To: 3, Req: &ferry.RequestNote{ReqID: "r"}}
	mod := func(l ferry.Letter, f func(*ferry.Letter)) ferry.Letter { f(&l); return l }
	tests := []struct {
		name string
		l    ferry.Letter
		cmd  []byte
		age  int64
		want Code
	}{
		{"negative age", raft, nil, -1, CodeAge},
		{"id island differs from From", mod(raft, func(l *ferry.Letter) { l.From = 3 }), nil, 0, CodeEID},
		{"id seq 0", mod(raft, func(l *ferry.Letter) { l.ID = 1_000_000_000 }), nil, 0, CodeTimestamp},
		{"to ferry", mod(raft, func(l *ferry.Letter) { l.To = 1001 }), nil, 0, CodeEID},
		{"to 0", mod(raft, func(l *ferry.Letter) { l.To = 0 }), nil, 0, CodeEID},
		{"raft nil", mod(raft, func(l *ferry.Letter) { l.Raft = nil }), nil, 0, CodeMalformed},
		{"raft with command", raft, []byte("{}"), 0, CodeMalformed},
		{"request nil", mod(req, func(l *ferry.Letter) { l.Req = nil }), nil, 0, CodeMalformed},
		{"request negative hops", mod(req, func(l *ferry.Letter) { l.Req = &ferry.RequestNote{ReqID: "r", Hops: -1} }), nil, 0, CodeMalformed},
		{"unknown kind", mod(raft, func(l *ferry.Letter) { l.Kind = 9 }), nil, 0, CodeMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Encode(tt.l, tt.cmd, tt.age); codeOf(err) != tt.want {
				t.Fatalf("Encode = %v, want %s", err, tt.want)
			}
		})
	}
	good, err := c.Encode(raft, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WithAge(good, -1); codeOf(err) != CodeAge {
		t.Fatalf("WithAge(-1) = %v", err)
	}
	// Age が int64 に収まらない Bundle は Codec では断る。
	b, _ := Decode(good)
	b.AgeMs = math.MaxInt64 + 1
	big, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.Decode(big); codeOf(err) != CodeAge {
		t.Fatalf("Decode(age > MaxInt64) = %v", err)
	}
	// ペイロードが読めない Bundle。
	for _, tc := range []struct {
		svc     uint64
		payload []byte
	}{{ServiceRaft, []byte{0x08}}, {ServiceRequest, []byte{0xa0}}} {
		b := validBundle()
		b.Source, b.Dest = IPN(1, tc.svc), IPN(2, tc.svc)
		b.Payload = tc.payload
		enc, err := Encode(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := c.Decode(enc); codeOf(err) != CodeMalformed {
			t.Fatalf("Decode(bad payload, service %d) = %v", tc.svc, err)
		}
	}
	if _, _, _, err := c.Decode(nil); codeOf(err) != CodeMalformed {
		t.Fatalf("Decode(nil) = %v", err)
	}
}
