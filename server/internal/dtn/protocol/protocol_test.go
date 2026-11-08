package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// requestFixture は testdata/requests/*.json の 1 件（TS 側の契約テストも同じ形で読む）。
type requestFixture struct {
	Description string          `json:"description"`
	Request     json.RawMessage `json:"request"`     // 電文（JSON の値）
	RequestText *string         `json:"requestText"` // 電文の bytes をそのまま書いたもの（重複したキー・小数など）
	Limits      map[string]int  `json:"limits"`      // DefaultLimits を部分的に上書きする
	Expect      string          `json:"expect"`      // "ok" かエラーコード
	Digest      string          `json:"digest"`      // ok のときの RequestDigest
}

func (f requestFixture) raw(t *testing.T) []byte {
	t.Helper()
	if f.RequestText != nil {
		return []byte(*f.RequestText)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, f.Request); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f requestFixture) limits(t *testing.T) Limits {
	t.Helper()
	lim := DefaultLimits
	if len(f.Limits) == 0 {
		return lim
	}
	b, _ := json.Marshal(f.Limits)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lim); err != nil {
		t.Fatalf("limits: %v", err)
	}
	return lim
}

func loadFixtures[T any](t *testing.T, dir string) map[string]T {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", dir, err)
	}
	out := map[string]T{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v T
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = v
	}
	return out
}

func TestValidateFixtures(t *testing.T) {
	fx := loadFixtures[requestFixture](t, "requests")
	codes := map[string]bool{"ok": true}
	for _, c := range Codes {
		codes[string(c)] = true
	}
	seen := map[string]bool{}
	for name, f := range fx {
		t.Run(name, func(t *testing.T) {
			if !codes[f.Expect] {
				t.Fatalf("unknown expect %q", f.Expect)
			}
			seen[f.Expect] = true
			req, verr := Validate(f.raw(t), f.limits(t))
			if f.Expect == "ok" {
				if verr != nil {
					t.Fatalf("%s: got %v, want ok", f.Description, verr)
				}
				d, err := RequestDigest(req)
				if err != nil {
					t.Fatal(err)
				}
				if d != f.Digest {
					t.Errorf("digest = %s, want %s", d, f.Digest)
				}
				return
			}
			if verr == nil {
				t.Fatalf("%s: got ok, want %s", f.Description, f.Expect)
			}
			if string(verr.Code) != f.Expect {
				t.Errorf("%s: got %v, want %s", f.Description, verr, f.Expect)
			}
		})
	}
	for _, want := range []string{"ok", "invalid", "too_large", "unsupported_version"} {
		if !seen[want] {
			t.Errorf("no fixture expects %s", want)
		}
	}
}

func TestValidateDefaultMessageLimit(t *testing.T) {
	// 1 MiB をちょうど超える電文（payload の中の未知の巨大な文字列でも、先に長さで断る）
	base := `{"v":1,"sessionId":"s_1","actor":"island:1","epoch":1,"coordEpoch":1,"eventSeq":1,"kind":"inspect","payload":{},"pad":""}`
	pad := DefaultLimits.MaxMessageBytes + 1 - len(base)
	msg := strings.Replace(base, `"pad":""`, `"pad":"`+strings.Repeat("a", pad)+`"`, 1)
	if len(msg) != DefaultLimits.MaxMessageBytes+1 {
		t.Fatalf("len = %d", len(msg))
	}
	if _, err := Validate([]byte(msg), DefaultLimits); err == nil || err.Code != CodeTooLarge {
		t.Fatalf("got %v, want too_large", err)
	}
	// ちょうど 1 MiB なら長さでは断らない（未知のフィールドで invalid）
	msg = strings.Replace(msg, `"pad":"a`, `"pad":"`, 1)
	if _, err := Validate([]byte(msg), DefaultLimits); err == nil || err.Code != CodeInvalid {
		t.Fatalf("got %v, want invalid", err)
	}
}

func TestValidateInvalidUTF8(t *testing.T) {
	msg := []byte(`{"v":1,"sessionId":"s_` + "\xff" + `","actor":"island:1","epoch":1,"coordEpoch":1,"eventSeq":1,"kind":"inspect","payload":{}}`)
	if _, err := Validate(msg, DefaultLimits); err == nil || err.Code != CodeInvalid {
		t.Fatalf("got %v, want invalid", err)
	}
}

type canonicalFixture struct {
	Description string `json:"description"`
	Input       string `json:"input"`
	Canonical   string `json:"canonical"`
	Digest      string `json:"digest"`
	Error       string `json:"error"`
}

