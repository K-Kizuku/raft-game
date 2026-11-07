package ferry

import pb "go.etcd.io/raft/v3/raftpb"

// LetterID は手紙の ID。1 から振る連番（Transport の中で決定的）。
type LetterID uint64

// LetterKind は手紙の種類。
type LetterKind uint8

const (
	LetterRaft    LetterKind = iota + 1 // Raft のメッセージ
	LetterRequest                       // 依頼書（アプリケーションの手紙）
)

// RequestNote は依頼書の中身。ferry は ReqID だけを見る（R6）。
type RequestNote struct {
	ReqID string
	Hops  int // 転送された回数（プロトコル §5.2 の hops）。ferry は見ない
}

// LocationKind は手紙のある場所の種類。
type LocationKind uint8

const (
	LocPort      LocationKind = iota + 1 // 港（Island = その港の島 = 送信元）
	LocFerry                             // 船（Ferry）
	LocDelivered                         // 配達済み（Island = 宛先）
	LocDropped                           // 港の規則で破棄した
	LocRelay                             // 中継倉庫（Island = 倉庫の島。送信元・宛先のどちらでもない）
)

// Location は手紙のある場所。
type Location struct {
	Kind   LocationKind
	Island uint64
	Ferry  FerryID
}

// Letter は手紙 1 通。
type Letter struct {
	ID        LetterID
	Kind      LetterKind
	From, To  uint64 // 島（= ノード）ID
	CreatedAt int64
	Raft      *pb.Message  // Kind == LetterRaft のとき。受け取ったポインタをそのまま持つ。読み取り専用
	Req       *RequestNote // Kind == LetterRequest のとき
	Location  Location
}

// DropReason は手紙を破棄した理由。
type DropReason string

const (
	DropSuperseded DropReason = "superseded"
	DropOverflow   DropReason = "overflow"
)

// Drop は港の規則で破棄した手紙 1 通。起きた順に返す。
type Drop struct {
	Letter LetterID
	Reason DropReason
	Rule   string // "R1"〜"R6"
}
