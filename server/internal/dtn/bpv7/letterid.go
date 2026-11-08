package bpv7

// letterIDBase は letterId の島の部分の重み（simulation-time.md §5.1: islandId × 10^9 + n）。
const letterIDBase uint64 = 1_000_000_000

// 作成時刻の連番 n の範囲（1..999,999,999）。
const (
	MinSeq uint64 = 1
	MaxSeq uint64 = letterIDBase - 1
)

// LetterID は、Bundle の送信元の EID と作成時刻の連番（= bundleId）から、ゲームの手紙の ID
// （letterId = src.Node × 10^9 + seq）を計算する。src が島の窓口でなければ bpv7_eid、
// seq が範囲外なら bpv7_timestamp。transferId とは関係がない（handoff-and-contact.md §1）。
func LetterID(src EID, seq uint64) (uint64, error) {
	if !src.isIsland() {
		return 0, errorf(CodeEID, "source %s is not an island endpoint", src)
	}
	if seq < MinSeq || seq > MaxSeq {
		return 0, errorf(CodeTimestamp, "sequence %d out of range", seq)
	}
	return src.Node*letterIDBase + seq, nil
}

// SplitLetterID は letterId を島のノード番号と連番に分ける。LetterID の逆。
func SplitLetterID(id uint64) (node, seq uint64, err error) {
	node, seq = id/letterIDBase, id%letterIDBase
	if node < MinIslandNode || node > MaxIslandNode {
		return 0, 0, errorf(CodeEID, "letter %d: island %d out of range", id, node)
	}
	if seq < MinSeq {
		return 0, 0, errorf(CodeTimestamp, "letter %d: sequence 0", id)
	}
	return node, seq, nil
}

// BundleID は Bundle の識別（送信元の EID と作成時刻 [0, seq]）。letterId と 1 対 1。
type BundleID struct {
	Source EID
	Seq    uint64
}

// BundleIDOf は letterId に対応する bundleId を返す。service はサービス番号（1 = Raft、2 = 依頼）。
func BundleIDOf(letterID, service uint64) (BundleID, error) {
	node, seq, err := SplitLetterID(letterID)
	if err != nil {
		return BundleID{}, err
	}
	src := IPN(node, service)
	if !src.isIsland() {
		return BundleID{}, errorf(CodeEID, "service %d is not an island service", service)
	}
	return BundleID{Source: src, Seq: seq}, nil
}

// LetterID は bundleId に対応する letterId を返す。
func (id BundleID) LetterID() (uint64, error) { return LetterID(id.Source, id.Seq) }
