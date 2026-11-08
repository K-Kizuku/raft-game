// Package contracttest は、Actor Host の契約テストの fixture（server/internal/dtn/host/testdata/contract/*.json）
// を読んで Host に流す runner（DTN-07 #164）。TS の ActorHost（DTN-12）の runner も同じ fixture を同じ規則で読む。
// fixture の形と規則は docs/dtn/persistence-and-recovery.md §7.3。
package contracttest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// Fixture は 1 つの場面（1 つのファイル）。
type Fixture struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Meta は Host の設定（新しい保存の meta になる）。
	Meta FixtureMeta `json:"meta"`
	// Limits は dtnproto.Limits の一部を上書きする（JSON の名前。maxMessageBytes など）。
	Limits map[string]int `json:"limits,omitempty"`
	// ActorTimeoutMs は Container の応答を待つ上限（省略なら 10 秒）。actorReply が "timeout" の場面で短くする。
	ActorTimeoutMs int `json:"actorTimeoutMs,omitempty"`
	// Defaults は各 step の request に足りないフィールドを補う値（浅いマージ。request の値が勝つ）。
	Defaults map[string]json.RawMessage `json:"defaults,omitempty"`
	Steps    []Step                     `json:"steps"`
}

// FixtureMeta は Host の設定。
type FixtureMeta struct {
	SessionID string `json:"sessionId"`
	Actor     string `json:"actor"`
	Epoch     uint64 `json:"epoch"` // 新しい保存の meta.epoch（0 なら 1）
}

// Step は 1 つの要求。
type Step struct {
	Note string `json:"note,omitempty"`
	// Request は要求の電文（JSON の値。Defaults を補って送る）。RequestText は bytes をそのまま送る電文
	// （Defaults を補わない）。どちらか 1 つ。
	Request     json.RawMessage `json:"request,omitempty"`
	RequestText *string         `json:"requestText,omitempty"`
	// ActorReply は偽の Container の応答。null（省略）は「呼ばれない」（呼ばれたら接続の誤りを返す）、
	// オブジェクトは応答（足りないフィールドは Host が送った要求から補う）、"timeout" は応答しない、
	// "disconnect" は接続が切れる。
	ActorReply json.RawMessage `json:"actorReply,omitempty"`
	// PeerReply は偽の受け取る Host（CLA）の応答（transfer.prepare の accept。DTN-08）。規則は ActorReply と同じ
	// （null は「呼ばれない」・オブジェクトは応答で足りないフィールドを補う・"timeout"・"disconnect"）。
	PeerReply json.RawMessage `json:"peerReply,omitempty"`
	// CrashAt は止める位置（afterIntent・afterActorReply・afterCommit）。止めたら Store を開き直し、
	// 新しい Host で次の step へ進む。
	CrashAt *string `json:"crashAt,omitempty"`
	Expect  Expect  `json:"expect"`
}

// Expect は step の後に確かめること。値の照合は部分一致（§7.3）。
type Expect struct {
	// Response は応答（CrashAt のときは書かない）。
	Response json.RawMessage `json:"response,omitempty"`
	// Crashed は CrashAt で止まった（応答が無い）こと。
	Crashed bool `json:"crashed,omitempty"`
	// ActorCalled は Container が呼ばれたか（省略なら「actorReply が null でない」）。
	ActorCalled *bool `json:"actorCalled,omitempty"`
	// ActorRequest は Container に送った要求（部分一致）。
	ActorRequest json.RawMessage `json:"actorRequest,omitempty"`
	// PeerCalled は受け取る Host（CLA）が呼ばれたか（省略なら「peerReply が null でない」）。
	PeerCalled *bool `json:"peerCalled,omitempty"`
	// PeerRequest は受け取る Host に送った transfer.accept の電文（部分一致）。
	PeerRequest json.RawMessage `json:"peerRequest,omitempty"`
	// Rows は保存の行（meta・journal・bundles・transfers・contacts）。
	Rows map[string]json.RawMessage `json:"rows,omitempty"`
	// Destroyed は保存がすべて消えたこと（shutdown）。
	Destroyed bool `json:"destroyed,omitempty"`
}

// Backend は fixture を流す保存。
type Backend struct {
	// Open は同じ Actor の保存を開く（開き直しでも同じ保存を開く）。
	Open func() (host.Store, error)
	// Destroyed は保存がすべて消えたか。
	Destroyed func() (bool, error)
}

