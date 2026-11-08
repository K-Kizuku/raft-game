// Package sqlitestore は、Actor Host の保存（host.Store）を SQLite のファイルに置く Go の adapter（DTN-07 #164）。
// ドライバは modernc.org/sqlite（純 Go・BSD-3-Clause・CGO なし。NOTICE）。1 Actor = 1 ファイルで、置き場所は
// 呼び出す側が渡す（dtnfabric は --data-dir、試験は t.TempDir()）。
//
// PRAGMA journal_mode=WAL・synchronous=FULL。Update は確定（COMMIT）が戻ってから戻る。
// テーブルは docs/dtn/persistence-and-recovery.md §2 の SQL のとおり。
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // database/sql のドライバ "sqlite"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// Schema は persistence-and-recovery.md §2 のテーブル（IF NOT EXISTS を足しただけ）。
const Schema = `
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS journal (
  event_seq   INTEGER PRIMARY KEY,
  kind        TEXT    NOT NULL,
  req_digest  TEXT    NOT NULL,
  payload     TEXT    NOT NULL,
  status      TEXT    NOT NULL,
  response    TEXT,
  resp_digest TEXT,
  epoch       INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS bundles (
  letter_id   INTEGER PRIMARY KEY,
  bundle      BLOB,
  digest      TEXT    NOT NULL,
  size        INTEGER NOT NULL,
  state       TEXT    NOT NULL,
  place       TEXT    NOT NULL,
  transfer_id TEXT,
  updated_seq INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS transfers (
  transfer_id TEXT PRIMARY KEY,
  role        TEXT NOT NULL,
  peer        TEXT NOT NULL,
  contact_id  TEXT NOT NULL,
  letter_ids  TEXT NOT NULL,
  digests     TEXT NOT NULL,
  state       TEXT NOT NULL,
  ack         TEXT
);
CREATE TABLE IF NOT EXISTS contacts (contact_id TEXT PRIMARY KEY, body TEXT NOT NULL, state TEXT NOT NULL);
`

// Store は 1 Actor の SQLite のファイル。Open で作る。
type Store struct {
	path string

	mu        sync.Mutex
	db        *sql.DB
	destroyed bool
	closed    bool
}

var _ host.Store = (*Store)(nil)

// Open は path の SQLite のファイルを開く（無ければ作る）。テーブルを作り、journal_mode=WAL・
// synchronous=FULL を確かめる。meta.schemaVersion が host.SchemaVersion と違うファイルは、
// unsupported_version の *dtnproto.Error で開かない。
func Open(path string) (*Store, error) {
	if path == "" || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("sqlitestore: invalid path %q", path)
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open %s: %w", path, err)
	}
	// Host は Actor ごとに直列なので、接続は 1 つ（PRAGMA は接続ごとに DSN で当てる）
	db.SetMaxOpenConns(1)
	s := &Store{path: path, db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	ctx := context.Background()
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("sqlitestore: %s: journal_mode: %w", s.path, err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("sqlitestore: %s: journal_mode is %q, not wal", s.path, mode)
	}
	var syncMode int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&syncMode); err != nil {
		return fmt.Errorf("sqlitestore: %s: synchronous: %w", s.path, err)
	}
	if syncMode != 2 { // FULL
		return fmt.Errorf("sqlitestore: %s: synchronous is %d, not FULL", s.path, syncMode)
	}
	if _, err := s.db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("sqlitestore: %s: schema: %w", s.path, err)
	}
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", host.MetaSchemaVersion).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("sqlitestore: %s: meta: %w", s.path, err)
	case v != fmt.Sprint(host.SchemaVersion):
		return dtnproto.Errorf(dtnproto.CodeUnsupportedVersion, "%s: schema version %s (want %d)", s.path, v, host.SchemaVersion)
	}
	return nil
}

// Path は SQLite のファイルのパスを返す。
func (s *Store) Path() string { return s.path }

func (s *Store) handle() (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.destroyed:
		return nil, host.ErrDestroyed
	case s.closed:
		return nil, host.ErrClosed
	}
	return s.db, nil
}

// Update は f を 1 つのトランザクション（BEGIN IMMEDIATE）で実行し、COMMIT が戻ってから戻る。
func (s *Store) Update(f func(host.Tx) error) error {
	return s.run(f, true)
}

// View は f を 1 つのトランザクションで実行し、最後にロールバックする。
func (s *Store) View(f func(host.Tx) error) error {
	return s.run(f, false)
}

func (s *Store) run(f func(host.Tx) error, write bool) error {
	db, err := s.handle()
	if err != nil {
		return err
	}
	ctx := context.Background()
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitestore: begin: %w", err)
	}
	if err := f(&tx{ctx: ctx, tx: sqlTx}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if !write {
		return sqlTx.Rollback()
	}
	if err := sqlTx.Commit(); err != nil {
		_ = sqlTx.Rollback()
		return fmt.Errorf("sqlitestore: commit: %w", err)
	}
	return nil
}

