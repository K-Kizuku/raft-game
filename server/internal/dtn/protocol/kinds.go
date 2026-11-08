package protocol

// Kind は要求の種類（actor-protocol.md §3）。
type Kind string

// §3.1 Actor（Container）が受ける種類。
const (
	KindInit         Kind = "init"
	KindAdvance      Kind = "advance"
	KindDeliver      Kind = "deliver"
	KindPortRemove   Kind = "port.remove"
	KindUnload       Kind = "unload"
	KindRelayRemove  Kind = "relay.remove"
	KindArrive       Kind = "arrive"
	KindDepart       Kind = "depart"
	KindCargoAdd     Kind = "cargo.add"
	KindCargoRemove  Kind = "cargo.remove"
	KindBundleExport Kind = "bundle.export"
	KindInspect      Kind = "inspect"
	KindShutdown     Kind = "shutdown"
)

// §3.2 Actor Host が受ける種類（Container には送らない）。処理は DTN-07〜09。
const (
	KindContactOpen      Kind = "contact.open"
	KindContactClose     Kind = "contact.close"
	KindTransferPrepare  Kind = "transfer.prepare"
	KindTransferAccept   Kind = "transfer.accept"
	KindTransferActivate Kind = "transfer.activate"
	KindTransferGC       Kind = "transfer.gc"
	KindTransferAbort    Kind = "transfer.abort"
	KindTransferStatus   Kind = "transfer.status"
	KindHostStatus       Kind = "host.status"
)

// KindInfo は種類ごとの決まり。
type KindInfo struct {
	Kind Kind
	// Targets は宛先にできる役割（Role のビットの和）。合わない宛先の要求は invalid。
	Targets Role
	// HostOnly は Actor Host だけが受ける種類（§3.2）。Runtime（Container）に届いたら invalid。
	HostOnly bool
	// Journaled は journal に入れる（状態を変える）種類（persistence-and-recovery.md §2）。
	// 入れない種類は Runtime の eventSeq を進めない。
	Journaled bool

	newPayload func() payload
}

// roleActors は §3.1・§3.2 の「すべて」。島と船（Actor Host の後ろの Actor）。
// Coordinator が受ける種類（§3.3）と、Coordinator に送る init・inspect・shutdown は DTN-05 がこの表に足す。
const roleActors = RoleIsland | RoleFerry

// kindTable は種類の表（1 か所にまとめる）。
var kindTable = []KindInfo{
	// §3.1
	{KindInit, roleActors, false, true, func() payload { return new(InitPayload) }},
	{KindAdvance, RoleIsland, false, true, func() payload { return new(AdvancePayload) }},
	{KindDeliver, RoleIsland, false, true, func() payload { return new(LettersPayload) }},
	{KindPortRemove, RoleIsland, false, true, func() payload { return new(LetterIDsPayload) }},
	{KindUnload, RoleIsland, false, true, func() payload { return new(UnloadPayload) }},
	{KindRelayRemove, RoleIsland, false, true, func() payload { return new(LetterIDsPayload) }},
	{KindArrive, RoleFerry, false, true, func() payload { return new(ArrivePayload) }},
	{KindDepart, RoleFerry, false, true, func() payload { return new(DepartPayload) }},
	{KindCargoAdd, RoleFerry, false, true, func() payload { return new(LettersPayload) }},
	{KindCargoRemove, RoleFerry, false, true, func() payload { return new(LetterIDsPayload) }},
	{KindBundleExport, roleActors, false, false, func() payload { return new(ExportPayload) }},
	{KindInspect, roleActors, false, false, func() payload { return new(EmptyPayload) }},
	{KindShutdown, roleActors, false, true, func() payload { return new(EmptyPayload) }},
	// §3.2
	{KindContactOpen, roleActors, true, true, func() payload { return new(ContactPayload) }},
	{KindContactClose, roleActors, true, true, func() payload { return new(ContactPayload) }},
	{KindTransferPrepare, roleActors, true, true, func() payload { return new(TransferPreparePayload) }},
	{KindTransferAccept, roleActors, true, true, func() payload { return new(TransferAcceptPayload) }},
	{KindTransferActivate, roleActors, true, true, func() payload { return new(TransferThenPayload) }},
	{KindTransferGC, roleActors, true, true, func() payload { return new(TransferThenPayload) }},
	{KindTransferAbort, roleActors, true, true, func() payload { return new(TransferIDPayload) }},
	{KindTransferStatus, roleActors, true, false, func() payload { return new(TransferIDPayload) }},
	{KindHostStatus, roleActors, true, false, func() payload { return new(EmptyPayload) }},
}

// LookupKind は k の決まりを返す。未知の種類なら false。
func LookupKind(k Kind) (KindInfo, bool) {
	for _, ki := range kindTable {
		if ki.Kind == k {
			return ki, true
		}
	}
	return KindInfo{}, false
}

// Kinds は表の順の種類の一覧。
func Kinds() []Kind {
	out := make([]Kind, len(kindTable))
	for i, ki := range kindTable {
		out[i] = ki.Kind
	}
	return out
}

// Journaled は k が journal に入れる種類か（未知の種類は false）。
func (k Kind) Journaled() bool {
	ki, ok := LookupKind(k)
	return ok && ki.Journaled
}

// Accepts は role の Actor が k を受けるか。
func (ki KindInfo) Accepts(role Role) bool { return role != 0 && ki.Targets&role != 0 }
