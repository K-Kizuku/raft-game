package server

import (
	"net/http"
)

// serveSingle は single モードの振り分け。パスの完全一致 → メソッド → ルートごとの処理。
// Origin は検査しない（コンテナは外部に公開せず、Durable Object だけがつなぐ。DesignDoc §16）。
func (s *Server) serveSingle(w http.ResponseWriter, r *http.Request) {
	switch requestPath(r) {
	case "/healthz":
		s.serveHealthz(w, r)
	case "/ws":
		if !allowMethod(w, r, http.MethodGet) || !requireUpgrade(w, r) {
			return
		}
		s.single.serveWS(w, r)
	default:
		notFound(w)
	}
}
