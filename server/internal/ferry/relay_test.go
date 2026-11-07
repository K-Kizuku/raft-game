package ferry

import (
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

// relayConfig は 1-2-3 の一直線で、f1（容量 4）が島 1、f2（容量 2）が島 2 にいる設定。
func relayConfig(relayCap int) Config {
	cfg := baseConfig()
	cfg.Ferries[1].Home = 2
	cfg.RelayCap = relayCap
	return cfg
}

// carry は島 1 の港の手紙を f1 に積み、島 2 へ運んで着かせる。
func carry(t *testing.T, tr *Transport, ids []LetterID) {
	t.Helper()
	if err := tr.Load("f1", ids); err != nil {
		t.Fatalf("Load: %v", err)
	}
	f, _ := tr.Ferry("f1")
	at, err := tr.Depart(f.ReadyAt, "f1", 2)
	if err != nil {
		t.Fatalf("Depart: %v", err)
	}
	if d, err := tr.Arrive(at, "f1"); err != nil || len(d) != 0 {
		t.Fatalf("Arrive: %v %v", d, err)
	}
	mustInv(t, tr)
}

type relayState struct {
	port1, port2, relay2, cargo1, cargo2 []LetterID
}

func snapRelay(tr *Transport) relayState {
	f1, _ := tr.Ferry("f1")
	f2, _ := tr.Ferry("f2")
	return relayState{tr.Port(1), tr.Port(2), tr.Relay(2), f1.Cargo, f2.Cargo}
}

func sameRelayState(t *testing.T, got, want relayState) {
	t.Helper()
	for _, p := range []struct {
		name string
		g, w []LetterID
	}{
		{"port 1", got.port1, want.port1}, {"port 2", got.port2, want.port2}, {"relay 2", got.relay2, want.relay2},
		{"cargo f1", got.cargo1, want.cargo1}, {"cargo f2", got.cargo2, want.cargo2},
	} {
		if !slices.Equal(p.g, p.w) {
			t.Fatalf("%s = %v, want %v", p.name, p.g, p.w)
		}
	}
}

// FR-1: 経由の手紙を降ろすと、その島の中継倉庫に入る。港は変わらず、破棄は起きない。
func TestRelayUnloadStores(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1))
	carry(t, tr, c)
	port2 := tr.Port(2)
	d, err := tr.Unload("f1", c)
	if err != nil || len(d) != 0 {
		t.Fatalf("Unload: %v %v", d, err)
	}
	wantIDs(t, "relay 2", tr.Relay(2), c)
	wantIDs(t, "port 2", tr.Port(2), port2)
	if got := loc(t, tr, c[0]); got != (Location{Kind: LocRelay, Island: 2}) {
		t.Fatalf("location = %+v", got)
	}
	f, _ := tr.Ferry("f1")
	wantIDs(t, "cargo", f.Cargo, nil)
	mustInv(t, tr)
}

// FR-2: 別の船が倉庫の手紙を積み荷の末尾に積み、宛先の島に着くと配達される。
func TestRelayLoadAndDeliver(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1))
	carry(t, tr, c)
	if _, err := tr.Unload("f1", c); err != nil {
		t.Fatal(err)
	}
	p, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 3, 1))
	if err := tr.Load("f2", p); err != nil {
		t.Fatal(err)
	}
	if err := tr.RelayLoad("f2", c); err != nil {
		t.Fatalf("RelayLoad: %v", err)
	}
	f, _ := tr.Ferry("f2")
	wantIDs(t, "cargo", f.Cargo, []LetterID{p[0], c[0]})
	if r := tr.Relay(2); len(r) != 0 {
		t.Fatalf("relay 2 = %v, want empty", r)
	}
	if got := loc(t, tr, c[0]); got != (Location{Kind: LocFerry, Ferry: "f2"}) {
		t.Fatalf("location = %+v", got)
	}
	mustInv(t, tr)
	at, err := tr.Depart(f.ReadyAt, "f2", 3)
	if err != nil {
		t.Fatal(err)
	}
	d, err := tr.Arrive(at, "f2")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "delivered", d, []LetterID{p[0], c[0]})
	if got := loc(t, tr, c[0]); got != (Location{Kind: LocDelivered, Island: 3}) {
		t.Fatalf("location = %+v", got)
	}
	mustInv(t, tr)
}

