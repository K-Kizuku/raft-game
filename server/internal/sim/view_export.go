package sim

import (
	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// 分散版の島の Actor（DTN-03 #160）が、出力の data を参照モデルのドメインイベントと同じ形で作るための公開の関数。
// 中身は参照モデルが使うものと同じ（振る舞いは変えない）。

// NewLetterView は、港に置いた時点の手紙 l の表示用の値（letter.created の letter）を作る。
// senderTerm は送信元のノードの今の Term（raft.stale の判定に使う）。
func NewLetterView(l ferry.Letter, senderTerm uint64) LetterView { return newLetterView(l, senderTerm) }

// RoleName は Raft の役割の名前（node.state の role）を返す。
func RoleName(r raft.StateType) string { return roleString(r) }
