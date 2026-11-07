package ledger

import (
	"fmt"
	"slices"
)

// ResourceKind は資源の種類（データ仕様 §5.2 の resources[].kind）。
type ResourceKind string

const (
	ResourceFirework ResourceKind = "firework" // 1 件だけ予約できる
	ResourceStall    ResourceKind = "stall"    // Capacity 件まで予約できる
)

// ResourceDef は 1 つの資源の定義。値はステージ定義（T-201）から渡す。このパッケージに既定値を持たない。
type ResourceDef struct {
	ID       string
	Kind     ResourceKind
	Capacity int // stall の定員。firework では 0
}

// 適用の結果の状態（§7.2）。
const (
	StatusOK        = "ok"
	StatusRejected  = "rejected"
	StatusDuplicate = "duplicate"
)

// rejected の理由。
const (
	ReasonAlreadyReserved      = "already_reserved"       // 花火がすでに予約されている
	ReasonFull                 = "full"                   // 屋台の定員に達している
	ReasonUnknownResource      = "unknown_resource"       // resource が定義にない
	ReasonResourceKindMismatch = "resource_kind_mismatch" // kind と資源の種類が合わない（firework.reserve で stall を指すなど）
	ReasonUnknownKind          = "unknown_kind"           // kind が未知（budget.spend を含む）
	ReasonUnsupportedVersion   = "unsupported_version"    // v が Version でない
	ReasonInvalidCommand       = "invalid_command"        // Decode できない・reqId が空・origin が 0
)

// Result は 1 つのエントリの適用の結果（§7.2）。
type Result struct {
	ReqID  string // Decode できないときと reqId が空のときは ""
	Status string // StatusOK・StatusRejected・StatusDuplicate
	Reason string // StatusRejected のときだけ。ほかは ""
}

// Record は reqId ごとに記録した、最初の適用。
type Record struct {
	Index    uint64 // 最初に適用したエントリの index
	Origin   uint64
	Kind     string
	Resource string
	Result   Result // StatusOK か StatusRejected（StatusDuplicate は記録しない）
}

// ResourceState は 1 つの資源の状態。
type ResourceState struct {
	ID       string
	Kind     ResourceKind
	Capacity int
	Holders  []string // 予約が成功した reqId（適用の順）。nil にしない
}

// Snapshot は台帳の状態の複製。2 つの台帳の比較（V-3・I-6）に reflect.DeepEqual で使う。
type Snapshot struct {
	AppliedIndex uint64
	Resources    []ResourceState // New に渡した定義の順。nil にしない
	Records      []Record        // Index の昇順（= 適用の順）。nil にしない
}

// Ledger は 1 ノード分の台帳。1 つの goroutine から使う（ADR-0003）。
type Ledger struct {
	resources    []ResourceState   // 定義の順
	resourceIdx  map[string]int    // 資源の ID → resources の添字（検索だけに使う）
	records      map[string]Record // reqId → 最初の適用の記録（検索だけに使う）
	order        []string          // 記録した reqId（記録の順）
	appliedIndex uint64
}

// New は資源の定義から空の台帳を作る。定義が不正（ID が空・重複・未知の種類・
// firework に Capacity がある・stall の Capacity が 1 未満）ならエラーを返す。
func New(defs []ResourceDef) (*Ledger, error) {
	l := &Ledger{
		resources:   make([]ResourceState, 0, len(defs)),
		resourceIdx: make(map[string]int, len(defs)),
		records:     make(map[string]Record),
		order:       []string{},
	}
	for i, d := range defs {
		if d.ID == "" {
			return nil, fmt.Errorf("ledger: resources[%d]: id is empty", i)
		}
		if _, dup := l.resourceIdx[d.ID]; dup {
			return nil, fmt.Errorf("ledger: resources[%d]: duplicate id %q", i, d.ID)
		}
		switch d.Kind {
		case ResourceFirework:
			if d.Capacity != 0 {
				return nil, fmt.Errorf("ledger: resources[%d] (%s): firework must not have capacity, got %d", i, d.ID, d.Capacity)
			}
		case ResourceStall:
			if d.Capacity < 1 {
				return nil, fmt.Errorf("ledger: resources[%d] (%s): stall capacity must be >= 1, got %d", i, d.ID, d.Capacity)
			}
		default:
			return nil, fmt.Errorf("ledger: resources[%d] (%s): unknown kind %q", i, d.ID, d.Kind)
		}
		l.resourceIdx[d.ID] = len(l.resources)
		l.resources = append(l.resources, ResourceState{
			ID:       d.ID,
			Kind:     d.Kind,
			Capacity: d.Capacity,
			Holders:  []string{},
		})
	}
	return l, nil
}

// Apply は Commit された index のエントリの data を適用し、結果を返す（§7.2）。
// index が AppliedIndex 以下（0 を含む）のときはプログラムの誤りとして panic し、状態を変えない。
func (l *Ledger) Apply(index uint64, data []byte) Result {
	if index <= l.appliedIndex {
		panic(fmt.Sprintf("ledger: Apply index %d must be greater than applied index %d", index, l.appliedIndex))
	}
	l.appliedIndex = index

	c, err := Decode(data)
	if err != nil || c.ReqID == "" {
		return Result{Status: StatusRejected, Reason: ReasonInvalidCommand}
	}
	if _, ok := l.records[c.ReqID]; ok {
		return Result{ReqID: c.ReqID, Status: StatusDuplicate}
	}

	res := l.judge(c)
	l.records[c.ReqID] = Record{
		Index:    index,
		Origin:   c.Origin,
		Kind:     c.Kind,
		Resource: c.Resource,
		Result:   res,
	}
	l.order = append(l.order, c.ReqID)
	return res
}

