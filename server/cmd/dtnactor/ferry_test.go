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

// repoData はリポジトリの data/（islands.json と stages/）。
const repoData = "../../../data"

// ROLE=ferry は実のステージ定義で船の Actor を開く（DTN-04 #161）。
func TestDtnactorFerry(t *testing.T) {
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
		code <- run(ctx, env(map[string]string{"ROLE": "ferry", "DATA_DIR": repoData}), pw, &stderr, roles)
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

	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dctx, "ws://"+listen.Listen+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send := func(seq uint64, kind dtnproto.Kind, payload any) dtnproto.Response {
		t.Helper()
		p, _ := json.Marshal(payload)
		req, _ := json.Marshal(dtnproto.Request{V: 1, SessionID: "s_1", Actor: "ferry:f1", Epoch: 1, CoordEpoch: 1, EventSeq: seq, Kind: kind, Payload: p})
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
	resp := send(1, dtnproto.KindInit, dtnproto.InitPayload{Stage: st.ID, StageHash: st.Hash, Seed: 1, Actor: "ferry:f1"})
	if resp.Status != dtnproto.StatusOK || len(resp.Outputs) != 1 || resp.Outputs[0].Type != "ferry.state" ||
		string(resp.Outputs[0].Data) != `{"state":"docked","at":1,"readyAt":0,"cargo":[]}` {
		t.Fatalf("init: %+v", resp)
	}
	resp = send(2, dtnproto.KindDepart, dtnproto.DepartPayload{T: 0, To: 2, ArriveAt: 3750})
	if resp.Status != dtnproto.StatusOK || string(resp.Outputs[0].Data) != `{"state":"sailing","to":2,"arriveAt":3750,"readyAt":0,"cargo":[]}` {
		t.Fatalf("depart: %+v", resp)
	}
	resp = send(3, dtnproto.KindArrive, dtnproto.ArrivePayload{T: 3750, Island: 3})
	if resp.Error == nil || resp.Error.Code != dtnproto.CodeInvalid {
		t.Fatalf("arrive at a wrong island: %+v", resp)
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

// ステージ定義がなければ、ROLE=ferry は起動の失敗（終了コード 1）。
func TestDtnactorFerryNoData(t *testing.T) {
	for name, dir := range map[string]string{"missing dir": filepath.Join(t.TempDir(), "nope"), "empty dir": t.TempDir()} {
		var stdout, stderr bytes.Buffer
		if c := run(context.Background(), env(map[string]string{"ROLE": "ferry", "DATA_DIR": dir}), &stdout, &stderr, roles); c != 1 {
			t.Errorf("%s: exit code %d, want 1; stderr: %s", name, c, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: wrote to stdout: %q", name, stdout.String())
		}
	}
	if _, err := newFerryActor(Env{Role: "ferry"}); err == nil {
		t.Error("newFerryActor without data: no error")
	}
}
