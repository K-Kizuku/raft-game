package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// ROLE=island は実のステージ定義で島の Actor を開く（DTN-03 #160）。
func TestDtnactorIsland(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoData, "islands.json")); errors.Is(err, fs.ErrNotExist) {
		t.Skip("data/islands.json がない")
	}
	stages, err := stage.LoadDir(os.DirFS(repoData), "islands.json", "stages")
	if err != nil {
		t.Fatal(err)
	}
	var st *stage.Stage
	for _, s := range stages {
		if s.ID == "stage1" {
			st = s
		}
	}
	if st == nil {
		t.Fatal("stage1 がない")
	}

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, env(map[string]string{"ROLE": "island", "DATA_DIR": repoData}), pw, &stderr, roles)
		_ = pw.Close()
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("%v; stderr: %s", err, stderr.String())
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	var listen struct {
		Listen string `json:"listen"`
	}
	if err := json.Unmarshal([]byte(line), &listen); err != nil {
		t.Fatal(err)
	}

	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dctx, "ws://"+listen.Listen+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send := func(seq uint64, kind dtnproto.Kind, payload any) dtnproto.Response {
		t.Helper()
		p, _ := json.Marshal(payload)
		req, _ := json.Marshal(dtnproto.Request{V: 1, SessionID: "s_1", Actor: "island:1", Epoch: 1, CoordEpoch: 1, EventSeq: seq, Kind: kind, Payload: p})
		if err := c.Write(dctx, websocket.MessageText, req); err != nil {
			t.Fatal(err)
		}
		_, b, err := c.Read(dctx)
		if err != nil {
			t.Fatal(err)
		}
		var resp dtnproto.Response
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := send(1, dtnproto.KindInit, dtnproto.InitPayload{Stage: st.ID, StageHash: st.Hash, Seed: 1, Actor: "island:1"})
	if resp.Status != dtnproto.StatusOK || resp.Cursor == nil || len(resp.Outputs) == 0 {
		t.Fatalf("init: %+v", resp)
	}
	for _, o := range resp.Outputs {
		if o.T != 0 || o.Class != 0 {
			t.Fatalf("init output %+v is not at (0, 0)", o)
		}
	}
	resp = send(2, dtnproto.KindAdvance, dtnproto.AdvancePayload{Horizon: dtnproto.Mark{T: 5000, C: 8}})
	if resp.Status != dtnproto.StatusOK || resp.Cursor == nil || resp.Cursor.Horizon != (dtnproto.Mark{T: 5000, C: 8}) {
		t.Fatalf("advance: %+v", resp)
	}
	created := 0
	for _, o := range resp.Outputs {
		if o.Type == "letter.created" {
			created++
			if len(o.Bundle) == 0 || dtnproto.Digest(o.Bundle) == "" {
				t.Fatalf("letter.created without a bundle: %+v", o)
			}
		}
	}
	if created == 0 {
		t.Fatal("no letter.created in 5 s of the leader island")
	}
	resp = send(3, dtnproto.KindAdvance, dtnproto.AdvancePayload{Horizon: dtnproto.Mark{T: 4000, C: 8}})
	if resp.Error == nil || resp.Error.Code != dtnproto.CodeInvalid {
		t.Fatalf("decreasing horizon: %+v", resp)
	}

	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit code %d; stderr: %s", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
}
