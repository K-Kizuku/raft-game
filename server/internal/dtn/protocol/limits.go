package protocol

// Limits は電文の上限（actor-protocol.md §6）。
type Limits struct {
	MaxMessageBytes int `json:"maxMessageBytes"` // 1 つの電文（要求・応答）の bytes
	MaxBundleBytes  int `json:"maxBundleBytes"`  // 1 つの Bundle の bytes（base64 にする前）
	MaxLetters      int `json:"maxLetters"`      // 1 つの要求の手紙・手紙の ID の数（deliver・cargo.add など）
	MaxOutputs      int `json:"maxOutputs"`      // 1 つの応答の outputs の数
	MaxJournalRows  int `json:"maxJournalRows"`  // 1 Actor の journal の行（DTN-07 で使う）
	MaxStoredBytes  int `json:"maxStoredBytes"`  // 1 Actor の保存の合計の bytes（DTN-07 で使う）
}

// DefaultLimits は §6 の初期値。
var DefaultLimits = Limits{
	MaxMessageBytes: 1 << 20,
	MaxBundleBytes:  16 << 10,
	MaxLetters:      16,
	MaxOutputs:      4096,
	MaxJournalRows:  20000,
	MaxStoredBytes:  8 << 20,
}
