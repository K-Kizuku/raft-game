package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// POST /api/sessions の本文の上限（プロトコル §2。Worker と同じ）。
const (
	maxCreateBodyBytes   = 4096
	maxTurnstileTokenLen = 2048
	maxSafeInteger       = 1<<53 - 1
	wsSessionsPrefix     = "/ws/sessions/"
)

// serveStandalone は standalone モードの振り分け（プロトコル §2）。
// 検査の順序は「パス（404）→ メソッド（405）→ Origin（403）→ ルートごとの処理」。
func (s *Server) serveStandalone(w http.ResponseWriter, r *http.Request) {
	p := requestPath(r)
	switch p {
	case "/healthz", "/api/healthz":
		if !allowMethod(w, r, http.MethodGet) || !s.checkOrigin(w, r, false) {
			return
		}
		s.serveHealthz(w, r)
		return
	case "/api/stages":
		if !allowMethod(w, r, http.MethodGet) || !s.checkOrigin(w, r, false) {
			return
		}
		writeJSON(w, http.StatusOK, s.stageSummaries())
		return
	case "/api/sessions":
		if !allowMethod(w, r, http.MethodPost) || !s.checkOrigin(w, r, true) {
			return
		}
		s.createSession(w, r)
		return
	}
	if id, ok := strings.CutPrefix(p, wsSessionsPrefix); ok && sessionIDPattern.MatchString(id) {
		if !allowMethod(w, r, http.MethodGet) || !s.checkOrigin(w, r, true) || !requireUpgrade(w, r) {
			return
		}
		sess, ok := s.standalone.lookup(id, r.URL.Query().Get("token"))
		if !ok {
			notFound(w)
			return
		}
		s.standalone.serveWS(w, r, sess)
		return
	}
	notFound(w)
}

// checkOrigin は Origin を許可リストと照合する（プロトコル §2 の「Origin の規則」）。
// 許可リストが空なら、リクエスト自身のオリジン（http:// ＋ Host）だけを許す。照合は文字列の完全一致。
// required なら Origin がなければ 403。違反は 403 を書いて false。
func (s *Server) checkOrigin(w http.ResponseWriter, r *http.Request, required bool) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		if !required {
			return true
		}
	} else if len(values) == 1 && s.originAllowed(values[0], r) {
		return true
	}
	writeError(w, http.StatusForbidden, protocol.HTTPErrorCodeForbiddenOrigin, "Origin not allowed")
	return false
}

func (s *Server) originAllowed(origin string, r *http.Request) bool {
	if len(s.cfg.AllowedOrigins) == 0 {
		return origin == "http://"+r.Host
	}
	return contains(s.cfg.AllowedOrigins, origin)
}

// createSessionBody は POST /api/sessions の本文を読んだもの。
type createSessionBody struct {
	stage *stage.Stage
	seed  uint64
	demo  bool
}

// createSession は POST /api/sessions（Origin は検査済み）。Worker と同じ規則で本文を確かめる。
// 署名・Turnstile・レート制限はしない（ローカル専用）。
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	badRequest := func() {
		writeError(w, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest, "Invalid request body")
	}
	body, ok := s.readCreateBody(r)
	if !ok {
		badRequest()
		return
	}
	if s.standalone.full() {
		writeError(w, http.StatusServiceUnavailable, protocol.HTTPErrorCodeSessionsDisabled, "too many sessions")
		return
	}

	id, err := randomToken()
	if err != nil {
		s.log.Error("session id", "err", err.Error())
		writeError(w, http.StatusInternalServerError, protocol.HTTPErrorCodeInternalError, "Internal error")
		return
	}
	id = "s_" + id
	token, err := randomToken()
	if err != nil {
		s.log.Error("session token", "err", err.Error())
		writeError(w, http.StatusInternalServerError, protocol.HTTPErrorCodeInternalError, "Internal error")
		return
	}

	cfg, err := stage.SimConfig(body.stage)
	var e *sim.Engine
	if err == nil {
		if r := recovered(func() { e, err = sim.New(cfg, body.seed, s.simOptions()) }); r != nil {
			err = errors.New("sim.New panicked")
		}
	}
	if err != nil {
		s.log.Error("create session", "session", id, "stage", body.stage.ID, "err", err.Error())
		writeError(w, http.StatusInternalServerError, protocol.HTTPErrorCodeInternalError, "Internal error")
		return
	}

	params := s.sessionParams()
	params.id = id
	params.st = body.stage
	params.islands, _ = s.cat.islands(body.stage.ID)
	params.seed = body.seed
	params.demo = body.demo
	params.engine = e
	params.onBroken = func() { s.standalone.remove(id) }
	sess := newSession(s.baseCtx, params)
	if !s.standalone.add(id, newStandaloneEntry(sess, token)) {
		writeError(w, http.StatusServiceUnavailable, protocol.HTTPErrorCodeSessionsDisabled, "too many sessions")
		return
	}
	sess.start()
	s.log.Info("session created", "session", id, "stage", body.stage.ID)
	writeJSON(w, http.StatusCreated, protocol.CreateSessionResponse{
		SessionID: id,
		Token:     token,
		WSPath:    wsSessionsPrefix + id,
	})
}

