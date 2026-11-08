package sqlitestore_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host/sqlitestore"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

func open(t *testing.T, path string) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTripAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	s := open(t, path)
	meta := host.Meta{SchemaVersion: host.SchemaVersion, SessionID: "s1", Actor: "ferry:f1", Epoch: 3, MaxCoordEpoch: 2,
		InitPayload: json.RawMessage(`{"x":1}`), CreatedAt: "2026-01-01T00:00:00Z", JournalRows: 2, StoredBytes: 10}
	want := host.Snapshot{
		Meta: meta.KV(),
		Journal: []host.JournalRow{
			{EventSeq: 1, Kind: dtnproto.KindInit, ReqDigest: "sha256:a", Payload: json.RawMessage(`{"x":1}`), Status: host.StatusApplied,
				Response: json.RawMessage(`{"ok":true}`), RespDigest: "sha256:b", Epoch: 3},
			{EventSeq: 4, Kind: dtnproto.KindCargoAdd, ReqDigest: "sha256:c", Payload: json.RawMessage(`{ "y" : 2 }`), Status: host.StatusIntent, Epoch: 3},
		},
		Bundles: []host.BundleRow{
			{LetterID: 7, Bundle: []byte{0, 1, 2}, Digest: "sha256:d", Size: 3, State: host.BundleHeld, Place: host.PlaceCargo, UpdatedSeq: 4},
			{LetterID: 9, Digest: "sha256:e", Size: 5, State: host.BundleGone, Place: host.PlaceMoved, TransferID: "t1", UpdatedSeq: 4},
		},
		Transfers: []host.TransferRow{{TransferID: "t1", Role: "source", Peer: "island:2", ContactID: "c1",
			LetterIDs: json.RawMessage(`[9]`), Digests: json.RawMessage(`["sha256:e"]`), State: "prepared"}},
		Contacts: []host.ContactRow{{ContactID: "c1", Body: json.RawMessage(`{"contactId":"c1"}`), State: "open"}},
	}
	if err := s.Update(func(tx host.Tx) error {
		if err := tx.PutMeta(meta); err != nil {
			return err
		}
		for _, r := range want.Journal {
			if err := tx.PutJournal(r); err != nil {
				return err
			}
		}
		for _, r := range want.Bundles {
			if err := tx.PutBundle(r); err != nil {
				return err
			}
		}
		if err := tx.PutTransfer(want.Transfers[0]); err != nil {
			return err
		}
		return tx.PutContact(want.Contacts[0])
	}); err != nil {
		t.Fatal(err)
	}
	// ロールバックしたトランザクションは残らない
	boom := errors.New("boom")
	if err := s.Update(func(tx host.Tx) error {
		_ = tx.DeleteJournal(1)
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.View(func(host.Tx) error { return nil }); !errors.Is(err, host.ErrClosed) {
		t.Fatalf("View after Close: %v", err)
	}

	s = open(t, path)
	defer func() { _ = s.Close() }()
	var got host.Snapshot
	if err := s.View(func(tx host.Tx) (err error) {
		if got, err = tx.Snapshot(); err != nil {
			return err
		}
		m, ok, err := tx.Meta()
		if err != nil || !ok || !reflect.DeepEqual(m, meta) {
			t.Errorf("Meta = %+v %v %v", m, ok, err)
		}
		if r, ok, _ := tx.JournalBefore(4); !ok || r.EventSeq != 1 {
			t.Errorf("JournalBefore(4) = %+v %v", r, ok)
		}
		if r, ok, _ := tx.LastJournal(); !ok || r.EventSeq != 4 {
			t.Errorf("LastJournal = %+v %v", r, ok)
		}
		if r, ok, _ := tx.LastApplied(); !ok || r.EventSeq != 1 {
			t.Errorf("LastApplied = %+v %v", r, ok)
		}
		if _, ok, _ := tx.Journal(2); ok {
			t.Error("Journal(2) exists")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot after reopen:\n got %+v\nwant %+v", got, want)
	}
}

func TestPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	s := open(t, path)
	defer func() { _ = s.Close() }()
	// 別の接続で、ファイルが WAL になっていることを確かめる
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
}

func TestUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	s := open(t, path)
	if err := s.Update(func(tx host.Tx) error {
		return tx.PutMeta(host.Meta{SchemaVersion: 2, SessionID: "s1", Actor: "island:1"})
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err := sqlitestore.Open(path)
	if pe, ok := errors.AsType[*dtnproto.Error](err); !ok || pe.Code != dtnproto.CodeUnsupportedVersion {
		t.Fatalf("Open = %v, want unsupported_version", err)
	}
}

func TestDestroy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	s := open(t, path)
	if err := s.Update(func(tx host.Tx) error {
		return tx.PutMeta(host.Meta{SchemaVersion: 1, SessionID: "s1", Actor: "island:1", Epoch: 1})
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Destroy(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
	if err := s.Update(func(host.Tx) error { return nil }); !errors.Is(err, host.ErrDestroyed) {
		t.Fatalf("Update after Destroy: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidPath(t *testing.T) {
	for _, p := range []string{"", "a?b"} {
		if _, err := sqlitestore.Open(p); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}
	}
}
