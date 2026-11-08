// Package protocol は、DTN 版の Actor の内部 API の電文（docs/dtn/actor-protocol.md）を、
// Go の型と検証として実装する（DTN-02 #159）。
//
// ブラウザ向けの server/internal/protocol と名前が重なるので、import するときは別名を dtnproto にそろえる。
//
//	import dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
//
// 中身:
//   - エンベロープ（Request・Response・Output・Mark・Cursor・Error）と ActorRef
//   - 種類（Kind）と、種類ごとの宛先の役割・journal に入れるか・payload の型の表（kinds.go）
//   - payload の型と検証（payload.go）、電文全体の検証 Validate（validate.go）
//   - エラーコード（codes.go）と上限（limits.go）
//   - RFC 8785 の正準の JSON と digest（canonical.go）。この電文の数は整数だけなので、数の規則は整数の部分だけを持つ
//   - 手紙と Bundle の bytes の変換の interface LetterCodec と、試験用の FakeCodec（codec.go）
//
// testdata/requests と testdata/canonical の fixture は、TS 側（DTN-07・12）の契約テストでも同じファイルを読む。
package protocol
