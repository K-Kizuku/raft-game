package protocol

// Handoff の応答の形（handoff-and-contact.md §3・§3.4。DTN-08 #165）。Actor Host が応答の outputs に入れる。

// 応答の outputs の type（Host が作る。Container は出さない）。
const (
	// OutputTransferAck は transfer.accept・transfer.prepare の応答の outputs の 1 つ。data は TransferAck。
	OutputTransferAck = "transfer.ack"
	// OutputTransferStatus は transfer.status・transfer.abort の応答の outputs の 1 つ。data は TransferStatus。
	OutputTransferStatus = "transfer.status"
)

// TransferAck は受け取る Host の永続 ACK（ACK{transferId, digests}）。Digests は transfer の手紙の順
// （transfer.prepare の letterIds の順）の、保存した bytes の digest。
type TransferAck struct {
	TransferID string   `json:"transferId"`
	Digests    []string `json:"digests"`
}

// Host の transfers の行の状態（transfer.status の state）。行が無いときは TransferNone。
const (
	TransferNone      = "none"
	TransferPrepared  = "prepared"  // 送り元: bytes を prepared にした（ack は受け取る Host の ACK を受けたら入る）
	TransferPending   = "pending"   // 受け取り: bytes を pending で保存した（まだ使えない）
	TransferActivated = "activated" // 受け取り: then を Container に適用した
	TransferCollected = "collected" // 送り元: then を Container に適用し、bytes を消した
	TransferAborted   = "aborted"   // 取り消した（committed の前だけ）
)

// TransferStatus は transfer.status の応答の data。Ack は行に ACK があるときだけ（送り元は ACK を受けた後、
// 受け取りは pending を保存した後）。
type TransferStatus struct {
	TransferID string       `json:"transferId"`
	State      string       `json:"state"`
	Ack        *TransferAck `json:"ack,omitempty"`
}
