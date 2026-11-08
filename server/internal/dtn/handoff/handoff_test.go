package handoff_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/handoff"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

const (
	l1 ferry.LetterID = 1_000_000_001
	l2 ferry.LetterID = 1_000_000_002
	l3 ferry.LetterID = 2_000_000_001
)

var (
	port1  = handoff.IslandPort(1)
	relay1 = handoff.IslandRelay(1)
	cargo  = handoff.FerryCargo("f1")
)

func digest(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func ack(id string, n int) handoff.Ack {
	a := handoff.Ack{TransferID: id}
	for i := range n {
		a.Digests = append(a.Digests, digest("0123456789abcdef"[i]))
	}
	return a
}

func load(id string, letters ...ferry.LetterID) handoff.Transfer {
	return handoff.Transfer{ID: id, From: port1, To: cargo, ContactID: "k1", Letters: letters}
}

// ledgerWith は l1・l2 を島 1 の港に、l3 を船の Cargo に置いた台帳。
func ledgerWith(t *testing.T) *handoff.Ledger {
	t.Helper()
	l := handoff.NewLedger()
	for _, id := range []ferry.LetterID{l1, l2} {
		if err := l.Create(id, port1); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Create(l3, handoff.IslandPort(2)); err != nil {
		t.Fatal(err)
	}
	mustRun(t, l, handoff.Transfer{ID: "x1", From: handoff.IslandPort(2), To: cargo, ContactID: "k0", Letters: []ferry.LetterID{l3}})
	return l
}

// mustRun は t を Collected まで進める。
func mustRun(t *testing.T, l *handoff.Ledger, tr handoff.Transfer) {
	t.Helper()
	steps := []func() error{
		func() error { return l.Propose(tr) },
		func() error { return l.Persisted(tr.ID, ack(tr.ID, len(tr.Letters))) },
		func() error { return l.Commit(tr.ID) },
		func() error { return l.Activated(tr.ID) },
		func() error { return l.Collected(tr.ID) },
	}
	for i, f := range steps {
		if err := f(); err != nil {
			t.Fatalf("%s step %d: %v", tr.ID, i, err)
		}
	}
}

func stage(t *testing.T, l *handoff.Ledger, id string) handoff.Stage {
	t.Helper()
	tr, ok := l.Transfer(id)
	if !ok {
		t.Fatalf("no transfer %s", id)
	}
	return tr.Stage
}

func TestPlace(t *testing.T) {
	for _, c := range []struct {
		p            handoff.Place
		valid        bool
		actor, slot  string
		island, boat bool
	}{
		{"island:2/port", true, "island:2", "port", true, false},
		{"island:12/relay", true, "island:12", "relay", true, false},
		{"ferry:f1/cargo", true, "ferry:f1", "cargo", false, true},
		{"delivered", true, "", "", false, false},
		{"dropped", true, "", "", false, false},
		{"island:02/port", false, "", "", false, false},
		{"island:0/port", false, "", "", false, false},
		{"island:2/cargo", false, "", "", false, false},
		{"ferry:F1/cargo", false, "", "", false, false},
		{"ferry:f1/port", false, "", "", false, false},
		{"", false, "", "", false, false},
	} {
		if got := c.p.Valid(); got != c.valid {
			t.Errorf("%q.Valid() = %v", c.p, got)
		}
		if got := c.p.Actor(); got != c.actor {
			t.Errorf("%q.Actor() = %q", c.p, got)
		}
		if got := c.p.Slot(); got != c.slot {
			t.Errorf("%q.Slot() = %q", c.p, got)
		}
		if c.p.IsIsland() != c.island || c.p.IsFerry() != c.boat {
			t.Errorf("%q: IsIsland %v IsFerry %v", c.p, c.p.IsIsland(), c.p.IsFerry())
		}
	}
	if handoff.IslandPort(3) != "island:3/port" || handoff.IslandRelay(3) != "island:3/relay" || handoff.FerryCargo("f2") != "ferry:f2/cargo" {
		t.Error("constructors")
	}
	if handoff.TransferID(1207) != "x1207" {
		t.Error("TransferID")
	}
	for _, c := range []struct {
		id string
		n  uint64
		ok bool
	}{{"x1207", 1207, true}, {"x1", 1, true}, {"x0", 0, false}, {"x01", 0, false}, {"x", 0, false}, {"k1", 0, false}, {"x-1", 0, false}} {
		n, ok := handoff.ParseTransferID(c.id)
		if n != c.n || ok != c.ok {
			t.Errorf("ParseTransferID(%q) = %d, %v", c.id, n, ok)
		}
	}
}

// TestLedgerTransitions は、許す遷移・許さない遷移・CAS の失敗を確かめる（DTN-08 のテストケース）。
func TestLedgerTransitions(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		l := ledgerWith(t)
		tr := load("x2", l1, l2)
		if err := l.Propose(tr); err != nil {
			t.Fatal(err)
		}
		if l.Owner(l1) != port1 || l.Holder(l1) != "" {
			t.Fatalf("propose must not move: owner %q holder %q", l.Owner(l1), l.Holder(l1))
		}
		if err := l.Persisted("x2", ack("x2", 2)); err != nil {
			t.Fatal(err)
		}
		if got, _ := l.Transfer("x2"); len(got.Digests) != 2 || got.Stage != handoff.Persisted {
			t.Fatalf("after persisted: %+v", got)
		}
		if err := l.Commit("x2"); err != nil {
			t.Fatal(err)
		}
		if l.Owner(l1) != cargo || l.Owner(l2) != cargo || l.Holder(l1) != "x2" {
			t.Fatalf("after commit: owner %q %q holder %q", l.Owner(l1), l.Owner(l2), l.Holder(l1))
		}
		if err := l.Activated("x2"); err != nil {
			t.Fatal(err)
		}
		if err := l.Collected("x2"); err != nil {
			t.Fatal(err)
		}
		if l.Holder(l1) != "" || stage(t, l, "x2") != handoff.Collected {
			t.Fatalf("after collected: holder %q stage %s", l.Holder(l1), stage(t, l, "x2"))
		}
		if len(l.Open()) != 0 {
			t.Fatalf("open = %+v", l.Open())
		}
	})

	t.Run("forbidden transitions", func(t *testing.T) {
		l := ledgerWith(t)
		if err := l.Propose(load("x2", l1)); err != nil {
			t.Fatal(err)
		}
		bad := []struct {
			name string
			f    func() error
		}{
			{"commit before persisted", func() error { return l.Commit("x2") }},
			{"activated before committed", func() error { return l.Activated("x2") }},
			{"collected before activated", func() error { return l.Collected("x2") }},
		}
		for _, b := range bad {
			if err := b.f(); !errors.Is(err, handoff.ErrTransition) {
				t.Errorf("%s: %v, want ErrTransition", b.name, err)
			}
		}
		if err := l.Persisted("x2", ack("x2", 1)); err != nil {
			t.Fatal(err)
		}
		if err := l.Persisted("x2", ack("x2", 1)); !errors.Is(err, handoff.ErrTransition) {
			t.Errorf("persisted twice: %v", err)
		}
		if err := l.Commit("x2"); err != nil {
			t.Fatal(err)
		}
		for name, f := range map[string]func() error{
			"commit twice":     func() error { return l.Commit("x2") },
			"abort committed":  func() error { return l.Abort("x2") },
			"persisted after":  func() error { return l.Persisted("x2", ack("x2", 1)) },
			"collected before": func() error { return l.Collected("x2") },
		} {
			if err := f(); !errors.Is(err, handoff.ErrTransition) {
				t.Errorf("%s: %v, want ErrTransition", name, err)
			}
		}
		if stage(t, l, "x2") != handoff.Committed || l.Owner(l1) != cargo {
			t.Fatalf("a failed call must not change anything: %s %q", stage(t, l, "x2"), l.Owner(l1))
		}
		if err := l.Activated("x2"); err != nil {
			t.Fatal(err)
		}
		if err := l.Abort("x2"); !errors.Is(err, handoff.ErrTransition) {
			t.Errorf("abort activated: %v", err)
		}
		if err := l.Collected("x2"); err != nil {
			t.Fatal(err)
		}
		for name, f := range map[string]func() error{
			"abort collected":     func() error { return l.Abort("x2") },
			"activated collected": func() error { return l.Activated("x2") },
			"collected twice":     func() error { return l.Collected("x2") },
		} {
			if err := f(); !errors.Is(err, handoff.ErrTransition) {
				t.Errorf("%s: %v, want ErrTransition", name, err)
			}
		}
		if err := l.Commit("x9"); !errors.Is(err, handoff.ErrUnknownTransfer) {
			t.Errorf("unknown: %v", err)
		}
	})

	t.Run("abort", func(t *testing.T) {
		l := ledgerWith(t)
		if err := l.Propose(load("x2", l1)); err != nil {
			t.Fatal(err)
		}
		if err := l.Abort("x2"); err != nil {
			t.Fatal(err)
		}
		// 遅れて届いた ACK は捨てる
		if err := l.Persisted("x2", ack("x2", 1)); !errors.Is(err, handoff.ErrAborted) {
			t.Errorf("late ack: %v, want ErrAborted", err)
		}
		for name, f := range map[string]func() error{
			"commit aborted": func() error { return l.Commit("x2") },
			"abort twice":    func() error { return l.Abort("x2") },
		} {
			if err := f(); !errors.Is(err, handoff.ErrTransition) {
				t.Errorf("%s: %v, want ErrTransition", name, err)
			}
		}
		if l.Owner(l1) != port1 || l.Holder(l1) != "" {
			t.Fatalf("abort must leave the owner: %q %q", l.Owner(l1), l.Holder(l1))
		}
		// Persisted からも取り消せる
		if err := l.Propose(load("x3", l1)); err != nil {
			t.Fatal(err)
		}
		if err := l.Persisted("x3", ack("x3", 1)); err != nil {
			t.Fatal(err)
		}
		if err := l.Abort("x3"); err != nil {
			t.Fatal(err)
		}
		// 取り消した後に、同じ手紙で別の transfer を最後まで進められる
		mustRun(t, l, load("x4", l1))
		if l.Owner(l1) != cargo {
			t.Fatalf("owner %q", l.Owner(l1))
		}
	})

	t.Run("cas: owner is not From", func(t *testing.T) {
		l := ledgerWith(t)
		// l3 は船にあるので、島 1 の港からの transfer の CAS は通らない
		tr := load("x2", l1, l3)
		if err := l.Propose(tr); err != nil {
			t.Fatal(err)
		}
		if err := l.Persisted("x2", ack("x2", 2)); err != nil {
			t.Fatal(err)
		}
		if err := l.Commit("x2"); !errors.Is(err, handoff.ErrCAS) {
			t.Fatalf("commit: %v, want ErrCAS", err)
		}
		if l.Owner(l1) != port1 || l.Holder(l1) != "" || stage(t, l, "x2") != handoff.Persisted {
			t.Fatalf("a failed CAS must change nothing: %q %q %s", l.Owner(l1), l.Holder(l1), stage(t, l, "x2"))
		}
		if err := l.Abort("x2"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cas: held by another transfer", func(t *testing.T) {
		l := ledgerWith(t)
		// 船から島 1 への 2 つの transfer が同じ手紙 l3 を取り合う。先に Commit した方だけが通る
		down := func(id string) handoff.Transfer {
			return handoff.Transfer{ID: id, From: cargo, To: port1, ContactID: "k2", Letters: []ferry.LetterID{l3},
				Final: []handoff.Place{relay1}}
		}
		for _, id := range []string{"x2", "x3"} {
			if err := l.Propose(down(id)); err != nil {
				t.Fatal(err)
			}
			if err := l.Persisted(id, ack(id, 1)); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Commit("x2"); err != nil {
			t.Fatal(err)
		}
		if l.Owner(l3) != relay1 || l.Holder(l3) != "x2" {
			t.Fatalf("owner %q holder %q", l.Owner(l3), l.Holder(l3))
		}
		if err := l.Commit("x3"); !errors.Is(err, handoff.ErrCAS) {
			t.Fatalf("second commit: %v, want ErrCAS", err)
		}
		// 握っている間は Handoff の外でも動かせない
		if err := l.Move(l3, relay1, handoff.Dropped); !errors.Is(err, handoff.ErrHeld) {
			t.Fatalf("move held: %v, want ErrHeld", err)
		}
		// 別の transfer が倉庫から積もうとしても、Collected までは CAS が通らない
		up := handoff.Transfer{ID: "x4", From: relay1, To: cargo, ContactID: "k2", Letters: []ferry.LetterID{l3}}
		if err := l.Propose(up); err != nil {
			t.Fatal(err)
		}
		if err := l.Persisted("x4", ack("x4", 1)); err != nil {
			t.Fatal(err)
		}
		if err := l.Commit("x4"); !errors.Is(err, handoff.ErrCAS) {
			t.Fatalf("commit while held: %v, want ErrCAS", err)
		}
		if err := l.Activated("x2"); err != nil {
			t.Fatal(err)
		}
		if err := l.Collected("x2"); err != nil {
			t.Fatal(err)
		}
		if err := l.Commit("x4"); err != nil {
			t.Fatalf("commit after collected: %v", err)
		}
		if l.Owner(l3) != cargo {
			t.Fatalf("owner %q", l.Owner(l3))
		}
		open := l.Open()
		if len(open) != 2 || open[0].ID != "x3" || open[1].ID != "x4" {
			t.Fatalf("open = %+v", open)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		l := ledgerWith(t)
		for name, tr := range map[string]handoff.Transfer{
			"bad id":          {ID: "t1", From: port1, To: cargo, ContactID: "k", Letters: []ferry.LetterID{l1}},
			"island→island":   {ID: "x2", From: port1, To: handoff.IslandPort(2), ContactID: "k", Letters: []ferry.LetterID{l1}},
			"ferry→ferry":     {ID: "x2", From: cargo, To: handoff.FerryCargo("f2"), ContactID: "k", Letters: []ferry.LetterID{l1}},
			"to delivered":    {ID: "x2", From: cargo, To: handoff.Delivered, ContactID: "k", Letters: []ferry.LetterID{l1}},
			"no contact":      {ID: "x2", From: port1, To: cargo, Letters: []ferry.LetterID{l1}},
			"no letters":      {ID: "x2", From: port1, To: cargo, ContactID: "k"},
			"duplicate":       {ID: "x2", From: port1, To: cargo, ContactID: "k", Letters: []ferry.LetterID{l1, l1}},
			"final length":    {ID: "x2", From: cargo, To: port1, ContactID: "k", Letters: []ferry.LetterID{l3}, Final: []handoff.Place{}},
			"final elsewhere": {ID: "x2", From: cargo, To: port1, ContactID: "k", Letters: []ferry.LetterID{l3}, Final: []handoff.Place{handoff.IslandRelay(2)}},
			"ferry delivery":  {ID: "x2", From: port1, To: cargo, ContactID: "k", Letters: []ferry.LetterID{l1}, Final: []handoff.Place{handoff.Delivered}},
			"with digests":    {ID: "x2", From: port1, To: cargo, ContactID: "k", Letters: []ferry.LetterID{l1}, Digests: []string{digest('a')}},
			"stage":           {ID: "x2", From: port1, To: cargo, ContactID: "k", Letters: []ferry.LetterID{l1}, Stage: handoff.Committed},
		} {
			if err := l.Propose(tr); !errors.Is(err, handoff.ErrInvalid) {
				t.Errorf("%s: %v, want ErrInvalid", name, err)
			}
		}
		if err := l.Propose(load("x1", l1)); !errors.Is(err, handoff.ErrDuplicate) {
			t.Errorf("duplicate id: %v", err)
		}
		if err := l.Propose(load("x2", l1)); err != nil {
			t.Fatal(err)
		}
		for name, a := range map[string]handoff.Ack{
			"other id":   ack("x3", 1),
			"count":      ack("x2", 2),
			"bad digest": {TransferID: "x2", Digests: []string{"sha256:XY"}},
		} {
			if err := l.Persisted("x2", a); !errors.Is(err, handoff.ErrInvalid) {
				t.Errorf("%s: %v, want ErrInvalid", name, err)
			}
		}
		if err := l.Create(l1, port1); !errors.Is(err, handoff.ErrDuplicate) {
			t.Errorf("create twice: %v", err)
		}
		if err := l.Create(9, cargo); !errors.Is(err, handoff.ErrInvalid) {
			t.Errorf("create on a ferry: %v", err)
		}
		if err := l.Move(l2, relay1, handoff.Dropped); !errors.Is(err, handoff.ErrCAS) {
			t.Errorf("move from the wrong place: %v", err)
		}
		if err := l.Move(l2, port1, handoff.Dropped); err != nil || l.Owner(l2) != handoff.Dropped {
			t.Errorf("move: %v %q", err, l.Owner(l2))
		}
	})

	t.Run("delivery", func(t *testing.T) {
		l := ledgerWith(t)
		tr := handoff.Transfer{ID: "x2", From: cargo, To: handoff.IslandPort(3), ContactID: "k3", Letters: []ferry.LetterID{l3},
			Final: []handoff.Place{handoff.Delivered}}
		mustRun(t, l, tr)
		if l.Owner(l3) != handoff.Delivered {
			t.Fatalf("owner %q", l.Owner(l3))
		}
	})

	t.Run("the transfer is a copy", func(t *testing.T) {
		l := ledgerWith(t)
		tr := load("x2", l1, l2)
		if err := l.Propose(tr); err != nil {
			t.Fatal(err)
		}
		tr.Letters[0] = 7
		got, _ := l.Transfer("x2")
		got.Letters[1] = 8
		again, _ := l.Transfer("x2")
		if again.Letters[0] != l1 || again.Letters[1] != l2 {
			t.Fatalf("letters = %v", again.Letters)
		}
	})
}

// TestResume は handoff-and-contact.md §3.3 の表の各行と、矛盾した組み合わせを確かめる。
func TestResume(t *testing.T) {
	const (
		none      = handoff.HostNone
		prepared  = handoff.HostPrepared
		pending   = handoff.HostPending
		activated = handoff.HostActivated
		collected = handoff.HostCollected
		aborted   = handoff.HostAborted
	)
	for _, c := range []struct {
		st       handoff.Stage
		src, dst handoff.HostTransferState
		want     handoff.Action
	}{
		// §3.3 の 6 つの位置
		{handoff.Proposed, none, none, handoff.ActionResendPrepare},
		{handoff.Proposed, prepared, none, handoff.ActionResendPrepare},
		{handoff.Proposed, prepared, pending, handoff.ActionCommit},
		{handoff.Persisted, prepared, pending, handoff.ActionCommit},
		{handoff.Committed, prepared, pending, handoff.ActionActivate},
		{handoff.Activated, prepared, activated, handoff.ActionGC},
		// 台帳が Host より遅れている（journal に書く前に止まった）
		{handoff.Committed, prepared, activated, handoff.ActionActivate},
		{handoff.Committed, collected, activated, handoff.ActionActivate},
		{handoff.Activated, collected, activated, handoff.ActionGC},
		// 終わっている
		{handoff.Collected, collected, activated, handoff.ActionNone},
		{handoff.Aborted, none, none, handoff.ActionNone},
		{handoff.Aborted, aborted, aborted, handoff.ActionNone},
		{handoff.Aborted, aborted, none, handoff.ActionNone},
		// 取り消しの途中
		{handoff.Proposed, aborted, none, handoff.ActionAbort},
		{handoff.Proposed, prepared, aborted, handoff.ActionAbort},
		{handoff.Persisted, aborted, pending, handoff.ActionAbort},
		{handoff.Aborted, prepared, none, handoff.ActionAbort},
		{handoff.Aborted, aborted, pending, handoff.ActionAbort},
		// 矛盾
		{handoff.Proposed, none, pending, handoff.ActionFail},
		{handoff.Persisted, prepared, none, handoff.ActionFail},
		{handoff.Persisted, none, none, handoff.ActionFail},
		{handoff.Committed, prepared, none, handoff.ActionFail},    // 受け取り側の bytes が失われた
		{handoff.Committed, prepared, aborted, handoff.ActionFail}, // committed の後は取り消さない
		{handoff.Activated, prepared, pending, handoff.ActionFail},
		{handoff.Collected, prepared, activated, handoff.ActionFail},
		{handoff.Proposed, collected, activated, handoff.ActionFail},
		{0, none, none, handoff.ActionFail},
	} {
		if got := handoff.Resume(c.st, c.src, c.dst); got != c.want {
			t.Errorf("Resume(%s, %s, %s) = %s, want %s (%s)", c.st, c.src, c.dst, got, c.want, handoff.Explain(c.st, c.src, c.dst))
		}
	}
}