// 預けた船がそのまま積み直せる。
func TestRelayLoadSameFerry(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 3, 1))
	carry(t, tr, c)
	if _, err := tr.Unload("f1", c); err != nil {
		t.Fatal(err)
	}
	if err := tr.RelayLoad("f1", []LetterID{c[1], c[0]}); err != nil {
		t.Fatal(err)
	}
	f, _ := tr.Ferry("f1")
	wantIDs(t, "cargo", f.Cargo, []LetterID{c[1], c[0]})
	mustInv(t, tr)
}

// FR-3: 1 つの Unload に送信元の手紙と経由の手紙が混ざる。前者は港（R1 で破棄）、後者は倉庫。
func TestRelayUnloadMixed(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1))
	carry(t, tr, c)
	old, _ := post1(t, tr, msg(pb.MsgHeartbeat, 2, 1, 1))
	if err := tr.Load("f1", []LetterID{old}); err != nil {
		t.Fatal(err)
	}
	newer, _ := post1(t, tr, msg(pb.MsgHeartbeat, 2, 1, 1))
	d, err := tr.Unload("f1", []LetterID{old, c[0]})
	if err != nil {
		t.Fatal(err)
	}
	wantDrops(t, d, []Drop{{Letter: old, Reason: DropSuperseded, Rule: "R1"}})
	wantIDs(t, "port 2", tr.Port(2), []LetterID{newer})
	wantIDs(t, "relay 2", tr.Relay(2), c)
	mustInv(t, tr)
}

// FR-4: 倉庫の上限を超えるなら 1 通も降ろさない（港へ戻す手紙が混ざっていても）。
func TestRelayFull(t *testing.T) {
	t.Parallel()

	t.Run("cap 2", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, relayConfig(2))
		c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 3, 1))
		carry(t, tr, c)
		if _, err := tr.Unload("f1", c[:1]); err != nil {
			t.Fatal(err)
		}
		own, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 1, 1))
		if err := tr.Load("f1", own); err != nil {
			t.Fatal(err)
		}
		before := snapRelay(tr)
		_, err := tr.Unload("f1", []LetterID{own[0], c[1], c[2]})
		wantErr(t, err, ErrRelayFull)
		sameRelayState(t, snapRelay(tr), before)
		mustInv(t, tr)
		// 1 通なら預けられる（上限ちょうど）。
		if _, err := tr.Unload("f1", []LetterID{c[1]}); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "relay 2", tr.Relay(2), c[:2])
		mustInv(t, tr)
	})

	t.Run("cap 0", func(t *testing.T) {
		t.Parallel()
		tr := newT(t, relayConfig(0))
		c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1))
		carry(t, tr, c)
		before := snapRelay(tr)
		_, err := tr.Unload("f1", c)
		wantErr(t, err, ErrRelayFull)
		sameRelayState(t, snapRelay(tr), before)
		// 送信元の手紙だけなら今までどおり港へ戻る。
		own, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 1, 1))
		if err := tr.Load("f1", own); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Unload("f1", own); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "port 2", tr.Port(2), own)
		mustInv(t, tr)
	})
}

// FR-5: RelayLoad の誤りでは何も変えない。
func TestRelayLoadErrors(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 3, 1), msg(pb.MsgApp, 1, 2, 1))
	carry(t, tr, c[:3])
	if _, err := tr.Unload("f1", c[:3]); err != nil {
		t.Fatal(err)
	}
	inPort1 := c[3]
	own, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 1, 1))
	cargo, _ := post(t, tr, 0, 2, msg(pb.MsgApp, 2, 3, 1))
	if err := tr.Load("f2", cargo); err != nil {
		t.Fatal(err)
	}
	before := snapRelay(tr)
	check := func(err, target error) {
		t.Helper()
		wantErr(t, err, target)
		sameRelayState(t, snapRelay(tr), before)
		mustInv(t, tr)
	}
	check(tr.RelayLoad("f1", []LetterID{own[0]}), ErrLetterNotHere)
	check(tr.RelayLoad("f1", []LetterID{inPort1}), ErrLetterNotHere)
	check(tr.RelayLoad("f1", []LetterID{cargo[0]}), ErrLetterNotHere)
	check(tr.RelayLoad("f1", []LetterID{999}), ErrLetterNotHere)
	check(tr.RelayLoad("f1", []LetterID{0}), ErrLetterNotHere)
	check(tr.RelayLoad("f1", []LetterID{c[0], c[0]}), ErrInvalidArgument)
	check(tr.RelayLoad("f1", nil), ErrInvalidArgument)
	check(tr.RelayLoad("zz", c[:1]), ErrUnknownFerry)
	check(tr.RelayLoad("f2", c[:2]), ErrCapacityExceeded)

	// 航行中の船。
	f, _ := tr.Ferry("f1")
	at, err := tr.Depart(f.ReadyAt, "f1", 3)
	if err != nil {
		t.Fatal(err)
	}
	wantErr(t, tr.RelayLoad("f1", c[:1]), ErrNotDocked)
	// 別の島（3）の船から島 2 の倉庫の手紙は積めない。
	if _, err := tr.Arrive(at, "f1"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, tr.RelayLoad("f1", c[:1]), ErrLetterNotHere)
	wantIDs(t, "relay 2", tr.Relay(2), c[:3])
	mustInv(t, tr)
}

