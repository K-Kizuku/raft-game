// Package game は、住民の依頼（アプリケーション層、DesignDoc §8）の純粋な型と関数を提供する。
//
// Raft の外の「役場」の判断（Decide）、依頼の状態の判定（Track.Status）、ラウンドの依頼の計画
// （PlanRequests。台本とランダム、データ仕様 §4.2）だけを持つ。状態を持つ部分（依頼の帳簿・発生・
// route・再送・適用の観察）は sim.Engine の中にある（DesignDoc §2 の「game は sim.Engine の中」）。
//
// 依存の向きは stage → sim → game → ledger。このパッケージは sim・cluster・ferry・stage・
// go.etcd.io/raft/v3 を import しない（テストを含む）。
//
// # 決定性の規則（DesignDoc §9.2）
//
// I/O・実時間・ゴルーチン・チャネル・パッケージ全体で共有される乱数・浮動小数点・map の走査を使わない。
// 乱数は呼び出し側が作った *rand.Rand を引数で受け、決まった順に引く（PlanRequests）。
package game