// Load は fixture のファイルを読む（未知のフィールドは誤り）。
func Load(path string) (Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var fx Fixture
	if err := dec.Decode(&fx); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	return fx, nil
}

// RunDir は dir の *.json をファイル名の順に読み、fixture ごとに newBackend の保存で Run する。
func RunDir(t *testing.T, dir string, newBackend func(t *testing.T) Backend) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixtures in %s", dir)
	}
	slices.Sort(files)
	for _, f := range files {
		fx, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.TrimSuffix(filepath.Base(f), ".json"); fx.Name != want {
			t.Fatalf("%s: name %q must be the file name %q", f, fx.Name, want)
		}
		t.Run(fx.Name, func(t *testing.T) { Run(t, fx, newBackend(t)) })
	}
}

var errCrash = errors.New("contracttest: crash")

// Run は fx の step を順に流して確かめる。
func Run(t *testing.T, fx Fixture, b Backend) {
	t.Helper()
	lim, err := limits(fx.Limits)
	if err != nil {
		t.Fatal(err)
	}
	conn := &scriptConn{}
	peer := &scriptConn{}
	var crashAt string
	cfg := host.Config{
		SessionID:    fx.Meta.SessionID,
		Actor:        dtnproto.ActorRef(fx.Meta.Actor),
		Limits:       lim,
		ActorTimeout: time.Duration(fx.ActorTimeoutMs) * time.Millisecond,
		InitialEpoch: fx.Meta.Epoch,
		Peer:         host.PeerFunc(peer.Call),
		Contacts:     host.RecordOnlyContacts{},
		Now:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Hooks: host.Hooks{At: func(p host.Point) error {
			if string(p) == crashAt {
				return errCrash
			}
			return nil
		}},
	}
	var (
		store host.Store
		h     *host.Host
	)
	open := func() {
		t.Helper()
		s, err := b.Open()
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		hh, err := host.New(s, conn, cfg)
		if err != nil {
			t.Fatalf("new host: %v", err)
		}
		store, h = s, hh
	}
	open()
	defer func() { _ = h.Close() }()

	for i, st := range fx.Steps {
		where := fmt.Sprintf("step %d", i)
		if st.Note != "" {
			where += " (" + st.Note + ")"
		}
		raw, err := buildRequest(fx.Defaults, st)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		conn.reset(st.ActorReply)
		peer.reset(st.PeerReply)
		crashAt = ""
		if st.CrashAt != nil {
			crashAt = *st.CrashAt
			if !slices.Contains(host.Points, host.Point(crashAt)) && !slices.Contains(host.HandoffPoints, host.Point(crashAt)) {
				t.Fatalf("%s: unknown crashAt %q", where, crashAt)
			}
		}
		resp, herr := h.Handle(context.Background(), raw)

		wantCalled := len(st.ActorReply) > 0 && string(st.ActorReply) != "null"
		if st.Expect.ActorCalled != nil {
			wantCalled = *st.Expect.ActorCalled
		}
		if got := conn.calls > 0; got != wantCalled {
			t.Errorf("%s: actor called = %v (%d calls), want %v", where, got, conn.calls, wantCalled)
		}
		if conn.calls > 1 {
			t.Errorf("%s: actor called %d times", where, conn.calls)
		}
		wantPeer := len(st.PeerReply) > 0 && string(st.PeerReply) != "null"
		if st.Expect.PeerCalled != nil {
			wantPeer = *st.Expect.PeerCalled
		}
		if got := peer.calls > 0; got != wantPeer {
			t.Errorf("%s: peer called = %v (%d calls), want %v", where, got, peer.calls, wantPeer)
		}
		if peer.calls > 1 {
			t.Errorf("%s: peer called %d times", where, peer.calls)
		}
		if len(st.Expect.PeerRequest) > 0 {
			if peer.calls == 0 {
				t.Errorf("%s: peerRequest expected but the peer was not called", where)
			} else if err := matchJSON(peer.lastReq, st.Expect.PeerRequest); err != nil {
				t.Errorf("%s: peerRequest: %v\n got: %s", where, err, peer.lastReq)
			}
		}
		if len(st.Expect.ActorRequest) > 0 {
			if conn.calls == 0 {
				t.Errorf("%s: actorRequest expected but the actor was not called", where)
			} else if err := matchJSON(conn.lastReq, st.Expect.ActorRequest); err != nil {
				t.Errorf("%s: actorRequest: %v\n got: %s", where, err, conn.lastReq)
			}
		}

		if st.Expect.Crashed || crashAt != "" {
			if !st.Expect.Crashed || crashAt == "" {
				t.Fatalf("%s: crashAt and expect.crashed must be given together", where)
			}
			if !errors.Is(herr, host.ErrStopped) {
				t.Fatalf("%s: want stop at %s, got response %s err %v", where, crashAt, resp, herr)
			}
			_ = h.Close()
			open()
		} else {
			if herr != nil {
				t.Fatalf("%s: Handle: %v", where, herr)
			}
			if len(st.Expect.Response) == 0 {
				t.Fatalf("%s: expect.response is required", where)
			}
			if err := matchJSON(resp, st.Expect.Response); err != nil {
				t.Errorf("%s: response: %v\n got: %s", where, err, resp)
			}
		}

		if st.Expect.Destroyed {
			d, err := b.Destroyed()
			if err != nil {
				t.Fatalf("%s: destroyed: %v", where, err)
			}
			if !d {
				t.Errorf("%s: store is not destroyed", where)
			}
		}
		if len(st.Expect.Rows) > 0 {
			var snap host.Snapshot
			if err := store.View(func(tx host.Tx) (err error) { snap, err = tx.Snapshot(); return err }); err != nil {
				t.Fatalf("%s: snapshot: %v", where, err)
			}
			got, err := SnapshotJSON(snap)
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			for _, table := range sortedKeys(st.Expect.Rows) {
				if err := matchJSON(got[table], st.Expect.Rows[table]); err != nil {
					t.Errorf("%s: rows.%s: %v\n got: %s", where, table, err, got[table])
				}
			}
		}
		if t.Failed() {
			return
		}
	}
}

