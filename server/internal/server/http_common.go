package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// sessionIDPattern は sessionId（/ws/sessions/{id} の id・internal.init の sessionId）の形（プロトコル §2）。
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,256}$`)

// writeJSON は JSON の応答を書く。ヘッダーは content-type と cache-control: no-store（プロトコル §2）。
func writeJSON(w http.ResponseWriter, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		b = []byte(`{"code":"internal_error","message":"Internal error"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError はエラーの本文（protocol.ErrorResponse）を書く。message は短い英語で、内部の詳細を含めない。
func writeError(w http.ResponseWriter, status int, code protocol.HTTPErrorCode, message string) {
	writeJSON(w, status, protocol.ErrorResponse{Code: code, Message: message})
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, protocol.HTTPErrorCodeNotFound, "Not found")
}

// allowMethod は r.Method が method なら true。違えば 405 と Allow を書いて false（HEAD・OPTIONS も 405）。
func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, protocol.HTTPErrorCodeMethodNotAllowed, "Method not allowed")
	return false
}

// isUpgrade は Upgrade: websocket があるかを返す（大文字・小文字は区別しない）。
func isUpgrade(r *http.Request) bool {
	for _, v := range r.Header.Values("Upgrade") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "websocket") {
				return true
			}
		}
	}
	return false
}

// requireUpgrade は Upgrade: websocket がなければ 426 を書いて false を返す。
func requireUpgrade(w http.ResponseWriter, r *http.Request) bool {
	if isUpgrade(r) {
		return true
	}
	writeError(w, http.StatusUpgradeRequired, protocol.HTTPErrorCodeUpgradeRequired, "Upgrade required")
	return false
}

// serveHealthz は GET /healthz・GET /api/healthz。ステージ定義を読み終えてから待ち受けるので、200 は準備完了。
func (s *Server) serveHealthz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, protocol.HealthzResponse{Status: "ok", Build: s.cfg.Build})
}

// requestPath は振り分けに使うパス（パーセントエンコードはそのまま。大文字・小文字を区別する）。
func requestPath(r *http.Request) string {
	return r.URL.EscapedPath()
}
