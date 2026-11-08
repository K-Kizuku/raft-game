package coordcore

// SimTime はゲーム内時間（ミリ秒）。0 = 航行開始（§3.1）。
//
// 単位の定数はゲームの数値ではないのでコードに置く。tickMs などの値はステージ定義から来る。
type SimTime int64

const (
	// Millisecond はゲーム内時間の 1 ミリ秒。
	Millisecond SimTime = 1
	// Second はゲーム内時間の 1 秒。
	Second SimTime = 1000 * Millisecond
)