// FR-6: 倉庫では合流しない（同じ (種類, 宛先) の Heartbeat が 2 通とも残る）。
func TestRelayNoMerge(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	a, _ := post1(t, tr, msg(pb.MsgHeartbeat, 1, 3, 1))
	if err := tr.Load("f1", []LetterID{a}); err != nil {
		t.Fatal(err)
	}
	b, _ := post1(t, tr, msg(pb.MsgHeartbeat, 1, 3, 1))
	carry(t, tr, []LetterID{b})
	d, err := tr.Unload("f1", []LetterID{b, a})
	if err != nil || len(d) != 0 {
		t.Fatalf("Unload: %v %v", d, err)
	}
	wantIDs(t, "relay 2", tr.Relay(2), []LetterID{a, b})
	mustInv(t, tr)
}

// FR-7: 非公開の状態を壊すと CheckInvariants が倉庫の違反を見つける。
func TestRelayCheckInvariantsDetects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		break_ func(tr *Transport, c []LetterID)
	}{
		{"relayCap exceeded", func(tr *Transport, _ []LetterID) { tr.relayCap = 1 }},
		{"location mismatch", func(tr *Transport, c []LetterID) {
			tr.letter(c[0]).Location = Location{Kind: LocRelay, Island: 3}
		}},
		{"to the island", func(tr *Transport, c []LetterID) { tr.letter(c[0]).To = 2 }},
		{"from the island", func(tr *Transport, c []LetterID) { tr.letter(c[0]).From = 2 }},
		{"listed twice", func(tr *Transport, c []LetterID) {
			tr.ferries[1].Cargo = append(tr.ferries[1].Cargo, c[0])
		}},
		{"in port and relay", func(tr *Transport, c []LetterID) {
			tr.ports[1] = append([]LetterID{c[0]}, tr.ports[1]...)
		}},
		{"relay order", func(tr *Transport, _ []LetterID) {
			r := tr.relays[2]
			r[0], r[1] = r[1], r[0]
		}},
		{"relay letter not listed", func(tr *Transport, _ []LetterID) {
			tr.relays[2] = tr.relays[2][1:]
		}},
		{"unknown letter", func(tr *Transport, _ []LetterID) {
			tr.relays[2] = append(tr.relays[2], 999)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newT(t, relayConfig(12))
			c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgVote, 1, 3, 1))
			carry(t, tr, c)
			if _, err := tr.Unload("f1", c); err != nil {
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

// FR-8: Reclaim は倉庫の手紙を手放さない。New で RelayCap < 0 は誤り。
func TestRelayReclaimAndConfig(t *testing.T) {
	t.Parallel()
	tr := newT(t, relayConfig(12))
	c, _ := post(t, tr, 0, 1, msg(pb.MsgApp, 1, 3, 1), msg(pb.MsgApp, 1, 2, 1))
	if err := tr.Load("f1", c); err != nil {
		t.Fatal(err)
	}
	at, _ := tr.Depart(0, "f1", 2)
	if d, err := tr.Arrive(at, "f1"); err != nil || !slices.Equal(d, c[1:]) { // c[1]（1→2）は着いた時に配達される
		t.Fatalf("Arrive: %v %v", d, err)
	}
	if _, err := tr.Unload("f1", c[:1]); err != nil {
		t.Fatal(err)
	}
	if n := tr.Reclaim(); n != 1 {
		t.Fatalf("Reclaim = %d, want 1", n)
	}
	wantIDs(t, "relay 2", tr.Relay(2), c[:1])
	if _, ok := tr.Letter(c[0]); !ok {
		t.Fatal("relay letter reclaimed")
	}
	mustInv(t, tr)

	if r := tr.Relay(9); r != nil {
		t.Fatalf("Relay(9) = %v, want nil", r)
	}
	if r := tr.Relay(3); r == nil || len(r) != 0 {
		t.Fatalf("Relay(3) = %#v, want empty", r)
	}
	if _, err := New(relayConfig(-1)); err == nil {
		t.Fatal("New with relayCap -1: no error")
	}
}
