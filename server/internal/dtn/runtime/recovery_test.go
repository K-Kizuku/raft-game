package runtime

import (
	"encoding/json"
	"testing"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// fakeHost は試験の中の Actor Host。persistence-and-recovery.md §3.1 の順序（intent を書く → Container に
// 送る → applied にして応答を保存 → 応答を返す）と §4.1 の復元（新しい epoch で journal を再生する）を守る。
type fakeHost struct {
	t        *testing.T
	newActor func() Actor
	rt       *Runtime
	epoch    uint64
	journal  []journalRow

	crashAt   uint64 // この eventSeq で止める
	crashMode string // "before-send"（Actor に届く前）・"after-actor"（Actor の処理の後、Host の確定の前）
}

type journalRow struct {
	seq        uint64
	digest     string
	req        dtnproto.Request // 送ったとおり（epoch は送るときに Host のものにする）
	applied    bool
	resp       []byte
	respDigest string
}

func newFakeHost(t *testing.T, newActor func() Actor) *fakeHost {
	h := &fakeHost{t: t, newActor: newActor, epoch: 1}
	h.rt = New(newActor())
	t.Cleanup(func() { h.rt.Close() })
	return h
}

func (h *fakeHost) envelope(r dtnproto.Request, replay bool) []byte {
	r.Epoch, r.Replay = h.epoch, replay
	b, err := json.Marshal(r)
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func respDigest(t *testing.T, b []byte) string {
	t.Helper()
	var resp dtnproto.Response
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	d, err := dtnproto.ResponseDigest(resp)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// send は Session Front からの要求 1 つを処理する。止まったら (nil, false)。
func (h *fakeHost) send(raw []byte) ([]byte, bool) {
	t := h.t
	req, verr := dtnproto.Validate(raw, dtnproto.DefaultLimits)
	if verr != nil {
		t.Fatalf("host: %v", verr)
	}
	digest, err := dtnproto.RequestDigest(req)
	if err != nil {
		t.Fatal(err)
	}
	// 冪等の確認
	var row *journalRow
	for i := range h.journal {
		if h.journal[i].seq == req.EventSeq {
			row = &h.journal[i]
		}
	}
	if row != nil {
		if row.digest != digest {
			t.Fatalf("host: conflict at %d", req.EventSeq)
		}
		if row.applied {
			return row.resp, true
		}
	} else {
		// intent を書く
		h.journal = append(h.journal, journalRow{seq: req.EventSeq, digest: digest, req: req})
		row = &h.journal[len(h.journal)-1]
	}
	if h.crashAt == req.EventSeq && h.crashMode == "before-send" {
		h.crashAt = 0
		return nil, false
	}
	b := h.rt.HandleRaw(h.envelope(req, false))
	if h.crashAt == req.EventSeq && h.crashMode == "after-actor" {
		h.crashAt = 0
		return nil, false // 応答は確定されずに失われる
	}
	// 確定（applied と応答の保存）してから返す
	row.applied, row.resp, row.respDigest = true, b, respDigest(t, b)
	return b, true
}

// recover は Container を作り直し、新しい epoch で journal を再生する。
func (h *fakeHost) recover() {
	t := h.t
	h.rt.Close()
	h.epoch++
	h.rt = New(h.newActor())
	for i := range h.journal {
		row := &h.journal[i]
		b := h.rt.HandleRaw(h.envelope(row.req, row.applied))
		d := respDigest(t, b)
		if row.applied {
			if d != row.respDigest {
				t.Fatalf("restore_failed at %d: %s", row.seq, b)
			}
			continue
		}
		// 最後の intent の行は、ふつうに処理して確定する
		row.applied, row.resp, row.respDigest = true, b, d
	}
}

func TestFakeHostRecoveryBoundary(t *testing.T) {
	var reqs [][]byte
	reqs = append(reqs, initReq(1).raw())
	for i := range 6 {
		reqs = append(reqs, advanceReq(uint64(i+2), int64(100*(i+1))).raw())
	}
	newActor := func() Actor { return &counterActor{} }

	// 止めなかった場合
	ref := newFakeHost(t, newActor)
	var refDigests []string
	for _, r := range reqs {
		b, ok := ref.send(r)
		if !ok {
			t.Fatal("reference crashed")
		}
		refDigests = append(refDigests, respDigest(t, b))
	}
	refCursor := ref.rt.Handle(req{kind: dtnproto.KindInspect, payload: `{}`, epoch: ref.epoch}.raw())

	for _, mode := range []string{"after-actor", "before-send"} {
		t.Run(mode, func(t *testing.T) {
			h := newFakeHost(t, newActor)
			h.crashAt, h.crashMode = 4, mode
			var digests []string
			for _, r := range reqs {
				b, ok := h.send(r)
				if !ok {
					// Container が止まった。Host が復元し、Session Front は同じ eventSeq で送り直す
					h.recover()
					if b, ok = h.send(r); !ok {
						t.Fatal("crashed twice")
					}
				}
				digests = append(digests, respDigest(t, b))
			}
			if h.epoch != 2 {
				t.Fatalf("epoch = %d, want 2", h.epoch)
			}
			for i := range digests {
				if digests[i] != refDigests[i] {
					t.Errorf("response %d differs from the uncrashed run", i)
				}
			}
			got := h.rt.Handle(req{kind: dtnproto.KindInspect, payload: `{}`, epoch: h.epoch}.raw())
			if got.Cursor == nil || *got.Cursor != *refCursor.Cursor || string(got.Outputs[0].Data) != string(refCursor.Outputs[0].Data) {
				t.Fatalf("final state differs: %+v vs %+v", got, refCursor)
			}
		})
	}
}
