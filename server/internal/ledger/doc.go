// Package ledger は、各島のノードが持つ「共有台帳」（Raft の状態機械の複製）を実装する
// （DesignDoc §7）。
//
// Commit されたエントリの Data（JSON のコマンド、§7.1）を index の順に Apply し、
// 花火の予約（1 つしかない資源）と屋台の出店枠（定員つき）を判定する。同じ reqId の
// 2 回目以降の適用は duplicate として無視する（FR-REQ-06）。
//
// 台帳は決定的な純粋関数である（§7.2）。同じ資源の定義に同じ (index, data) の列を
// 適用すれば、必ず同じ結果と Snapshot になる（V-3 の前提）。そのため、時刻・goroutine・
// チャネル・乱数・浮動小数点を使わず、map は検索だけに使って走査しない（§9.2）。
//
// このパッケージは標準ライブラリのほかに依存しない（docs/02-architecture.md §3）。
// cluster と game がこのパッケージを import する。Ledger は 1 つの goroutine から使う
// （ADR-0003）。
package ledger
