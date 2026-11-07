// Package server は Go のゲームサーバー（server/cmd/server）の本体（DesignDoc §12.1・§12.4、T-302）。
//
// 同じバイナリを 2 つのモードで動かす。
//
//   - single: 本番。コンテナの中で 1 プロセス＝1 ゲーム。Durable Object からの WebSocket（/ws）と
//     /healthz だけを受け、内部の電文（internal.init・internal.restore・internal.result・internal.ping）を扱う
//   - standalone: ローカル開発（make dev）とデモの保険。1 プロセスで複数のゲームを持ち、
//     公開 API（プロトコル §2）を自分で提供する。署名・Turnstile・レート制限はしない
//
// 構成:
//
//	Server                 HTTP の振り分け（single / standalone）
//	  host                 single: 1 つの枠 / standalone: セッションの一覧
//	    session            1 ゲーム。Engine を持つ goroutine 1 つ（session.run）
//	      wsConn           いまつながっている WebSocket 1 本（読み取り・書き込みの goroutine）
//
// Engine に触れるのは、そのセッションの goroutine（session.run）だけ。例外は goroutine を始める前に
// Engine を作る処理（sim.New・sim.Replay）だけ。ゲームの結果（Leader・Commit・依頼の成否）はここで決めず、
// Engine の公開 API の結果をそのまま送る。
//
// このパッケージは sim の外なので、goroutine・チャネル・time・crypto/rand を使ってよい。
package server