// Destroy はファイルを閉じて消す（-wal・-shm も）。2 回目は何もしない。
func (s *Store) Destroy() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return nil
	}
	if !s.closed {
		if err := s.db.Close(); err != nil {
			return fmt.Errorf("sqlitestore: close: %w", err)
		}
		s.closed = true
	}
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm", s.path + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sqlitestore: remove: %w", err)
		}
	}
	s.destroyed = true
	return nil
}

// Close はファイルを閉じる（消さない）。2 回目は何もしない。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// tx は host.Tx の SQLite の実装。
type tx struct {
	ctx context.Context
	tx  *sql.Tx
}

func (t *tx) exec(q string, args ...any) error {
	if _, err := t.tx.ExecContext(t.ctx, q, args...); err != nil {
		return fmt.Errorf("sqlitestore: %w", err)
	}
	return nil
}

func (t *tx) Meta() (host.Meta, bool, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT k, v FROM meta")
	if err != nil {
		return host.Meta{}, false, fmt.Errorf("sqlitestore: meta: %w", err)
	}
	defer func() { _ = rows.Close() }()
	kv := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return host.Meta{}, false, fmt.Errorf("sqlitestore: meta: %w", err)
		}
		kv[k] = v
	}
	if err := rows.Err(); err != nil {
		return host.Meta{}, false, fmt.Errorf("sqlitestore: meta: %w", err)
	}
	if len(kv) == 0 {
		return host.Meta{}, false, nil
	}
	m, err := host.MetaFromKV(kv)
	return m, true, err
}

func (t *tx) PutMeta(m host.Meta) error {
	if err := t.exec("DELETE FROM meta"); err != nil {
		return err
	}
	for k, v := range m.KV() {
		if err := t.exec("INSERT INTO meta (k, v) VALUES (?, ?)", k, v); err != nil {
			return err
		}
	}
	return nil
}

const journalCols = "event_seq, kind, req_digest, payload, status, response, resp_digest, epoch"

func scanJournal(sc interface{ Scan(...any) error }) (host.JournalRow, error) {
	var (
		r          host.JournalRow
		seq, epoch int64
		kind       string
		payload    string
		resp       sql.NullString
		respDigest sql.NullString
	)
	if err := sc.Scan(&seq, &kind, &r.ReqDigest, &payload, &r.Status, &resp, &respDigest, &epoch); err != nil {
		return r, err
	}
	r.EventSeq, r.Kind, r.Payload, r.Epoch = uint64(seq), dtnproto.Kind(kind), json.RawMessage(payload), uint64(epoch)
	if resp.Valid {
		r.Response = json.RawMessage(resp.String)
	}
	r.RespDigest = respDigest.String
	return r, nil
}

func (t *tx) journalOne(q string, args ...any) (host.JournalRow, bool, error) {
	r, err := scanJournal(t.tx.QueryRowContext(t.ctx, q, args...))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return host.JournalRow{}, false, nil
	case err != nil:
		return host.JournalRow{}, false, fmt.Errorf("sqlitestore: journal: %w", err)
	}
	return r, true, nil
}

func (t *tx) Journal(seq uint64) (host.JournalRow, bool, error) {
	return t.journalOne("SELECT "+journalCols+" FROM journal WHERE event_seq = ?", int64(seq))
}

func (t *tx) JournalBefore(seq uint64) (host.JournalRow, bool, error) {
	return t.journalOne("SELECT "+journalCols+" FROM journal WHERE event_seq < ? ORDER BY event_seq DESC LIMIT 1", int64(seq))
}

func (t *tx) LastJournal() (host.JournalRow, bool, error) {
	return t.journalOne("SELECT " + journalCols + " FROM journal ORDER BY event_seq DESC LIMIT 1")
}

func (t *tx) LastApplied() (host.JournalRow, bool, error) {
	return t.journalOne("SELECT "+journalCols+" FROM journal WHERE status = ? ORDER BY event_seq DESC LIMIT 1", host.StatusApplied)
}

func (t *tx) Journals() ([]host.JournalRow, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+journalCols+" FROM journal ORDER BY event_seq")
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: journal: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []host.JournalRow
	for rows.Next() {
		r, err := scanJournal(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlitestore: journal: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *tx) PutJournal(r host.JournalRow) error {
	return t.exec("INSERT OR REPLACE INTO journal ("+journalCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		int64(r.EventSeq), string(r.Kind), r.ReqDigest, string(r.Payload), r.Status,
		nullString(string(r.Response)), nullString(r.RespDigest), int64(r.Epoch))
}

func (t *tx) DeleteJournal(seq uint64) error {
	return t.exec("DELETE FROM journal WHERE event_seq = ?", int64(seq))
}

const bundleCols = "letter_id, bundle, digest, size, state, place, transfer_id, updated_seq"

func scanBundle(sc interface{ Scan(...any) error }) (host.BundleRow, error) {
	var (
		r        host.BundleRow
		id, seq  int64
		transfer sql.NullString
	)
	if err := sc.Scan(&id, &r.Bundle, &r.Digest, &r.Size, &r.State, &r.Place, &transfer, &seq); err != nil {
		return r, err
	}
	r.LetterID, r.TransferID, r.UpdatedSeq = uint64(id), transfer.String, uint64(seq)
	return r, nil
}

func (t *tx) Bundle(letterID uint64) (host.BundleRow, bool, error) {
	r, err := scanBundle(t.tx.QueryRowContext(t.ctx, "SELECT "+bundleCols+" FROM bundles WHERE letter_id = ?", int64(letterID)))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return host.BundleRow{}, false, nil
	case err != nil:
		return host.BundleRow{}, false, fmt.Errorf("sqlitestore: bundles: %w", err)
	}
	return r, true, nil
}

