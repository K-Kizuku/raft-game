package ferryactor

import (
	"encoding/json"
	"slices"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/bpv7"
	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// bpv7.LetterCodec は dtnproto.LetterCodec を満たす（DTN-06 #163。船は bpv7 の Codec を使う）。
var _ dtnproto.LetterCodec = bpv7.LetterCodec{}

// 出力の type（actor-protocol.md §4）。
const (
	OutputFerryState     = "ferry.state"
	OutputBundleExported = "bundle.exported"
)

// 出力の class（actor-protocol.md §3.1・§4）。
const (
	classInit    uint8 = 0 // init（t = 0）
	classArrive  uint8 = 1 // arrive（sim.ClassFerryArrive と同じ）
	classCommand uint8 = 8 // depart・cargo.*・bundle.export（プレイヤーのコマンドの位置）
)

// lifetimeMarginMs は Bundle の Lifetime の、ステージの durationMs への足し分（bpv7-profile.md §3）。
const lifetimeMarginMs = 60_000

// Option は New の設定。
type Option func(*Actor)

// WithCodec は Codec を変える（試験用。既定は init のステージから作る bpv7.LetterCodec）。
func WithCodec(c dtnproto.LetterCodec) Option { return func(a *Actor) { a.fixedCodec = c } }

// WithLimits は上限を変える（既定は dtnproto.DefaultLimits。Bundle の大きさと手紙の数に使う）。
func WithLimits(lim dtnproto.Limits) Option { return func(a *Actor) { a.lim = lim } }

// item は Cargo の手紙 1 通（積んだ順に並べる）。
type item struct {
	id        ferry.LetterID
	createdAt int64  // 手紙の欄の値（Bundle Age の計算に使う）
	digest    string // bundle の Digest
	bundle    []byte // 積んだときの bytes（Bundle Age は積んだときのまま。送り出すときに bundle.export で更新する）
}

// Actor は Ferry Actor（runtime.Actor を満たす）。New で作り、init で船を決める。
// 同時に 2 つ以上のメソッドを呼ばない（Runtime が直列にする）。
type Actor struct {
	stages     []*stage.Stage
	lim        dtnproto.Limits
	fixedCodec dtnproto.LetterCodec // WithCodec。nil なら init で bpv7 を作る

	// init で決まる
	inited   bool
	ref      dtnproto.ActorRef
	id       ferry.FerryID
	capacity int
	dockMs   int64
	islands  uint64 // ステージの島の数（島の ID は 1..islands）
	codec    dtnproto.LetterCodec

	// 船の状態（意味は ferry.Ferry と同じ。故障は持たない）
	state    ferry.FerryState
	at       uint64 // Docked のときの島。Sailing のときは 0
	from, to uint64 // Sailing のとき。Docked のときは 0
	departAt int64  // Sailing のとき。Docked のときは 0
	arriveAt int64  // Sailing のとき。Docked のときは 0
	readyAt  int64  // Docked のとき、この時刻から出航できる（出航しても前の値のまま）
	cargo    []item // 積んだ順

	// 出力の順序
	lastT     int64 // 最後に状態を変えた要求の t（cargo.remove の出力の t）
	lastClass uint8
	seq       uint64 // 最後に振った出力の seq（1 から）
}

// New は stages（init の stage で引く）の Ferry Actor を作る。
func New(stages []*stage.Stage, opts ...Option) *Actor {
	a := &Actor{stages: slices.Clone(stages), lim: dtnproto.DefaultLimits}
	for _, o := range opts {
		o(a)
	}
	return a
}

func invalid(format string, args ...any) *dtnproto.Error {
	return dtnproto.Errorf(dtnproto.CodeInvalid, format, args...)
}

func (a *Actor) findStage(id string) *stage.Stage {
	for _, st := range a.stages {
		if st != nil && st.ID == id {
			return st
		}
	}
	return nil
}

// Init は init の payload で船を作る（actor-protocol.md §3.1）。初期値は ferry.New と同じ
// （Docked・At = Home・ReadyAt = 0・Cargo は空）。出力は ferry.state を 1 つ（t = 0・class 0・seq 1）。
func (a *Actor) Init(p dtnproto.InitPayload) ([]dtnproto.Output, error) {
	if a.inited {
		return nil, invalid("already initialized")
	}
	fid, ok := p.Actor.FerryID()
	if !ok {
		return nil, dtnproto.Errorf(dtnproto.CodeUnknownActor, "%q is not a ferry", p.Actor)
	}
	if len(p.Plan) > 0 {
		return nil, invalid("payload.plan: ferries have no plan")
	}
	st := a.findStage(p.Stage)
	if st == nil {
		return nil, invalid("payload.stage: unknown stage %q", p.Stage)
	}
	if p.StageHash != st.Hash {
		return nil, invalid("payload.stageHash: %s does not match stage %q (%s)", p.StageHash, st.ID, st.Hash)
	}
	i := slices.IndexFunc(st.Ferries, func(f stage.Ferry) bool { return f.ID == string(fid) })
	if i < 0 {
		return nil, dtnproto.Errorf(dtnproto.CodeUnknownActor, "stage %q has no ferry %q", st.ID, fid)
	}
	sf := st.Ferries[i]
	if sf.Capacity < 1 || st.Transport.DockMs < 0 || sf.Home < 1 || sf.Home > uint64(len(st.Islands)) {
		return nil, invalid("stage %q: ferry %q is malformed", st.ID, fid)
	}
	codec := a.fixedCodec
	if codec == nil {
		codec = bpv7.LetterCodec{LifetimeMs: uint64(st.DurationMs + lifetimeMarginMs)}
	}

	a.inited = true
	a.ref, a.id, a.capacity, a.dockMs, a.islands, a.codec = p.Actor, fid, sf.Capacity, st.Transport.DockMs, uint64(len(st.Islands)), codec
	a.state, a.at = ferry.Docked, sf.Home
	a.from, a.to, a.departAt, a.arriveAt, a.readyAt = 0, 0, 0, 0, 0
	a.cargo = nil
	a.lastT, a.lastClass, a.seq = 0, classInit, 0
	return []dtnproto.Output{a.stateOutput(0, classInit)}, nil
}

// Handle は arrive・depart・cargo.add・cargo.remove・bundle.export を処理する。
func (a *Actor) Handle(kind dtnproto.Kind, raw json.RawMessage) ([]dtnproto.Output, error) {
	if !a.inited {
		return nil, dtnproto.Errorf(dtnproto.CodeNotReady, "not initialized")
	}
	pv, perr := dtnproto.DecodePayload(kind, raw, a.ref, a.lim)
	if perr != nil {
		return nil, perr
	}
	switch p := pv.(type) {
	case *dtnproto.ArrivePayload:
		return a.arrive(p)
	case *dtnproto.DepartPayload:
		return a.depart(p)
	case *dtnproto.LettersPayload:
		if kind == dtnproto.KindCargoAdd {
			return a.cargoAdd(p)
		}
	case *dtnproto.LetterIDsPayload:
		if kind == dtnproto.KindCargoRemove {
			return a.cargoRemove(p)
		}
	case *dtnproto.ExportPayload:
		return a.export(p)
	}
	return nil, invalid("%q is not for a ferry", kind)
}

// checkT は、状態を変える要求の t が直前の要求の t より前でないことを確かめる。
func (a *Actor) checkT(t int64) *dtnproto.Error {
	if t < a.lastT {
		return invalid("payload.t: %d is before the last request's t %d", t, a.lastT)
	}
	return nil
}

func (a *Actor) arrive(p *dtnproto.ArrivePayload) ([]dtnproto.Output, error) {
	switch {
	case a.state != ferry.Sailing:
		return nil, invalid("arrive: %q is not sailing", a.id)
	case p.T != a.arriveAt:
		return nil, invalid("arrive: t %d is not arriveAt %d", p.T, a.arriveAt)
	case p.Island != a.to:
		return nil, invalid("arrive: island %d is not the destination %d", p.Island, a.to)
	}
	if err := a.checkT(p.T); err != nil {
		return nil, err
	}
	// Cargo は変えない。宛先がこの島の手紙は、Coordinator が deliver の後の cargo.remove で除く。
	a.state, a.at = ferry.Docked, p.Island
	a.from, a.to, a.departAt, a.arriveAt = 0, 0, 0, 0
	a.readyAt = p.T + a.dockMs
	return a.changed(p.T, classArrive), nil
}

func (a *Actor) depart(p *dtnproto.DepartPayload) ([]dtnproto.Output, error) {
	switch {
	case a.state != ferry.Docked:
		return nil, invalid("depart: %q is not docked", a.id)
	case p.T < a.readyAt:
		return nil, invalid("depart: t %d is before readyAt %d", p.T, a.readyAt)
	case p.To == a.at:
		return nil, invalid("depart: %q is already at %d", a.id, p.To)
	case p.To > a.islands:
		return nil, invalid("depart: unknown island %d", p.To)
	case p.ArriveAt <= p.T:
		return nil, invalid("depart: arriveAt %d must be after t %d", p.ArriveAt, p.T)
	}
	if err := a.checkT(p.T); err != nil {
		return nil, err
	}
	// 航路があるか・嵐で止まっていないか・arriveAt が TravelMs どおりかは Coordinator が確かめる。
	a.state = ferry.Sailing
	a.from, a.to, a.at = a.at, p.To, 0
	a.departAt, a.arriveAt = p.T, p.ArriveAt
	return a.changed(p.T, classCommand), nil
}

func (a *Actor) cargoAdd(p *dtnproto.LettersPayload) ([]dtnproto.Output, error) {
	if a.state != ferry.Docked {
		return nil, invalid("cargo.add: %q is not docked", a.id)
	}
	if err := a.checkT(p.T); err != nil {
		return nil, err
	}
	if len(a.cargo)+len(p.Letters) > a.capacity {
		return nil, invalid("cargo.add: %q has %d of %d, loading %d", a.id, len(a.cargo), a.capacity, len(p.Letters))
	}
	added := make([]item, 0, len(p.Letters))
	for i := range p.Letters {
		l := &p.Letters[i]
		if a.index(l.LetterID) >= 0 || slices.ContainsFunc(added, func(it item) bool { return it.id == l.LetterID }) {
			return nil, invalid("cargo.add: letter %d is already on %q", l.LetterID, a.id)
		}
		if len(l.Bundle) > a.lim.MaxBundleBytes {
			return nil, dtnproto.Errorf(dtnproto.CodeTooLarge, "cargo.add: letter %d: bundle of %d bytes exceeds %d", l.LetterID, len(l.Bundle), a.lim.MaxBundleBytes)
		}
		if dtnproto.Digest(l.Bundle) != l.Digest {
			return nil, invalid("cargo.add: letter %d: digest does not match the bundle", l.LetterID)
		}
		// Decode は letterId の照合にだけ使う（Raft のメッセージの中身は解釈しない）。
		dl, _, _, err := a.codec.Decode(l.Bundle)
		if err != nil {
			return nil, invalid("cargo.add: letter %d: %v", l.LetterID, err)
		}
		if dl.ID != l.LetterID {
			return nil, invalid("cargo.add: letter %d: the bundle is letter %d", l.LetterID, dl.ID)
		}
		added = append(added, item{id: l.LetterID, createdAt: l.CreatedAt, digest: l.Digest, bundle: slices.Clone(l.Bundle)})
	}
	a.cargo = append(a.cargo, added...)
	return a.changed(p.T, classCommand), nil
}

func (a *Actor) cargoRemove(p *dtnproto.LetterIDsPayload) ([]dtnproto.Output, error) {
	if a.state != ferry.Docked {
		return nil, invalid("cargo.remove: %q is not docked", a.id)
	}
	for _, id := range p.LetterIDs {
		if a.index(id) < 0 {
			return nil, invalid("cargo.remove: letter %d is not on %q", id, a.id)
		}
	}
	// 残りの手紙の順を保って除く（bytes も消える）。
	a.cargo = slices.DeleteFunc(a.cargo, func(it item) bool { return slices.Contains(p.LetterIDs, it.id) })
	// cargo.remove には t がないので、直前の要求の t を使う。
	return a.changed(a.lastT, classCommand), nil
}

// exportedData は bundle.exported の data。
type exportedData struct {
	LetterID ferry.LetterID `json:"letterId"`
	Digest   string         `json:"digest"`
	Size     int            `json:"size"`
}

// export は、手紙ごとに Bundle Age を t − createdAt にした bytes を、要求の順に bundle.exported で返す。
// 状態は変えない（journal に入らないので seq・lastT も進めない。出力の seq は 0）。
func (a *Actor) export(p *dtnproto.ExportPayload) ([]dtnproto.Output, error) {
	if a.state != ferry.Docked {
		return nil, invalid("bundle.export: %q is not docked", a.id)
	}
	outs := make([]dtnproto.Output, 0, len(p.LetterIDs))
	for _, id := range p.LetterIDs {
		i := a.index(id)
		if i < 0 {
			return nil, invalid("bundle.export: letter %d is not on %q", id, a.id)
		}
		it := &a.cargo[i]
		if p.T < it.createdAt {
			return nil, invalid("bundle.export: t %d is before letter %d was created (%d)", p.T, id, it.createdAt)
		}
		b, err := a.codec.WithAge(it.bundle, p.T-it.createdAt)
		if err != nil {
			return nil, invalid("bundle.export: letter %d: %v", id, err)
		}
		data, err := json.Marshal(exportedData{LetterID: id, Digest: dtnproto.Digest(b), Size: len(b)})
		if err != nil {
			return nil, err
		}
		outs = append(outs, dtnproto.Output{T: p.T, Class: classCommand, Type: OutputBundleExported, Data: data, Bundle: b})
	}
	return outs, nil
}

func (a *Actor) index(id ferry.LetterID) int {
	return slices.IndexFunc(a.cargo, func(it item) bool { return it.id == id })
}

// changed は状態を変えた要求の後に、t・class を覚えて ferry.state を 1 つ返す。
func (a *Actor) changed(t int64, class uint8) []dtnproto.Output {
	a.lastT, a.lastClass = t, class
	return []dtnproto.Output{a.stateOutput(t, class)}
}

// StateData は ferry.state の data（actor-protocol.md §4）。at は Docked のとき、to・arriveAt は Sailing のときだけ。
type StateData struct {
	State    string           `json:"state"` // "docked" | "sailing"（sim.FerryView.State と同じ）
	At       uint64           `json:"at,omitempty"`
	To       uint64           `json:"to,omitempty"`
	ArriveAt int64            `json:"arriveAt,omitempty"`
	ReadyAt  int64            `json:"readyAt"`
	Cargo    []ferry.LetterID `json:"cargo"` // 積んだ順。空なら []
}

func stateName(s ferry.FerryState) string {
	switch s {
	case ferry.Docked:
		return "docked"
	case ferry.Sailing:
		return "sailing"
	}
	return ""
}

func (a *Actor) cargoIDs() []ferry.LetterID {
	ids := make([]ferry.LetterID, len(a.cargo))
	for i, it := range a.cargo {
		ids[i] = it.id
	}
	return ids
}

func (a *Actor) stateOutput(t int64, class uint8) dtnproto.Output {
	a.seq++
	d := StateData{State: stateName(a.state), At: a.at, To: a.to, ArriveAt: a.arriveAt, ReadyAt: a.readyAt, Cargo: a.cargoIDs()}
	data, err := json.Marshal(d)
	if err != nil {
		panic("ferryactor: marshal ferry.state: " + err.Error()) // 整数と文字列だけなので起きない
	}
	return dtnproto.Output{T: t, Class: class, Seq: a.seq, Type: OutputFerryState, Data: data}
}

// digestView は stateDigest の元（persistence-and-recovery.md の復元の照合に使う）。
// フィールドの名前は正準の JSON のキー（並びは RFC 8785 が決める）。
type digestView struct {
	ID        ferry.FerryID    `json:"id"`
	State     string           `json:"state"`
	At        uint64           `json:"at"`
	From      uint64           `json:"from"`
	To        uint64           `json:"to"`
	DepartAt  int64            `json:"departAt"`
	ArriveAt  int64            `json:"arriveAt"`
	ReadyAt   int64            `json:"readyAt"`
	Cargo     []ferry.LetterID `json:"cargo"`
	Digests   []string         `json:"digests"`
	CreatedAt []int64          `json:"createdAt"`
}

func (a *Actor) digestView() digestView {
	v := digestView{
		ID: a.id, State: stateName(a.state), At: a.at, From: a.from, To: a.to,
		DepartAt: a.departAt, ArriveAt: a.arriveAt, ReadyAt: a.readyAt,
		Cargo: a.cargoIDs(), Digests: make([]string, len(a.cargo)), CreatedAt: make([]int64, len(a.cargo)),
	}
	for i, it := range a.cargo {
		v.Digests[i], v.CreatedAt[i] = it.digest, it.createdAt
	}
	return v
}

// StateDigest は {id, state, at, from, to, departAt, arriveAt, readyAt, cargo, digests, createdAt}
// （cargo・digests・createdAt は Cargo の順）の RFC 8785 の正準の JSON の SHA-256。
func (a *Actor) StateDigest() string {
	d, err := dtnproto.CanonicalDigest(a.digestView())
	if err != nil {
		panic("ferryactor: state digest: " + err.Error()) // 整数は 2^53 − 1 以下（手紙の ID は島 × 10^9 + n）
	}
	return d
}

// Cursor は、最後に状態を変えた要求の (t, class) と stateDigest を返す（船に上限はないので、horizon は進み具合の印）。
func (a *Actor) Cursor() dtnproto.Cursor {
	return dtnproto.Cursor{Horizon: dtnproto.Mark{T: a.lastT, C: a.lastClass}, StateDigest: a.StateDigest()}
}

// inspectLetter は inspect の Cargo の手紙 1 通。
type inspectLetter struct {
	LetterID  ferry.LetterID `json:"letterId"`
	CreatedAt int64          `json:"createdAt"`
	Digest    string         `json:"digest"`
	Size      int            `json:"size"`
}

// inspectView は inspect の data。
type inspectView struct {
	ID          ferry.FerryID   `json:"id"`
	Capacity    int             `json:"capacity"`
	State       string          `json:"state"`
	At          uint64          `json:"at"`
	From        uint64          `json:"from"`
	To          uint64          `json:"to"`
	DepartAt    int64           `json:"departAt"`
	ArriveAt    int64           `json:"arriveAt"`
	ReadyAt     int64           `json:"readyAt"`
	Cargo       []inspectLetter `json:"cargo"`
	StateDigest string          `json:"stateDigest"`
}

// Inspect は状態・Cargo の手紙（ID・createdAt・digest・大きさ）・stateDigest を返す（状態は変えない）。
func (a *Actor) Inspect() (json.RawMessage, error) {
	if !a.inited {
		return json.RawMessage(`{"ready":false}`), nil
	}
	v := inspectView{
		ID: a.id, Capacity: a.capacity, State: stateName(a.state), At: a.at, From: a.from, To: a.to,
		DepartAt: a.departAt, ArriveAt: a.arriveAt, ReadyAt: a.readyAt,
		Cargo: make([]inspectLetter, len(a.cargo)), StateDigest: a.StateDigest(),
	}
	for i, it := range a.cargo {
		v.Cargo[i] = inspectLetter{LetterID: it.id, CreatedAt: it.createdAt, Digest: it.digest, Size: len(it.bundle)}
	}
	return json.Marshal(v)
}
