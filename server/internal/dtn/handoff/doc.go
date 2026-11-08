// Package handoff は、島と船の間の手紙の所有権の引き渡し（Handoff）の純粋な状態機械（DTN-08 #165）。
//
// 契約は docs/dtn/handoff-and-contact.md（§1 用語・§3.1 段階・§3.2 ゲームの操作との対応・§3.3 止まったときの
// 回復）と docs/dtn/persistence-and-recovery.md §4.2（Coordinator の復元）。
//
// 中身:
//   - Stage（Proposed → Persisted → Committed → Activated → Collected、と Proposed・Persisted からの Aborted）
//   - Place（論理的な持ち主。island:<id>/port・island:<id>/relay・ferry:<ferryId>/cargo・delivered・dropped）
//   - Transfer（1 回の引き渡し。ID は x<eventSeq>）
//   - Ledger（Coordinator の所有権の台帳。Commit は CAS）
//   - Resume（Coordinator の復元で、止まった transfer の次の一手を §3.3 の表で返す。呼ぶのは DTN-10）
//
// このパッケージは決定的（forbidigo の対象）。実時間・goroutine・チャネル・I/O・浮動小数点を使わず、map の
// 走査の順に結果を依存させない（一覧は transfer の番号の順に並べ直して返す）。
//
// Coordinator（DTN-05）からの使い方（handoff-and-contact.md §3.4）:
//
//  1. 今の Transport と同じ確認をメタデータで行い、通ったら journal に intent を書いて Propose
//  2. 送り元の Host に transfer.prepare（eventSeq は transferId の番号）。ACK を受けたら Persisted → Commit →
//     journal に committed
//  3. 受け取る Host に transfer.activate → Activated、送り元の Host に transfer.gc → Collected
//  4. 復元では journal を再生して同じ呼び出しを繰り返し、Open の transfer ごとに両方の Host の transfer.status を
//     照会して Resume の一手を行う
//
// Ledger は Create（letter.created）・Move（港の規則での破棄など、Handoff の外の持ち主の変化）で、Handoff を
// 通らない持ち主の変化も記録する。
package handoff
