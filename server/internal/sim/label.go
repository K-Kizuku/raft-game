package sim

import (
	"github.com/K-Kizuku/raft-game/server/internal/coordcore"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// letterLabel は手紙のゲーム上の区分（プロトコル §5.2 の label）を返す（coordcore.LetterLabel）。
func letterLabel(l ferry.Letter) string { return coordcore.LetterLabel(l) }
