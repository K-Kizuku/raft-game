package sim

// EvLetterStale は letter.stale の種類。
const EvLetterStale EventType = "letter.stale"

// LetterStale は letter.stale（プロトコル §6）。送信元の Term が上がり、手紙が古くなった。
type LetterStale struct {
	LetterID uint64 `json:"letterId"`
}

func (LetterStale) EventType() EventType { return EvLetterStale }

// 古い手紙の検出は coordcore.Core.StaleLetters（島の出力 term.changed を受けて、手紙の写しの Term で判定する）。
// Engine.drain が、出した順に letter.stale にする（DTN-00 #363）。