func TestCanonicalFixtures(t *testing.T) {
	for name, f := range loadFixtures[canonicalFixture](t, "canonical") {
		t.Run(name, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(f.Input))
			if f.Error != "" {
				var pe *Error
				if !errors.As(err, &pe) || string(pe.Code) != f.Error {
					t.Fatalf("%s: got %q, %v; want error %s", f.Description, got, err, f.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", f.Description, err)
			}
			if string(got) != f.Canonical {
				t.Errorf("canonical = %q, want %q", got, f.Canonical)
			}
			if d := Digest(got); d != f.Digest {
				t.Errorf("digest = %s, want %s", d, f.Digest)
			}
			// 正準の JSON は不動点
			again, err := CanonicalJSON(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Errorf("not idempotent: %q, %v", again, err)
			}
		})
	}
}

func TestCanonicalInvalidUTF8(t *testing.T) {
	if _, err := CanonicalJSON([]byte("\"\xc3\x28\"")); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := Canonical(map[string]any{"x": 1.5}); err == nil {
		t.Fatal("float accepted")
	}
	got, err := Canonical(map[string]any{"b": 1.0, "a": "<&>"})
	if err != nil || string(got) != `{"a":"<&>","b":1}` {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestRequestDigestStable(t *testing.T) {
	a := `{"v":1,"sessionId":"s_1","actor":"island:2","epoch":3,"coordEpoch":1,"eventSeq":7,"kind":"advance","payload":{"horizon":[100,1]}}`
	b := "{ \"payload\" : { \"horizon\" : [ 100 , 1 ] } ,\n \"kind\":\"advance\", \"eventSeq\":7, \"coordEpoch\":1, \"epoch\":3, \"actor\":\"island:2\", \"sessionId\":\"s_1\", \"v\":1 }"
	digest := func(s string) string {
		t.Helper()
		req, verr := Validate([]byte(s), DefaultLimits)
		if verr != nil {
			t.Fatalf("%s: %v", s, verr)
		}
		d, err := RequestDigest(req)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d0 := digest(a)
	if d := digest(b); d != d0 {
		t.Errorf("key order / whitespace changed the digest")
	}
	for _, s := range []string{
		strings.Replace(a, `"epoch":3`, `"epoch":4`, 1),
		strings.Replace(a, `"coordEpoch":1`, `"coordEpoch":9`, 1),
		strings.Replace(a, `"payload"`, `"replay":true,"payload"`, 1),
	} {
		if d := digest(s); d != d0 {
			t.Errorf("epoch/coordEpoch/replay changed the digest: %s", s)
		}
	}
	for _, s := range []string{
		strings.Replace(a, `[100,1]`, `[100,2]`, 1), // payload を 1 バイト変える
		strings.Replace(a, `"eventSeq":7`, `"eventSeq":8`, 1),
		strings.Replace(a, `"sessionId":"s_1"`, `"sessionId":"s_2"`, 1),
		strings.Replace(a, `"island:2"`, `"island:3"`, 1),
	} {
		if d := digest(s); d == d0 {
			t.Errorf("digest did not change: %s", s)
		}
	}
}

func TestResponseDigestIgnoresEpochAndBundle(t *testing.T) {
	r := Response{V: 1, Actor: "island:2", Epoch: 1, EventSeq: 5, Status: StatusOK,
		Outputs: []Output{{T: 1, Class: 3, Seq: 1, Type: "x", Data: json.RawMessage(`{"a":1}`), Bundle: []byte{1, 2}}},
		Cursor:  &Cursor{Horizon: Mark{10, 8}, StateDigest: Digest(nil)}}
	d0, err := ResponseDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	r2 := r
	r2.Epoch = 9
	r2.Outputs = []Output{r.Outputs[0]}
	r2.Outputs[0].Bundle = nil
	if d, _ := ResponseDigest(r2); d != d0 {
		t.Error("epoch/bundle changed the response digest")
	}
	r2.Outputs[0].Data = json.RawMessage(`{"a":2}`)
	if d, _ := ResponseDigest(r2); d == d0 {
		t.Error("data did not change the response digest")
	}
}

func TestKindTable(t *testing.T) {
	kinds := Kinds()
	if len(kinds) != 13+9 {
		t.Fatalf("got %d kinds, want 22", len(kinds))
	}
	var notJournaled []Kind
	for _, k := range kinds {
		if !k.Journaled() {
			notJournaled = append(notJournaled, k)
		}
	}
	slices.Sort(notJournaled)
	want := []Kind{KindBundleExport, KindHostStatus, KindInspect, KindTransferStatus}
	if !slices.Equal(notJournaled, want) {
		t.Errorf("not journaled = %v, want %v", notJournaled, want)
	}
	if len(Codes) != 14 {
		t.Errorf("got %d codes, want 14", len(Codes))
	}
	ki, _ := LookupKind(KindCargoAdd)
	if ki.Accepts(RoleIsland) || !ki.Accepts(RoleFerry) {
		t.Error("cargo.add must be for ferries only")
	}
}

func TestMarkJSON(t *testing.T) {
	b, _ := json.Marshal(AdvancePayload{Horizon: Mark{12500, 1}})
	if string(b) != `{"horizon":[12500,1]}` {
		t.Fatalf("got %s", b)
	}
	var m Mark
	for _, bad := range []string{`[1]`, `[1,2,3]`, `{"t":1}`, `[1,256]`, `[1.5,1]`} {
		if err := json.Unmarshal([]byte(bad), &m); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if !(Mark{1, 8}).Less(Mark{2, 0}) || (Mark{2, 1}).Less(Mark{2, 1}) {
		t.Error("Less")
	}
}

func TestResponseJSONShape(t *testing.T) {
	b, err := json.Marshal(Response{V: 1, Actor: "island:2", Epoch: 3, EventSeq: 1, Status: StatusError, Error: &Error{Code: CodeNotReady, Message: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"actor":"island:2","epoch":3,"eventSeq":1,"status":"error","outputs":[],"cursor":null,"error":{"code":"not_ready","message":"m"}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestActorRef(t *testing.T) {
	if r := IslandRef(2); r != "island:2" || r.Role() != RoleIsland {
		t.Error(r)
	}
	if id, ok := ActorRef("island:12").IslandID(); !ok || id != 12 {
		t.Error(id, ok)
	}
	if id, ok := FerryRef("f-1").FerryID(); !ok || id != "f-1" {
		t.Error(id, ok)
	}
	if _, ok := ActorRef("ferry:f1").IslandID(); ok {
		t.Error("ferry has no island id")
	}
	if CoordinatorRef.Role() != RoleCoordinator {
		t.Error("coordinator")
	}
}

func TestFakeCodecRoundTrip(t *testing.T) {
	c := FakeCodec{}
	raft := &pb.Message{}
	typ, to, from, term := pb.MessageType_MsgApp, uint64(3), uint64(1), uint64(4)
	raft.Type, raft.To, raft.From, raft.Term = &typ, &to, &from, &term
	raft.Entries = []*pb.Entry{{Data: []byte("cmd")}}
	letters := []struct {
		l   ferry.Letter
		cmd []byte
	}{
		{ferry.Letter{ID: 1000000001, Kind: ferry.LetterRaft, From: 1, To: 3, CreatedAt: 1200, Raft: raft}, nil},
		{ferry.Letter{ID: 2000000007, Kind: ferry.LetterRequest, From: 2, To: 1, CreatedAt: 3000, Req: &ferry.RequestNote{ReqID: "stage2-0001", Hops: 1}}, []byte(`{"v":1}`)},
	}
	for _, tc := range letters {
		b, err := c.Encode(tc.l, tc.cmd, 100)
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := c.Encode(tc.l, tc.cmd, 100)
		if !bytes.Equal(b, b2) {
			t.Fatal("encode is not deterministic")
		}
		l, cmd, age, err := c.Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if age != 100 || !bytes.Equal(cmd, tc.cmd) || l.ID != tc.l.ID || l.Kind != tc.l.Kind || l.From != tc.l.From || l.To != tc.l.To || l.CreatedAt != tc.l.CreatedAt {
			t.Fatalf("round trip: %+v %q %d", l, cmd, age)
		}
		switch l.Kind {
		case ferry.LetterRaft:
			if !proto.Equal(l.Raft, tc.l.Raft) {
				t.Fatalf("raft message differs: %v", l.Raft)
			}
		case ferry.LetterRequest:
			if *l.Req != *tc.l.Req {
				t.Fatalf("note differs: %+v", l.Req)
			}
		}
		// WithAge は Age だけを変える
		aged, err := c.WithAge(b, 5000)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(aged, b) {
			t.Fatal("WithAge did not change the bytes")
		}
		re, _ := c.Encode(tc.l, tc.cmd, 5000)
		if !bytes.Equal(aged, re) {
			t.Fatalf("WithAge differs from Encode with the new age:\n%s\n%s", aged, re)
		}
		_, _, age, _ = c.Decode(aged)
		if age != 5000 {
			t.Fatalf("age = %d", age)
		}
	}
	if _, _, _, err := c.Decode([]byte(`{"fake":2}`)); err == nil {
		t.Error("bad bundle accepted")
	}
	if _, err := c.Encode(ferry.Letter{Kind: ferry.LetterRaft}, nil, 0); err == nil {
		t.Error("raft letter without message accepted")
	}
}
