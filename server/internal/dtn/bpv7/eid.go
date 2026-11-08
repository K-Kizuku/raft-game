package bpv7

// EID の方式（RFC 9171 §4.2.5.1）。
const (
	SchemeDTN uint64 = 1 // dtn。このプロファイルでは dtn:none だけ
	SchemeIPN uint64 = 2 // ipn
)

// サービス番号（bpv7-profile.md §2）。
const (
	ServiceFerry   uint64 = 0 // 船（中継のノード）。Bundle の中には現れない
	ServiceRaft    uint64 = 1 // 島の Raft の窓口
	ServiceRequest uint64 = 2 // 島の依頼の窓口
)

// 島のノード番号の範囲。letterId = node × 10^9 + seq に収めるため（bpv7-profile.md §2）。
const (
	MinIslandNode uint64 = 1
	MaxIslandNode uint64 = 999
)

// ferryNodeBase は船の EID のノード番号の起点（ipn:<1000 + k>.0）。
const ferryNodeBase uint64 = 1000

// EID は Endpoint ID。Scheme が SchemeIPN なら ipn:<Node>.<Service>、
// SchemeDTN なら dtn:none（Node・Service は 0）。
type EID struct {
	Scheme        uint64
	Node, Service uint64
}

// DTNNone は dtn:none（Report-to に使う）。
var DTNNone = EID{Scheme: SchemeDTN}

// IPN は ipn:<node>.<service> を返す。
func IPN(node, service uint64) EID { return EID{Scheme: SchemeIPN, Node: node, Service: service} }

// IslandRaftEID は島の Raft の窓口 ipn:<node>.1。
func IslandRaftEID(node uint64) EID { return IPN(node, ServiceRaft) }

// IslandRequestEID は島の依頼の窓口 ipn:<node>.2。
func IslandRequestEID(node uint64) EID { return IPN(node, ServiceRequest) }

// FerryEID は船の EID ipn:<1000 + k>.0。k はステージの ferries[] の並びでの 1 始まりの番号。
// 船の EID は Bundle の中には現れない（宛先にならない・Previous Node ブロックを作らない）。
func FerryEID(k uint64) EID { return IPN(ferryNodeBase+k, ServiceFerry) }

// IsNone は dtn:none かどうか。
func (e EID) IsNone() bool { return e == DTNNone }

// isIsland は、島の窓口（ipn:<1..999>.<1|2>）かどうか。
func (e EID) isIsland() bool {
	return e.Scheme == SchemeIPN && e.Node >= MinIslandNode && e.Node <= MaxIslandNode &&
		(e.Service == ServiceRaft || e.Service == ServiceRequest)
}

// String は "ipn:3.1" や "dtn:none" の形にする（ログと試験用）。
func (e EID) String() string {
	switch {
	case e.IsNone():
		return "dtn:none"
	case e.Scheme == SchemeIPN:
		return "ipn:" + utoa(e.Node) + "." + utoa(e.Service)
	default:
		return "eid(" + utoa(e.Scheme) + "," + utoa(e.Node) + "," + utoa(e.Service) + ")"
	}
}

func utoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// validateEIDs は、宛先・送信元・Report-to が bpv7-profile.md §2 の形かを確かめる。
// 宛先と送信元は島の窓口で、サービス番号が同じ。Report-to は dtn:none。
func validateEIDs(dest, src, reportTo EID) *Error {
	if !src.isIsland() {
		return errorf(CodeEID, "source %s is not an island endpoint", src)
	}
	if !dest.isIsland() {
		return errorf(CodeEID, "destination %s is not an island endpoint", dest)
	}
	if src.Service != dest.Service {
		return errorf(CodeEID, "service differs: source %s, destination %s", src, dest)
	}
	if !reportTo.IsNone() {
		return errorf(CodeEID, "report-to %s is not dtn:none", reportTo)
	}
	return nil
}
