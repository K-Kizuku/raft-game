package ferry

import (
	"fmt"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

func TestNewValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mod  func(c *Config)
	}{
		{"no islands", func(c *Config) { c.Islands = nil }},
		{"island 0", func(c *Config) { c.Islands = []uint64{0, 1, 2, 3} }},
		{"duplicate island", func(c *Config) { c.Islands = []uint64{1, 2, 3, 2} }},
		{"route unknown island", func(c *Config) { c.Routes = append(c.Routes, RouteConfig{A: 1, B: 9, TravelMs: 1}) }},
		{"route same island", func(c *Config) { c.Routes = append(c.Routes, RouteConfig{A: 1, B: 1, TravelMs: 1}) }},
		{"duplicate route", func(c *Config) { c.Routes = append(c.Routes, RouteConfig{A: 2, B: 1, TravelMs: 1}) }},
		{"travel 0", func(c *Config) { c.Routes[0].TravelMs = 0 }},
		{"empty ferry id", func(c *Config) { c.Ferries[0].ID = "" }},
		{"duplicate ferry", func(c *Config) { c.Ferries[1].ID = "f1" }},
		{"ferry home unknown", func(c *Config) { c.Ferries[0].Home = 9 }},
		{"capacity 0", func(c *Config) { c.Ferries[0].Capacity = 0 }},
		{"speed 0", func(c *Config) { c.Ferries[0].SpeedPermille = 0 }},
		{"dockMs negative", func(c *Config) { c.DockMs = -1 }},
		{"portCap 0", func(c *Config) { c.PortCap = 0 }},
		{"perLinkAppCap 0", func(c *Config) { c.PerLinkAppCap = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			tt.mod(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		cfg := baseConfig()
		cfg.DockMs = 0
		cfg.Routes = nil // 連結性は見ない
		tr, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		mustInv(t, tr)
		if p := tr.Port(1); p == nil || len(p) != 0 {
			t.Fatalf("Port(1) = %v", p)
		}
		if p := tr.Port(9); p != nil {
			t.Fatalf("Port(9) = %v", p)
		}
		if _, ok := tr.Letter(1); ok {
			t.Fatal("letter 1 exists")
		}
	})
}

func TestReadersCopy(t *testing.T) {
	t.Parallel()
	tr := newT(t, baseConfig())
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1))
	if err := tr.Load("f1", c[:1]); err != nil {
		t.Fatal(err)
	}
	p := tr.Port(1)
	p[0] = 999
	f, _ := tr.Ferry("f1")
	f.Cargo[0] = 999
	fs := tr.Ferries()
	fs[0].Cargo[0] = 999
	rs := tr.Routes()
	rs[0].BlockedUntil = 999
	mustInv(t, tr)
	if b, _ := tr.IsBlocked(1, 2, 0); b {
		t.Fatal("Routes did not copy")
	}
}

// B-3: 非公開の状態を壊すと CheckInvariants が違反を見つける。
func TestCheckInvariantsDetects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		break_ func(tr *Transport, c []LetterID)
	}{
		{"portCap exceeded", func(tr *Transport, _ []LetterID) { tr.portCap = 2 }},
		{"perLinkAppCap exceeded", func(tr *Transport, _ []LetterID) { tr.perLinkAppCap = 1 }},
		{"two heartbeats", func(tr *Transport, _ []LetterID) {
			id := tr.newLetter(Letter{Kind: LetterRaft, From: 1, To: 2, Raft: msg(pb.MsgHeartbeat, 1, 2, 1),
				Location: Location{Kind: LocPort, Island: 1}})
			tr.ports[1] = append(tr.ports[1], id)
		}},
		{"two app resps", func(tr *Transport, _ []LetterID) {
			for range 2 {
				id := tr.newLetter(Letter{Kind: LetterRaft, From: 1, To: 2, Raft: msg(pb.MsgAppResp, 1, 2, 1),
					Location: Location{Kind: LocPort, Island: 1}})
				tr.ports[1] = append(tr.ports[1], id)
			}
		}},
		{"two requests", func(tr *Transport, _ []LetterID) {
			for range 2 {
				id := tr.newLetter(Letter{Kind: LetterRequest, From: 1, To: 2, Req: &RequestNote{ReqID: "x"},
					Location: Location{Kind: LocPort, Island: 1}})
				tr.ports[1] = append(tr.ports[1], id)
			}
		}},
		{"stale vote kept", func(tr *Transport, _ []LetterID) {
			for range 2 {
				id := tr.newLetter(Letter{Kind: LetterRaft, From: 1, To: 2, Raft: msg(pb.MsgVote, 1, 2, 3),
					Location: Location{Kind: LocPort, Island: 1}})
				tr.ports[1] = append(tr.ports[1], id)
			}
		}},
		{"cargo over capacity", func(tr *Transport, _ []LetterID) { tr.ferries[0].Capacity = 1 }},
		{"location mismatch", func(tr *Transport, c []LetterID) {
			tr.letter(c[0]).Location = Location{Kind: LocPort, Island: 2}
		}},
		{"port letter not listed", func(tr *Transport, c []LetterID) {
			tr.removeFromPort(1, c[0])
		}},
		{"dropped but listed", func(tr *Transport, c []LetterID) {
			tr.letter(c[0]).Location = Location{Kind: LocDropped}
		}},
		{"listed twice", func(tr *Transport, c []LetterID) {
			tr.ferries[0].Cargo = append(tr.ferries[0].Cargo, c[0])
		}},
		{"port order", func(tr *Transport, _ []LetterID) {
			p := tr.ports[1]
			p[0], p[1] = p[1], p[0]
		}},
		{"from mismatch", func(tr *Transport, c []LetterID) { tr.letter(c[0]).From = 3 }},
		{"docked with from", func(tr *Transport, _ []LetterID) { tr.ferries[1].From = 2 }},
		{"sailing without route", func(tr *Transport, _ []LetterID) {
			f := &tr.ferries[1]
			f.State, f.At, f.From, f.To = Sailing, 0, 3, 1
		}},
		{"letters order", func(tr *Transport, _ []LetterID) {
			tr.letters[0], tr.letters[1] = tr.letters[1], tr.letters[0]
		}},
		{"id above lastID", func(tr *Transport, _ []LetterID) { tr.lastID-- }},
		{"reclaimed but listed", func(tr *Transport, c []LetterID) {
			// 1→2 の MsgApp を足して 1 通目を R4 で破棄させ、Reclaim したあと、その ID を港の一覧に戻す。
			if _, d, err := tr.PostRaft(0, 1, []*pb.Message{msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1)}); err != nil || len(d) != 1 || d[0].Letter != c[0] {
				panic(fmt.Sprintf("setup: drops %+v, err %v", d, err))
			}
			if err := tr.CheckInvariants(); err != nil || tr.Reclaim() != 1 {
				panic(fmt.Sprintf("setup: %v", err))
			}
			tr.ports[1] = append([]LetterID{c[0]}, tr.ports[1]...)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, baseConfig())
			c, _ := post(t, tr, 0, 1,
				msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 2, 1), msg(pb.MsgApp, 1, 3, 1),
				msg(pb.MsgHeartbeat, 1, 2, 1),
			)
			l, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 3, 1))
			if err := tr.Load("f1", l); err != nil {
				t.Fatal(err)
			}
			mustInv(t, tr)
			tt.break_(tr, c)
			if err := tr.CheckInvariants(); err == nil {
				t.Fatal("violation not detected")
			}
		})
	}
}