// SnapshotJSON は保存の行を fixture の rows の形（表ごとの JSON。列の名前は SQL のまま）にする。
// meta は {k: v}（v は TEXT）、journal の payload・response と transfers・contacts の JSON の列は JSON の値、
// bundle は base64（RFC 4648 §4）、NULL は null。
func SnapshotJSON(s host.Snapshot) (map[string]json.RawMessage, error) {
	type obj = map[string]any
	jsonOrNull := func(b json.RawMessage) any {
		if len(b) == 0 {
			return nil
		}
		return b
	}
	strOrNull := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	journal := make([]obj, 0, len(s.Journal))
	for _, r := range s.Journal {
		journal = append(journal, obj{"event_seq": r.EventSeq, "kind": r.Kind, "req_digest": r.ReqDigest,
			"payload": jsonOrNull(r.Payload), "status": r.Status, "response": jsonOrNull(r.Response),
			"resp_digest": strOrNull(r.RespDigest), "epoch": r.Epoch})
	}
	bundles := make([]obj, 0, len(s.Bundles))
	for _, r := range s.Bundles {
		var b any
		if r.Bundle != nil {
			b = base64.StdEncoding.EncodeToString(r.Bundle)
		}
		bundles = append(bundles, obj{"letter_id": r.LetterID, "bundle": b, "digest": r.Digest, "size": r.Size,
			"state": r.State, "place": r.Place, "transfer_id": strOrNull(r.TransferID), "updated_seq": r.UpdatedSeq})
	}
	transfers := make([]obj, 0, len(s.Transfers))
	for _, r := range s.Transfers {
		transfers = append(transfers, obj{"transfer_id": r.TransferID, "role": r.Role, "peer": r.Peer,
			"contact_id": r.ContactID, "letter_ids": jsonOrNull(r.LetterIDs), "digests": jsonOrNull(r.Digests),
			"state": r.State, "ack": jsonOrNull(r.Ack)})
	}
	contacts := make([]obj, 0, len(s.Contacts))
	for _, r := range s.Contacts {
		contacts = append(contacts, obj{"contact_id": r.ContactID, "body": jsonOrNull(r.Body), "state": r.State})
	}
	meta := s.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	out := map[string]json.RawMessage{}
	for k, v := range map[string]any{"meta": meta, "journal": journal, "bundles": bundles, "transfers": transfers, "contacts": contacts} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", k, err)
		}
		out[k] = b
	}
	return out, nil
}

