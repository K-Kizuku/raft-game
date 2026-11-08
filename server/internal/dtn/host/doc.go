// Package host は DTN 版の Actor Host（DTN-07 #164）。Actor ごとに 1 つあり、その Actor の正本
// （journal・Bundle の bytes・Handoff と Contact の行）を持ち、Container（Actor の Runtime）の前に立つ。
//
// 契約は docs/dtn/persistence-and-recovery.md（§2 保存の形・§3 確定の順序・§6 上限と片付け・§7 実装の細部と
// 契約テストの fixture）と docs/dtn/actor-protocol.md（§2 エンベロープ・§5 冪等と順序・§6 エラーと上限）。
//
// 中身:
//   - Host（host.go）: 要求の処理。エンベロープの確認 → 冪等の確認 → journal に intent → Container に送る →
//     1 つのトランザクションで applied（Bundle の保存・advance のまとめ）→ 応答
//   - Store・Tx（store.go）: 保存の境界。Go では sqlitestore（modernc.org/sqlite。1 Actor = 1 ファイル）、
//     Cloudflare では ActorHost DO の ctx.storage.sql（DTN-12。TypeScript）
//   - 行の型（rows.go）: Meta・JournalRow・BundleRow・TransferRow・ContactRow
//   - ActorConn（conn.go）: Container への送信。RuntimeConn は同じプロセスの runtime.Runtime につなぐ
//   - Hooks: 試験用の止める位置（afterIntent・afterActorReply・afterCommit）と、Actor が止まったときの通知
//
// contracttest は testdata/contract/*.json の fixture を Host に流す runner。TS の ActorHost（DTN-12）も
// 同じ fixture を読む。
//
// このパッケージは実時間（Container の応答のタイムアウト）を使うので、決定的なパッケージ（forbidigo の対象）
// ではない。
//
// 後続の Issue の拡張点:
//   - transfer.*（DTN-08）: transfer.go。contact.*（DTN-09）は Host.handleHostOnly で internal（未実装）を返す。
//     Contact の確認（ContactVerifier）と CLA（Peer）は Config で差し替える
//   - 復元（DTN-10）: Host.Restart（epoch を 1 増やして Container をつなぎ替える）・Store.View と Tx.Journals
//     （journal を event_seq の順に読む）・Hooks.OnActorLost
package host
