package island

import (
	"encoding/json"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/islandcore"
	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// 状態の要約（stateDigest・inspect）。フィールドの名前は正準の JSON のキー（並びは RFC 8785 が決める）。

type hardStateView struct {
	Term   uint64 `json:"term"`
	Vote   uint64 `json:"vote"`
	Commit uint64 `json:"commit"`
}

type ledgerResourceView struct {
	ID      string   `json:"id"`
	Holders []string `json:"holders"`
}

type ledgerRecordView struct {
	ReqID    string `json:"reqId"`
	Index    uint64 `json:"index"`
	Origin   uint64 `json:"origin"`
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Result   string `json:"result"`
	Reason   string `json:"reason"`
}

type ledgerView struct {
	AppliedIndex uint64               `json:"appliedIndex"`
	Resources    []ledgerResourceView `json:"resources"`
	Records      []ledgerRecordView   `json:"records"`
}

type eventView struct {
	At    int64  `json:"at"`
	Class uint8  `json:"class"`
	Kind  uint8  `json:"kind"`  // 1 = RaftTick・2 = RequestSpawn・3 = RequestRetry
	ReqID string `json:"reqId"` // RaftTick は ""
	Gen   uint64 `json:"gen"`
}

type waitView struct {
	ReqID string `json:"reqId"`
	Hops  int    `json:"hops"`
}

type requestView struct {
	ID       string `json:"id"`
	Origin   uint64 `json:"origin"`
	Created  bool   `json:"created"`
	Resolved bool   `json:"resolved"`
	Gen      uint64 `json:"gen"`
}

type noteView struct {
	ReqID    string `json:"reqId"`
	Created  bool   `json:"created"`
	Resolved bool   `json:"resolved"`
}

// digestView は stateDigest の元（persistence-and-recovery.md §4.1 の復元の照合に使う）。
type digestView struct {
	ID            uint64           `json:"id"`
	Horizon       dtnproto.Mark    `json:"horizon"`
	Now           int64            `json:"now"`
	HardState     hardStateView    `json:"hardState"`
	Applied       uint64           `json:"applied"`
	LastIndex     uint64           `json:"lastIndex"`
	Ledger        ledgerView       `json:"ledger"`
	Port          []ferry.LetterID `json:"port"`
	Relay         []ferry.LetterID `json:"relay"`
	BundleDigests []string         `json:"bundleDigests"` // 港と倉庫の手紙の ID の昇順
	CreatedAt     []int64          `json:"createdAt"`     // bundleDigests と同じ順
	Delivered     []ferry.LetterID `json:"delivered"`
	NextLetterN   uint64           `json:"nextLetterN"`
	Queue         []eventView      `json:"queue"`
	Waits         []waitView       `json:"waits"`
	Requests      []requestView    `json:"requests"`
	Notes         []noteView       `json:"notes"`
	ReportedTerm  uint64           `json:"reportedTerm"`
	LastState     sim.NodeState    `json:"lastState"`
	ContactAt     int64            `json:"contactAt"`
}

func (a *Actor) ledgerView() ledgerView {
	s := a.core.Ledger().Snapshot()
	v := ledgerView{AppliedIndex: s.AppliedIndex, Resources: make([]ledgerResourceView, len(s.Resources)), Records: make([]ledgerRecordView, len(s.Records))}
	for i, r := range s.Resources {
		v.Resources[i] = ledgerResourceView{ID: r.ID, Holders: r.Holders}
	}
	for i, r := range s.Records {
		v.Records[i] = ledgerRecordView{ReqID: r.Result.ReqID, Index: r.Index, Origin: r.Origin, Kind: r.Kind,
			Resource: r.Resource, Result: r.Result.Status, Reason: r.Result.Reason}
	}
	return v
}

// heldIDs は港と倉庫の手紙の ID の昇順。
func (a *Actor) heldIDs() []ferry.LetterID {
	p := a.core.Port()
	port, relay := p.Port(), p.Relay()
	out := make([]ferry.LetterID, 0, len(port)+len(relay))
	i, j := 0, 0
	for i < len(port) || j < len(relay) {
		if j == len(relay) || (i < len(port) && port[i] < relay[j]) {
			out = append(out, port[i])
			i++
		} else {
			out = append(out, relay[j])
			j++
		}
	}
	return out
}

func (a *Actor) digestView() digestView {
	c := a.core
	st := c.Node().Status()
	reqs := c.Requests()
	h := c.Horizon()
	last := c.LastState()
	v := digestView{
		ID:           a.id,
		Horizon:      dtnproto.Mark{T: h.T, C: uint8(h.Class)},
		Now:          c.Now(),
		HardState:    hardStateView{Term: st.Term, Vote: st.Vote, Commit: st.Commit},
		Applied:      st.Applied,
		LastIndex:    st.LastIndex,
		Ledger:       a.ledgerView(),
		Port:         c.Port().Port(),
		Relay:        c.Port().Relay(),
		Delivered:    append([]ferry.LetterID{}, a.delivered...),
		NextLetterN:  c.LetterCount() + 1,
		Queue:        []eventView{},
		Waits:        []waitView{},
		Requests:     make([]requestView, len(reqs)),
		Notes:        make([]noteView, len(a.notes)),
		ReportedTerm: c.ReportedTerm(),
		LastState: sim.NodeState{ID: a.id, Role: sim.RoleName(last.Role), Term: last.Term, Vote: last.Vote, Lead: last.Lead,
			Commit: last.Commit, Applied: last.Applied, LastIndex: last.LastIndex},
		ContactAt: c.ContactAt(),
	}
	ids := a.heldIDs()
	v.BundleDigests, v.CreatedAt = make([]string, len(ids)), make([]int64, len(ids))
	for i, id := range ids {
		hd := a.bundles[id]
		v.BundleDigests[i], v.CreatedAt[i] = hd.digest, hd.createdAt
	}
	for _, ev := range c.Events() {
		ev := eventView{At: ev.At, Class: uint8(ev.Class), Kind: uint8(ev.Kind), Gen: ev.Gen, ReqID: reqIDOf(reqs, ev)}
		v.Queue = append(v.Queue, ev)
	}
	for _, w := range c.Waits() {
		v.Waits = append(v.Waits, waitView{ReqID: reqs[w.Req].Request.ID, Hops: w.Hops})
	}
	for i, r := range reqs {
		v.Requests[i] = requestView{ID: r.Request.ID, Origin: r.Request.Origin, Created: r.Created, Resolved: r.Resolved, Gen: r.Gen}
	}
	for i, n := range a.notes {
		v.Notes[i] = noteView{ReqID: n.reqID, Created: n.created, Resolved: n.resolved}
	}
	return v
}

func reqIDOf(reqs []islandcore.RequestInfo, ev islandcore.Event) string {
	if ev.Kind == islandcore.KindRaftTick {
		return ""
	}
	return reqs[ev.Req].Request.ID
}

// StateDigest は digestView（actor-protocol.md §7.6）の RFC 8785 の正準の JSON の SHA-256。init の前は ""。
func (a *Actor) StateDigest() string {
	if !a.inited {
		return ""
	}
	d, err := dtnproto.CanonicalDigest(a.digestView())
	if err != nil {
		panic("island: state digest: " + err.Error()) // 整数は 2^53 − 1 以下（手紙の ID は島 × 10^9 + n）
	}
	return d
}

// Cursor は今の上限と stateDigest を返す。
func (a *Actor) Cursor() dtnproto.Cursor {
	if !a.inited {
		return dtnproto.Cursor{}
	}
	h := a.core.Horizon()
	return dtnproto.Cursor{Horizon: dtnproto.Mark{T: h.T, C: uint8(h.Class)}, StateDigest: a.StateDigest()}
}

// inspectLetter は inspect の港・倉庫の手紙 1 通。
type inspectLetter struct {
	LetterID  ferry.LetterID `json:"letterId"`
	CreatedAt int64          `json:"createdAt"`
	Digest    string         `json:"digest"`
	Size      int            `json:"size"`
}

// inspectView は inspect の data。
type inspectView struct {
	ID          uint64          `json:"id"`
	Horizon     dtnproto.Mark   `json:"horizon"`
	Now         int64           `json:"now"`
	Role        string          `json:"role"`
	Term        uint64          `json:"term"`
	Vote        uint64          `json:"vote"`
	Lead        uint64          `json:"lead"`
	Commit      uint64          `json:"commit"`
	Applied     uint64          `json:"applied"`
	LastIndex   uint64          `json:"lastIndex"`
	Port        []inspectLetter `json:"port"`
	Relay       []inspectLetter `json:"relay"`
	NextLetterN uint64          `json:"nextLetterN"`
	Delivered   int             `json:"delivered"`
	Ledger      ledgerView      `json:"ledger"`
	Pending     int             `json:"pending"` // 待ち行列のイベントの数
	Waits       []waitView      `json:"waits"`
	StateDigest string          `json:"stateDigest"`
}

func (a *Actor) inspectLetters(ids []ferry.LetterID) []inspectLetter {
	out := make([]inspectLetter, len(ids))
	for i, id := range ids {
		h := a.bundles[id]
		out[i] = inspectLetter{LetterID: id, CreatedAt: h.createdAt, Digest: h.digest, Size: len(h.bundle)}
	}
	return out
}

// Inspect は Raft の状態（Term・Vote・Commit・Applied・LastIndex・Role・Lead）・港と倉庫の手紙・上限・次の手紙の
// 連番・配達した数・台帳の要約・stateDigest を返す（状態は変えない）。
func (a *Actor) Inspect() (json.RawMessage, error) {
	if !a.inited {
		return json.RawMessage(`{"ready":false}`), nil
	}
	c := a.core
	st := c.Node().Status()
	h := c.Horizon()
	dv := a.digestView()
	v := inspectView{
		ID: a.id, Horizon: dtnproto.Mark{T: h.T, C: uint8(h.Class)}, Now: c.Now(),
		Role: sim.RoleName(st.Role), Term: st.Term, Vote: st.Vote, Lead: st.Lead, Commit: st.Commit,
		Applied: st.Applied, LastIndex: st.LastIndex,
		Port: a.inspectLetters(c.Port().Port()), Relay: a.inspectLetters(c.Port().Relay()),
		NextLetterN: c.LetterCount() + 1, Delivered: len(a.delivered), Ledger: dv.Ledger,
		Pending: c.Pending(), Waits: dv.Waits, StateDigest: a.StateDigest(),
	}
	return json.Marshal(v)
}