// judge はコマンドを検査し、成功なら資源を消費して結果を返す。
func (l *Ledger) judge(c Command) Result {
	reject := func(reason string) Result {
		return Result{ReqID: c.ReqID, Status: StatusRejected, Reason: reason}
	}
	if c.V != Version {
		return reject(ReasonUnsupportedVersion)
	}
	if c.Origin == 0 {
		return reject(ReasonInvalidCommand)
	}
	var want ResourceKind
	switch c.Kind {
	case KindFireworkReserve:
		want = ResourceFirework
	case KindStallReserve:
		want = ResourceStall
	default:
		return reject(ReasonUnknownKind)
	}
	i, ok := l.resourceIdx[c.Resource]
	if !ok {
		return reject(ReasonUnknownResource)
	}
	r := &l.resources[i]
	if r.Kind != want {
		return reject(ReasonResourceKindMismatch)
	}
	switch r.Kind {
	case ResourceFirework:
		if len(r.Holders) != 0 {
			return reject(ReasonAlreadyReserved)
		}
	case ResourceStall:
		if len(r.Holders) >= r.Capacity {
			return reject(ReasonFull)
		}
	}
	r.Holders = append(r.Holders, c.ReqID)
	return Result{ReqID: c.ReqID, Status: StatusOK}
}

// AppliedIndex は最後に Apply した index（初期値 0）を返す。
func (l *Ledger) AppliedIndex() uint64 {
	return l.appliedIndex
}

// Lookup は reqId の記録（最初の適用の index と結果）を返す。記録がなければ false。
func (l *Ledger) Lookup(reqID string) (Record, bool) {
	r, ok := l.records[reqID]
	return r, ok
}

// Snapshot は台帳の状態の深い複製を返す。スライスは空でも nil にしない。
func (l *Ledger) Snapshot() Snapshot {
	s := Snapshot{
		AppliedIndex: l.appliedIndex,
		Resources:    make([]ResourceState, len(l.resources)),
		Records:      make([]Record, len(l.order)),
	}
	for i, r := range l.resources {
		r.Holders = append([]string{}, r.Holders...)
		s.Resources[i] = r
	}
	for i, id := range l.order {
		s.Records[i] = l.records[id]
	}
	return s
}

// CheckInvariants は 1 つの台帳で見られる不変条件（§7.3）を検査し、
// 最初に見つけた違反を説明つきのエラーで返す。違反がなければ nil。
func (l *Ledger) CheckInvariants() error {
	holderOf := make(map[string]string) // reqId → それを Holders に持つ資源の ID（検索だけに使う）
	for _, r := range l.resources {
		switch r.Kind {
		case ResourceFirework:
			if len(r.Holders) > 1 {
				return fmt.Errorf("ledger: invariant: firework %q has %d holders", r.ID, len(r.Holders))
			}
		case ResourceStall:
			if len(r.Holders) > r.Capacity {
				return fmt.Errorf("ledger: invariant: stall %q has %d holders over capacity %d", r.ID, len(r.Holders), r.Capacity)
			}
		}
		for _, h := range r.Holders {
			if prev, dup := holderOf[h]; dup {
				return fmt.Errorf("ledger: invariant: reqId %q is held by both %q and %q", h, prev, r.ID)
			}
			holderOf[h] = r.ID
			rec, ok := l.records[h]
			if !ok {
				return fmt.Errorf("ledger: invariant: holder %q of %q has no record", h, r.ID)
			}
			if rec.Result.Status != StatusOK || rec.Resource != r.ID {
				return fmt.Errorf("ledger: invariant: holder %q of %q has record %s on %q", h, r.ID, rec.Result.Status, rec.Resource)
			}
		}
	}
	if len(l.order) != len(l.records) {
		return fmt.Errorf("ledger: invariant: %d records in order but %d in the map", len(l.order), len(l.records))
	}
	var prev uint64
	for i, id := range l.order {
		rec, ok := l.records[id]
		if !ok {
			return fmt.Errorf("ledger: invariant: record %q in order is missing", id)
		}
		if i > 0 && rec.Index <= prev {
			return fmt.Errorf("ledger: invariant: record %q index %d is not after %d", id, rec.Index, prev)
		}
		if rec.Index > l.appliedIndex {
			return fmt.Errorf("ledger: invariant: record %q index %d exceeds applied index %d", id, rec.Index, l.appliedIndex)
		}
		if rec.Result.Status == StatusOK && !l.holds(rec.Resource, id) {
			return fmt.Errorf("ledger: invariant: ok record %q is not in holders of %q", id, rec.Resource)
		}
		prev = rec.Index
	}
	return nil
}

// holds は資源 resourceID の Holders に reqID があるかを返す。
func (l *Ledger) holds(resourceID, reqID string) bool {
	i, ok := l.resourceIdx[resourceID]
	return ok && slices.Contains(l.resources[i].Holders, reqID)
}
