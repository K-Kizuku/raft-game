package handoff

import (
	"regexp"
	"strconv"
	"strings"
)

// Place は手紙の論理的な持ち主（handoff-and-contact.md §1）。常に 1 つ。
//
//   - island:<id>/port（港）・island:<id>/relay（倉庫）。<id> は Raft のノード ID（1 以上、先頭に 0 を付けない）
//   - ferry:<ferryId>/cargo。<ferryId> はステージ定義の ferries[].id の文字列（例: ferry:f1/cargo）
//   - delivered（宛先の島に配達した）・dropped（港の規則などで捨てた）
type Place string

// 持ち主のうち、Actor に属さないもの。
const (
	Delivered Place = "delivered"
	Dropped   Place = "dropped"
)

var (
	islandPlaceRe = regexp.MustCompile(`^island:([1-9][0-9]{0,15})/(port|relay)$`)
	ferryPlaceRe  = regexp.MustCompile(`^ferry:([a-z0-9][a-z0-9-]{0,63})/cargo$`) // dtn/protocol の ferryIDRe と同じ
)

// IslandPort は島 id の港。
func IslandPort(id uint64) Place { return Place("island:" + strconv.FormatUint(id, 10) + "/port") }

// IslandRelay は島 id の倉庫。
func IslandRelay(id uint64) Place { return Place("island:" + strconv.FormatUint(id, 10) + "/relay") }

// FerryCargo は船 id（ステージ定義の ferries[].id）の Cargo。
func FerryCargo(id string) Place { return Place("ferry:" + id + "/cargo") }

// Valid は p が Place の形か。
func (p Place) Valid() bool {
	return p == Delivered || p == Dropped || islandPlaceRe.MatchString(string(p)) || ferryPlaceRe.MatchString(string(p))
}

// Actor は p を持つ Actor（dtn/protocol の ActorRef の文字列。island:2・ferry:f1）。delivered・dropped と
// 不正な Place は ""。
func (p Place) Actor() string {
	if !islandPlaceRe.MatchString(string(p)) && !ferryPlaceRe.MatchString(string(p)) {
		return ""
	}
	a, _, _ := strings.Cut(string(p), "/")
	return a
}

// Slot は Actor の中の置き場所（port・relay・cargo）。Actor に属さない Place は ""。
func (p Place) Slot() string {
	if p.Actor() == "" {
		return ""
	}
	_, s, _ := strings.Cut(string(p), "/")
	return s
}

// IsIsland は p が島の港か倉庫か。
func (p Place) IsIsland() bool { return islandPlaceRe.MatchString(string(p)) }

// IsFerry は p が船の Cargo か。
func (p Place) IsFerry() bool { return ferryPlaceRe.MatchString(string(p)) }

// TransferID は eventSeq（transfer.prepare を送る dispatch の番号）の transferId（x<eventSeq>）。
func TransferID(eventSeq uint64) string { return "x" + strconv.FormatUint(eventSeq, 10) }

// ParseTransferID は transferId の eventSeq を返す。x<eventSeq>（eventSeq は 1 以上、先頭に 0 を付けない）の形で
// なければ false。
func ParseTransferID(id string) (uint64, bool) {
	s, ok := strings.CutPrefix(id, "x")
	if !ok || s == "" || s[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