func limits(over map[string]int) (dtnproto.Limits, error) {
	lim := dtnproto.DefaultLimits
	if len(over) == 0 {
		return lim, nil
	}
	b, _ := json.Marshal(lim)
	m := map[string]int{}
	_ = json.Unmarshal(b, &m)
	for k, v := range over {
		if _, ok := m[k]; !ok {
			return lim, fmt.Errorf("unknown limit %q", k)
		}
		m[k] = v
	}
	b, _ = json.Marshal(m)
	err := json.Unmarshal(b, &lim)
	return lim, err
}

func buildRequest(defaults map[string]json.RawMessage, st Step) ([]byte, error) {
	switch {
	case st.RequestText != nil && len(st.Request) > 0:
		return nil, errors.New("request and requestText are exclusive")
	case st.RequestText != nil:
		return []byte(*st.RequestText), nil
	case len(st.Request) == 0:
		return nil, errors.New("request or requestText is required")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(st.Request, &m); err != nil {
		return nil, fmt.Errorf("request must be an object: %w", err)
	}
	for k, v := range defaults {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// scriptConn は台本どおりに答える偽の Container（と、偽の受け取る Host）。
type scriptConn struct {
	reply   json.RawMessage
	calls   int
	lastReq []byte
}

func (c *scriptConn) reset(reply json.RawMessage) {
	c.reply, c.calls, c.lastReq = reply, 0, nil
}

func (c *scriptConn) Call(ctx context.Context, raw []byte) ([]byte, error) {
	c.calls++
	c.lastReq = append([]byte(nil), raw...)
	r := bytes.TrimSpace(c.reply)
	switch string(r) {
	case "", "null":
		return nil, errors.New("contracttest: the actor must not be called in this step")
	case `"timeout"`:
		<-ctx.Done()
		return nil, ctx.Err()
	case `"disconnect"`:
		return nil, errors.New("contracttest: disconnected")
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal(r, &reply); err != nil {
		return nil, fmt.Errorf("contracttest: actorReply must be an object: %w", err)
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	fill := map[string]json.RawMessage{
		"v": json.RawMessage("1"), "actor": req["actor"], "epoch": req["epoch"], "eventSeq": req["eventSeq"],
		"status": json.RawMessage(`"ok"`), "outputs": json.RawMessage("[]"), "cursor": json.RawMessage("null"), "error": json.RawMessage("null"),
	}
	for k, v := range fill {
		if _, ok := reply[k]; !ok {
			reply[k] = v
		}
	}
	return json.Marshal(reply)
}

func (c *scriptConn) Close() error { return nil }

// matchJSON は actual が expected に部分一致するかを確かめる。
func matchJSON(actual, expected []byte) error {
	a, err := decode(actual)
	if err != nil {
		return fmt.Errorf("actual: %w", err)
	}
	e, err := decode(expected)
	if err != nil {
		return fmt.Errorf("expected: %w", err)
	}
	return match(a, e, "$")
}

func decode(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// absent は「そのキーが無い」ことを表す期待値。
const absent = "$absent"

// match は部分一致の規則（§7.3）: オブジェクトは期待のキーだけを比べる（期待が null なら、キーが無くてもよい。
// 期待が "$absent" なら、キーが無いこと）。配列は同じ長さで要素ごと。数・文字列・真偽・null は等しいこと。
func match(a, e any, path string) error {
	switch ev := e.(type) {
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object, got %v", path, a)
		}
		for _, k := range sortedKeys(ev) {
			x, has := av[k]
			if s, ok := ev[k].(string); ok && s == absent {
				if has {
					return fmt.Errorf("%s.%s: want absent, got %v", path, k, x)
				}
				continue
			}
			if !has && ev[k] != nil {
				return fmt.Errorf("%s.%s: missing", path, k)
			}
			if err := match(x, ev[k], path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		av, ok := a.([]any)
		if !ok {
			return fmt.Errorf("%s: want an array, got %v", path, a)
		}
		if len(av) != len(ev) {
			return fmt.Errorf("%s: want %d elements, got %d", path, len(ev), len(av))
		}
		for i := range ev {
			if err := match(av[i], ev[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if a != e {
		return fmt.Errorf("%s: want %v, got %v", path, e, a)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