// readCreateBody は POST /api/sessions の本文を読んで確かめる（プロトコル §2）。不正なら false。
func (s *Server) readCreateBody(r *http.Request) (createSessionBody, bool) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return createSessionBody{}, false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxCreateBodyBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxCreateBodyBytes || !utf8.Valid(data) {
		return createSessionBody{}, false
	}

	// JSON のオブジェクト 1 つで、後ろに余分なものがないこと。
	dec := json.NewDecoder(bytes.NewReader(data))
	var m map[string]json.RawMessage
	if err := dec.Decode(&m); err != nil || m == nil {
		return createSessionBody{}, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return createSessionBody{}, false
	}
	for k := range m {
		switch k {
		case "stageId", "seed", "demo", "turnstileToken":
		default:
			return createSessionBody{}, false
		}
	}

	var out createSessionBody
	stageID, ok := jsonString(m["stageId"])
	if !ok {
		return createSessionBody{}, false
	}
	if out.stage, ok = s.cat.Lookup(stageID); !ok {
		return createSessionBody{}, false
	}
	if !s.stageVisible(out.stage) {
		// 一覧に出ないステージ（デモ操作が無効なときのデモ用のステージ）は受け付けない（Worker と同じ）。
		return createSessionBody{}, false
	}

	if raw, has := m["seed"]; has {
		// 数の字句が 0 以上 2^53 − 1 以下の整数であること（"1"・-1・1.5・1.0・1e3 は不可）。
		n, err := strconv.ParseUint(string(bytes.TrimSpace(raw)), 10, 64)
		if err != nil || n > maxSafeInteger {
			return createSessionBody{}, false
		}
		out.seed = n
	} else {
		seed, err := randomSeed()
		if err != nil {
			return createSessionBody{}, false
		}
		out.seed = seed
	}

	if raw, has := m["demo"]; has {
		switch string(bytes.TrimSpace(raw)) {
		case "true":
			if !s.cfg.DemoControls {
				return createSessionBody{}, false
			}
			out.demo = true
		case "false":
		default:
			return createSessionBody{}, false
		}
	}

	if raw, has := m["turnstileToken"]; has {
		// 値は使わない（standalone は Turnstile を確かめない）。長さは JS の文字数（UTF-16）で数える。
		tok, ok := jsonString(raw)
		if !ok || len(utf16.Encode([]rune(tok))) > maxTurnstileTokenLen {
			return createSessionBody{}, false
		}
	}
	return out, true
}

// jsonString は raw が JSON の文字列ならその値を返す（null・数などは false）。
func jsonString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return v, true
}

// randomToken は 128 bit の乱数の base64url（パディングなし、22 文字）を返す。
func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// randomSeed は 0 以上 2^32 未満の乱数を返す（seed を省略したとき）。
func randomSeed() (uint64, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return uint64(binary.BigEndian.Uint32(b[:])), nil
}