func (t *tx) PutBundle(r host.BundleRow) error {
	var b any // nil は NULL
	if r.Bundle != nil {
		b = r.Bundle
	}
	return t.exec("INSERT OR REPLACE INTO bundles ("+bundleCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		int64(r.LetterID), b, r.Digest, r.Size, r.State, r.Place, nullString(r.TransferID), int64(r.UpdatedSeq))
}

const transferCols = "transfer_id, role, peer, contact_id, letter_ids, digests, state, ack"

func scanTransfer(sc interface{ Scan(...any) error }) (host.TransferRow, error) {
	var (
		r            host.TransferRow
		ids, digests string
		ack          sql.NullString
	)
	if err := sc.Scan(&r.TransferID, &r.Role, &r.Peer, &r.ContactID, &ids, &digests, &r.State, &ack); err != nil {
		return r, err
	}
	r.LetterIDs, r.Digests = json.RawMessage(ids), json.RawMessage(digests)
	if ack.Valid {
		r.Ack = json.RawMessage(ack.String)
	}
	return r, nil
}

func (t *tx) Transfer(id string) (host.TransferRow, bool, error) {
	r, err := scanTransfer(t.tx.QueryRowContext(t.ctx, "SELECT "+transferCols+" FROM transfers WHERE transfer_id = ?", id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return host.TransferRow{}, false, nil
	case err != nil:
		return host.TransferRow{}, false, fmt.Errorf("sqlitestore: transfers: %w", err)
	}
	return r, true, nil
}

func (t *tx) PutTransfer(r host.TransferRow) error {
	return t.exec("INSERT OR REPLACE INTO transfers ("+transferCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		r.TransferID, r.Role, r.Peer, r.ContactID, string(r.LetterIDs), string(r.Digests), r.State, nullString(string(r.Ack)))
}

func (t *tx) Contact(id string) (host.ContactRow, bool, error) {
	var (
		r    host.ContactRow
		body string
	)
	err := t.tx.QueryRowContext(t.ctx, "SELECT contact_id, body, state FROM contacts WHERE contact_id = ?", id).Scan(&r.ContactID, &body, &r.State)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return host.ContactRow{}, false, nil
	case err != nil:
		return host.ContactRow{}, false, fmt.Errorf("sqlitestore: contacts: %w", err)
	}
	r.Body = json.RawMessage(body)
	return r, true, nil
}

func (t *tx) PutContact(r host.ContactRow) error {
	return t.exec("INSERT OR REPLACE INTO contacts (contact_id, body, state) VALUES (?, ?, ?)", r.ContactID, string(r.Body), r.State)
}

func (t *tx) Snapshot() (host.Snapshot, error) {
	var snap host.Snapshot
	m, ok, err := t.Meta()
	if err != nil {
		return snap, err
	}
	snap.Meta = map[string]string{}
	if ok {
		snap.Meta = m.KV()
	}
	if snap.Journal, err = t.Journals(); err != nil {
		return snap, err
	}
	if err := t.each("SELECT "+bundleCols+" FROM bundles ORDER BY letter_id", func(sc interface{ Scan(...any) error }) error {
		r, err := scanBundle(sc)
		snap.Bundles = append(snap.Bundles, r)
		return err
	}); err != nil {
		return snap, err
	}
	if err := t.each("SELECT "+transferCols+" FROM transfers ORDER BY transfer_id", func(sc interface{ Scan(...any) error }) error {
		r, err := scanTransfer(sc)
		snap.Transfers = append(snap.Transfers, r)
		return err
	}); err != nil {
		return snap, err
	}
	err = t.each("SELECT contact_id, body, state FROM contacts ORDER BY contact_id", func(sc interface{ Scan(...any) error }) error {
		var (
			r    host.ContactRow
			body string
		)
		err := sc.Scan(&r.ContactID, &body, &r.State)
		r.Body = json.RawMessage(body)
		snap.Contacts = append(snap.Contacts, r)
		return err
	})
	return snap, err
}

func (t *tx) each(q string, f func(sc interface{ Scan(...any) error }) error) error {
	rows, err := t.tx.QueryContext(t.ctx, q)
	if err != nil {
		return fmt.Errorf("sqlitestore: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := f(rows); err != nil {
			return fmt.Errorf("sqlitestore: %w", err)
		}
	}
	return rows.Err()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
