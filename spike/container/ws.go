package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// MaxWSMessageBytes は WS の 1 通の上限（DesignDoc §12.5。spike/worker/limits.ts と同じ）。
const MaxWSMessageBytes = 16384

// handleWS は WS のエコー（M-5）。テキストの JSON のオブジェクトには serverAt（Unix ミリ秒）を足して返し、
// それ以外はそのまま返す。16 KiB を超える電文が来たら 1009 で閉じる（coder/websocket の読み取りの上限）。
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept が応答を書いている
	}
	c.SetReadLimit(MaxWSMessageBytes)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-s.shut.Done():
			_ = c.Close(websocket.StatusGoingAway, "shutting down")
		case <-ctx.Done():
		}
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				_ = c.Close(websocket.StatusInternalError, "read failed")
			}
			return
		}
		if typ == websocket.MessageText {
			data = addServerAt(data, time.Now().UnixMilli())
		}
		if err := c.Write(ctx, typ, data); err != nil {
			return
		}
	}
}

// addServerAt は JSON のオブジェクトに serverAt を足す。JSON のオブジェクトでなければそのまま返す。
func addServerAt(data []byte, now int64) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return data
	}
	obj["serverAt"] = json.RawMessage(strconv.FormatInt(now, 10))
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}
