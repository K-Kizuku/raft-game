package stage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// 検査の範囲（データ仕様 §5.4 の規則。ゲームの数値ではない）。
const (
	maxInt          = 1_000_000_000 // すべての整数の上限（掛け算のあふれを防ぐ）
	minIslands      = 3
	maxIslands      = 7
	permille        = 1000
	maxSatisfaction = 100
	// rateMilliPerMin × slotMs の上限。重みが最大の 1000 でも p_i が 1,000,000 ppm を超えない（§4.2）。
	maxRateTimesSlot = 60_000_000
	// breakdown の ferry で「シードで選ぶ」を表す予約語。
	anyFerry = "any"
)

var (
	stageIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	learningRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	keyRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	colorRe    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// Parse はステージ定義の YAML を読んで検証し、整数だけで表した Stage を返す。
// 誤りがあれば、すべての Issue を検査の順に並べた *Error を返す。
// data と is は変えない（同じ is を複数の goroutine から同時に渡してよい）。
func Parse(name string, data []byte, is *Islands) (*Stage, error) {
	if is == nil {
		return nil, fileError(name, "", "islands.json が渡されていない")
	}
	var r rawStage
	if err := decodeStrictYAML(data, &r); err != nil {
		return nil, fileError(name, "", oneLine(err.Error()))
	}
	c := newChecker(is)
	st := c.stage(&r)
	if len(c.list) > 0 {
		return nil, &Error{File: name, Issues: c.list}
	}

	if err := assignWeights(st); err != nil {
		return nil, fileError(name, "islands", err.Error())
	}
	st.Warnings = electionWarnings(st)
	st.Hash = stageHash(data, is)
	return st, nil
}

// stageHash は入力ログの stageHash（DesignDoc §10.1）。
// "sha256:" + hex(SHA-256(SHA-256(YAML) ‖ SHA-256(islands.json)))。
func stageHash(data []byte, is *Islands) string {
	y := sha256.Sum256(data)
	buf := make([]byte, 0, 2*sha256.Size)
	buf = append(buf, y[:]...)
	buf = append(buf, is.sum[:]...)
	h := sha256.Sum256(buf)
	return "sha256:" + hex.EncodeToString(h[:])
}

// assignWeights は populationExponent で決めた値から、島の重みを計算する（データ仕様 §4.1）。
func assignWeights(st *Stage) error {
	values := make([]int64, len(st.Islands))
	for i, isl := range st.Islands {
		switch st.Requests.Random.PopulationExponentPermille {
		case 0:
			values[i] = 1
		case permille / 2:
			values[i] = isl.PopulationSqrtCenti
		default: // permille
			values[i] = isl.Population
		}
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	if sum == 0 {
		return fmt.Errorf("人口の合計が 0 で重みを決められない")
	}
	w, err := Weights(values)
	if err != nil {
		return fmt.Errorf("重みを決められない: %w", err)
	}
	for i := range st.Islands {
		st.Islands[i].WeightPermille = w[i]
	}
	return nil
}

// electionWarnings は選挙タイムアウトの目安（データ仕様 §5.4）に合わないときの警告を返す。
// t_min は initialLeader に直接つながる航路の travelMs の最小。船の speed は使わない。
func electionWarnings(st *Stage) []Issue {
	leader := st.Raft.InitialLeader
	var tMin int64 = -1
	for _, r := range st.Routes {
		if (r.A == leader || r.B == leader) && (tMin < 0 || r.TravelMs < tMin) {
			tMin = r.TravelMs
		}
	}
	if tMin < 0 {
		return nil
	}
	timeout := int64(st.Raft.ElectionTick) * st.Raft.TickMs
	need := 2 * (2*tMin + st.Transport.DockMs)
	if timeout >= need {
		return nil
	}
	return []Issue{{
		Path: "raft.electionTick",
		Message: fmt.Sprintf("選挙タイムアウト electionTick × tickMs = %d ms が、Leader と最も近い Follower の往復の 2 倍 2 × (2 × %d + %d) = %d ms より短い",
			timeout, tMin, st.Transport.DockMs, need),
	}}
}

// checker は 1 つのステージの検査の状態。参照の解決に使う map は検索だけに使い、走査しない。
type checker struct {
	issues
	is *Islands

	durationMax int64              // durationMs（読めなければ maxInt）
	refs        map[string]uint64  // 正しい島の ref → ノード ID
	badRefs     map[string]bool    // 書いたが誤りのある ref（参照しても Issue を増やさない）
	islandsOK   bool               // islands に誤りがない
	routes      map[[2]uint64]bool // 航路の組（小さいほうが先）
	routesOK    bool               // routes の a・b がすべて解決できた
	ferries     map[string]bool    // 船の id（書いたものすべて）
	resources   map[string]ResourceKind
}

func newChecker(is *Islands) *checker {
	return &checker{
		is:          is,
		durationMax: maxInt,
		refs:        map[string]uint64{},
		badRefs:     map[string]bool{},
		routes:      map[[2]uint64]bool{},
		ferries:     map[string]bool{},
		resources:   map[string]ResourceKind{},
	}
}

const msgRequired = "必須の欄がない"

// reqInt は必須の整数が lo 以上 hi 以下であることを確かめる。
func (c *checker) reqInt(path string, v yInt, lo, hi int64) (int64, bool) {
	if !v.set {
		c.add(path, msgRequired)
		return 0, false
	}
	return c.rangeInt(path, v.value, lo, hi)
}

func (c *checker) rangeInt(path string, v, lo, hi int64) (int64, bool) {
	if v < lo || v > hi {
		c.add(path, fmt.Sprintf("%d 以上 %d 以下にする（値: %d）", lo, hi, v))
		return 0, false
	}
	return v, true
}

func (c *checker) reqBool(path string, v yBool) bool {
	if !v.set {
		c.add(path, msgRequired)
	}
	return v.value
}

// reqDecPositive は必須の小数が 0 より大きいことを確かめ、千分率で返す。
func (c *checker) reqDecPositive(path string, v yDec) (int64, bool) {
	if !v.set {
		c.add(path, msgRequired)
		return 0, false
	}
	if v.permille <= 0 {
		c.add(path, "0 より大きくする")
		return 0, false
	}
	return v.permille, true
}

func (c *checker) nonEmpty(path, s string) {
	if s == "" {
		c.add(path, "空にしない")
	}
}

// provenance は必須の出典区分を読む。
func (c *checker) provenance(path, s string) (Provenance, bool) {
	switch p := Provenance(s); p {
	case Fact, Derived, Creative:
		return p, true
	case "":
		c.add(path, msgRequired)
	default:
		c.add(path, fmt.Sprintf("fact・derived・creative のどれかにする（値: %q）", s))
	}
	return "", false
}

// sourceFor は source を検査する。derived なら必須。
func (c *checker) sourceFor(path string, prov Provenance, s *rawSource) *Source {
	if s == nil {
		if prov == Derived {
			c.add(path, "provenance が derived なので source が必須")
		}
		return nil
	}
	return c.source(path, s)
}

// source は出典を検査する（データ仕様 §5.4 の共通の型）。
func (c *checker) source(path string, s *rawSource) *Source {
	if s.Dataset == "" {
		c.add(path+".dataset", msgRequired)
	} else if !c.is.sources[s.Dataset] {
		c.add(path+".dataset", fmt.Sprintf("islands.json の sources にない（値: %q）", s.Dataset))
	}
	c.nonEmpty(path+".region", s.Region)
	c.nonEmpty(path+".field", s.Field)
	if s.Region != "" && s.Field != "" {
		if s.Quote != "" {
			if !c.is.excerpts[[3]string{s.Region, s.Field, s.Quote}] {
				c.add(path+".quote", "islands.json の excerpts に、region・field・quote が一致する抜き出しがない（data/curated/ に抜き出しを書く）")
			}
		} else if !c.is.regions[s.Region] {
			c.add(path+".region", fmt.Sprintf("islands.json の plan.region・excerpts[].region にない（値: %q）", s.Region))
		}
	}
	return &Source{Dataset: s.Dataset, Region: s.Region, Field: s.Field, Quote: s.Quote}
}

// text は出典区分つきの文章を検査する。t は nil でない。
func (c *checker) text(path string, t *rawText) Text {
	if strings.TrimSpace(t.Text) == "" {
		c.add(path+".text", "空白だけにしない")
	}
	prov, _ := c.provenance(path+".provenance", t.Provenance)
	return Text{Text: t.Text, Provenance: prov, Source: c.sourceFor(path+".source", prov, t.Source)}
}

// island は島の ref をノード ID にする。誤りのある島を指すときは、Issue を増やさずに false を返す。
func (c *checker) island(path, ref string) (uint64, bool) {
	if id, ok := c.refs[ref]; ok {
		return id, true
	}
	if !c.badRefs[ref] {
		if ref == "" {
			c.add(path, msgRequired)
		} else {
			c.add(path, fmt.Sprintf("islands の ref にない（値: %q）", ref))
		}
	}
	return 0, false
}

func pairKey(a, b uint64) [2]uint64 {
	if a > b {
		a, b = b, a
	}
	return [2]uint64{a, b}
}

func (c *checker) stage(r *rawStage) *Stage {
	st := &Stage{}

	// format
	if !r.Format.set {
		c.add("format", msgRequired)
	} else if r.Format.value != 1 {
		c.add("format", fmt.Sprintf("1 にする（値: %d）", r.Format.value))
	}

	// id・title・region
	if !stageIDRe.MatchString(r.ID) {
		c.add("id", fmt.Sprintf("^[a-z0-9][a-z0-9-]{0,31}$ に合わない（値: %q）", r.ID))
	}
	st.ID = r.ID
	c.nonEmpty("title", r.Title)
	st.Title = r.Title
	c.nonEmpty("region", r.Region)
	st.Region = r.Region

	// intro
	if r.Intro == nil {
		c.add("intro", msgRequired)
	} else {
		st.Intro = c.text("intro", r.Intro)
	}

	// learning
	seen := map[string]bool{}
	for i, k := range r.Learning {
		path := fmt.Sprintf("learning[%d]", i)
		switch {
		case !learningRe.MatchString(k):
			c.add(path, fmt.Sprintf("^[a-z][a-z0-9_]*$ に合わない（値: %q）", k))
		case seen[k]:
			c.add(path, fmt.Sprintf("%q が重複している", k))
		default:
			seen[k] = true
		}
		st.Learning = append(st.Learning, k)
	}

	// durationMs
	if v, ok := c.reqInt("durationMs", r.DurationMs, 1, maxInt); ok {
		st.DurationMs = v
		c.durationMax = v
	}

	// unlock
	if r.Unlock != nil {
		u := *r.Unlock
		switch {
		case !stageIDRe.MatchString(u):
			c.add("unlock", fmt.Sprintf("^[a-z0-9][a-z0-9-]{0,31}$ に合わない（値: %q）", u))
		case u == r.ID:
			c.add("unlock", "自分の id を指している")
		}
		st.Unlock = u
	}

	// tutorial
	if r.Tutorial != nil {
		if !keyRe.MatchString(*r.Tutorial) {
			c.add("tutorial", fmt.Sprintf("^[a-z0-9][a-z0-9-]*$ に合わない（値: %q）", *r.Tutorial))
		}
		st.Tutorial = *r.Tutorial
	}

	c.checkIslands(st, r.Islands)
	c.checkRoutes(st, r.Routes)
	c.checkFerries(st, r.Ferries)
	c.checkRaft(st, r.Raft)
	c.checkTransport(st, r.Transport)
	c.checkResources(st, r.Resources)
	c.checkRequests(st, r.Requests)
	for i := range r.Events {
		st.Events = append(st.Events, c.event(fmt.Sprintf("events[%d]", i), &r.Events[i]))
	}
	if r.Demo != nil {
		st.Demo = c.demo(r.Demo)
	}
	c.checkScoring(st, r.Scoring)
	c.checkSatisfaction(st, r.Satisfaction)
	c.checkSpeed(st, r.Speed)
	return st
}

func (c *checker) checkIslands(st *Stage, list []rawIsland) {
	before := len(c.list)
	if n := len(list); n < minIslands || n > maxIslands {
		c.add("islands", fmt.Sprintf("%d 個以上 %d 個以下にする（%d 個）", minIslands, maxIslands, n))
	}
	for i, ri := range list {
		path := fmt.Sprintf("islands[%d]", i)
		isl := Island{NodeID: uint64(i + 1), Ref: ri.Ref, Color: ri.Color}
		d, found := c.is.Lookup(ri.Ref)
		_, dup := c.refs[ri.Ref]
		switch {
		case ri.Ref == "":
			c.add(path+".ref", msgRequired)
		case !found:
			c.add(path+".ref", fmt.Sprintf("islands.json にない（値: %q）", ri.Ref))
			c.badRefs[ri.Ref] = true
		case dup:
			c.add(path+".ref", fmt.Sprintf("%q が重複している", ri.Ref))
		default:
			c.refs[ri.Ref] = isl.NodeID
		}
		if found {
			isl.Name, isl.Prefecture = d.Name, d.Prefecture
			isl.Population, isl.PopulationSqrtCenti = d.Population, d.PopulationSqrtCenti
		}
		if ri.Layout == nil {
			c.add(path+".layout", msgRequired)
		} else {
			isl.X, _ = c.reqInt(path+".layout.x", ri.Layout.X, 0, maxInt)
			isl.Y, _ = c.reqInt(path+".layout.y", ri.Layout.Y, 0, maxInt)
		}
		if !colorRe.MatchString(ri.Color) {
			c.add(path+".color", fmt.Sprintf("#RRGGBB の形にする（値: %q）", ri.Color))
		}
		st.Islands = append(st.Islands, isl)
	}
	c.islandsOK = len(c.list) == before
}

func (c *checker) checkRoutes(st *Stage, list []rawRoute) {
	allResolved := true
	for i, rr := range list {
		path := fmt.Sprintf("routes[%d]", i)
		rt := Route{}
		a, okA := c.island(path+".a", rr.A)
		b, okB := c.island(path+".b", rr.B)
		if okA && okB {
			switch {
			case a == b:
				c.add(path+".b", "a と同じ島にしない")
			case c.routes[pairKey(a, b)]:
				c.add(path, "同じ島の組の航路が 2 つある")
			default:
				c.routes[pairKey(a, b)] = true
			}
		} else {
			allResolved = false
		}
		rt.A, rt.B = a, b
		rt.TravelMs, _ = c.reqInt(path+".travelMs", rr.TravelMs, 1, maxInt)
		rt.Provenance, _ = c.provenance(path+".provenance", rr.Provenance)
		rt.Source = c.sourceFor(path+".source", rt.Provenance, rr.Source)
		st.Routes = append(st.Routes, rt)
	}
	c.routesOK = allResolved
	// 連結の検査は、島と航路の参照がすべて解決できたときだけ行う。
	if c.islandsOK && allResolved && !connected(len(st.Islands), st.Routes) {
		c.add("routes", "航路のグラフが連結でない（ほかの島とつながらない島がある）")
	}
}

// connected は、ノード ID 1..n と航路のグラフが連結かどうかを返す。
func connected(n int, routes []Route) bool {
	if n == 0 {
		return true
	}
	parent := make([]int, n+1)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, r := range routes {
		if r.A == 0 || r.B == 0 || int(r.A) > n || int(r.B) > n {
			continue
		}
		parent[find(int(r.A))] = find(int(r.B))
	}
	root := find(1)
	for i := 2; i <= n; i++ {
		if find(i) != root {
			return false
		}
	}
	return true
}

func (c *checker) checkFerries(st *Stage, list []rawFerry) {
	if len(list) == 0 {
		c.add("ferries", "1 隻以上にする")
	}
	for i, rf := range list {
		path := fmt.Sprintf("ferries[%d]", i)
		f := Ferry{ID: rf.ID, Name: rf.Name}
		switch {
		case !keyRe.MatchString(rf.ID):
			c.add(path+".id", fmt.Sprintf("^[a-z0-9][a-z0-9-]*$ に合わない（値: %q）", rf.ID))
		case rf.ID == anyFerry:
			c.add(path+".id", "any は breakdown の予約語なので使わない")
		case c.ferries[rf.ID]:
			c.add(path+".id", fmt.Sprintf("%q が重複している", rf.ID))
		}
		if rf.ID != "" {
			c.ferries[rf.ID] = true
		}
		c.nonEmpty(path+".name", rf.Name)
		f.Home, _ = c.island(path+".home", rf.Home)
		if v, ok := c.reqInt(path+".capacity", rf.Capacity, 1, maxInt); ok {
			f.Capacity = int(v)
		}
		f.SpeedPermille, _ = c.reqDecPositive(path+".speed", rf.Speed)
		switch Provenance(rf.Provenance) {
		case "":
			f.Provenance = Creative
		case Fact, Creative:
			f.Provenance = Provenance(rf.Provenance)
		default:
			c.add(path+".provenance", fmt.Sprintf("fact か creative にする（値: %q）", rf.Provenance))
		}
		st.Ferries = append(st.Ferries, f)
	}
}

func (c *checker) checkRaft(st *Stage, r *rawRaft) {
	if r == nil {
		c.add("raft", msgRequired)
		return
	}
	st.Raft.InitialLeader, _ = c.island("raft.initialLeader", r.InitialLeader)
	st.Raft.TickMs, _ = c.reqInt("raft.tickMs", r.TickMs, 1, maxInt)
	hb, hbOK := c.reqInt("raft.heartbeatTick", r.HeartbeatTick, 1, maxInt)
	st.Raft.HeartbeatTick = int(hb)
	v, _ := c.reqInt("raft.maxInflightMsgs", r.MaxInflightMsgs, 1, maxInt)
	st.Raft.MaxInflightMsgs = int(v)
	v, _ = c.reqInt("raft.maxSizePerMsg", r.MaxSizePerMsg, 1, maxInt)
	st.Raft.MaxSizePerMsg = uint64(v)
	if el, ok := c.reqInt("raft.electionTick", r.ElectionTick, 0, maxInt); ok {
		if hbOK && el <= hb {
			c.add("raft.electionTick", fmt.Sprintf("heartbeatTick（%d）より大きくする（値: %d）", hb, el))
		}
		st.Raft.ElectionTick = int(el)
	}
	st.Raft.CheckQuorum = c.reqBool("raft.checkQuorum", r.CheckQuorum)
	st.Raft.PreVote = c.reqBool("raft.preVote", r.PreVote)
}

func (c *checker) checkTransport(st *Stage, t *rawTransport) {
	if t == nil {
		c.add("transport", msgRequired)
		return
	}
	st.Transport.DockMs, _ = c.reqInt("transport.dockMs", t.DockMs, 0, maxInt)
	v, _ := c.reqInt("transport.relayCap", t.RelayCap, 0, maxInt)
	st.Transport.RelayCap = int(v)
	v, _ = c.reqInt("transport.perLinkAppCap", t.PerLinkAppCap, 1, maxInt)
	st.Transport.PerLinkAppCap = int(v)
	v, _ = c.reqInt("transport.portCap", t.PortCap, 1, maxInt)
	st.Transport.PortCap = int(v)
}

func (c *checker) checkResources(st *Stage, list []rawResource) {
	if len(list) == 0 {
		c.add("resources", "1 個以上にする")
	}
	for i, rr := range list {
		path := fmt.Sprintf("resources[%d]", i)
		res := Resource{ID: rr.ID, Kind: ResourceKind(rr.Kind), Label: rr.Label, Critical: rr.Critical.value}
		_, dup := c.resources[rr.ID]
		switch {
		case rr.ID == "":
			c.add(path+".id", msgRequired)
		case dup:
			c.add(path+".id", fmt.Sprintf("%q が重複している", rr.ID))
		}
		kindOK := false
		switch res.Kind {
		case ResourceFirework, ResourceStall:
			kindOK = true
		case "":
			c.add(path+".kind", msgRequired)
		default:
			c.add(path+".kind", fmt.Sprintf("firework か stall にする（値: %q）", rr.Kind))
		}
		if rr.ID != "" && !dup {
			if kindOK {
				c.resources[rr.ID] = res.Kind
			} else {
				c.resources[rr.ID] = "" // 種類が誤り。参照の種類の照合を飛ばす
			}
		}
		c.nonEmpty(path+".label", rr.Label)
		switch res.Kind {
		case ResourceFirework:
			if rr.Capacity.set {
				c.add(path+".capacity", "firework には capacity を書かない")
			}
		case ResourceStall:
			if v, ok := c.reqInt(path+".capacity", rr.Capacity, 1, maxInt); ok {
				res.Capacity = int(v)
			}
		}
		st.Resources = append(st.Resources, res)
	}
}

// requestKind は依頼の種類を検査し、その種類が使う資源の種類を返す。
func (c *checker) requestKind(path, kind string) (ResourceKind, bool) {
	switch kind {
	case RequestFireworkReserve:
		return ResourceFirework, true
	case RequestStallReserve:
		return ResourceStall, true
	case "":
		c.add(path, msgRequired)
	default:
		c.add(path, fmt.Sprintf("firework.reserve か stall.reserve にする（値: %q）", kind))
	}
	return "", false
}

// resource は資源の参照を検査する。want が "" なら種類を照合しない。
func (c *checker) resource(path, id string, want ResourceKind) {
	kind, ok := c.resources[id]
	switch {
	case id == "":
		c.add(path, msgRequired)
	case !ok:
		c.add(path, fmt.Sprintf("resources にない（値: %q）", id))
	case want != "" && kind != "" && kind != want:
		c.add(path, fmt.Sprintf("資源の種類 %s が依頼の種類に合わない（%s が必要）", kind, want))
	}
}

func (c *checker) checkRequests(st *Stage, r *rawRequests) {
	if r == nil {
		c.add("requests", msgRequired)
		return
	}
	rq := &st.Requests
	if r.DeadlineMs == nil {
		c.add("requests.deadlineMs", msgRequired)
	} else {
		rq.DeadlineNormalMs, _ = c.reqInt("requests.deadlineMs.normal", r.DeadlineMs.Normal, 1, maxInt)
		rq.DeadlineCriticalMs, _ = c.reqInt("requests.deadlineMs.critical", r.DeadlineMs.Critical, 1, maxInt)
	}
	rq.RetryMs, _ = c.reqInt("requests.retryMs", r.RetryMs, 1, maxInt)
	for i, rs := range r.Script {
		path := fmt.Sprintf("requests.script[%d]", i)
		sr := ScriptRequest{Kind: rs.Kind, Resource: rs.Resource}
		sr.At, _ = c.reqInt(path+".at", rs.At, 0, c.durationMax)
		sr.Island, _ = c.island(path+".island", rs.Island)
		want, _ := c.requestKind(path+".kind", rs.Kind)
		c.resource(path+".resource", rs.Resource, want)
		rq.Script = append(rq.Script, sr)
	}

	rd := r.Random
	if rd == nil {
		c.add("requests.random", msgRequired)
		return
	}
	// rateMilliPerMin の上限は slotMs で決まるので、slotMs を先に読む（Issue の順は表のとおり）。
	slot, slotOK := rd.SlotMs.value, rd.SlotMs.set && rd.SlotMs.value >= 1 && rd.SlotMs.value <= maxInt
	if rate, ok := c.reqInt("requests.random.rateMilliPerMin", rd.RateMilliPerMin, 0, maxInt); ok {
		if slotOK && rate*slot > maxRateTimesSlot {
			c.add("requests.random.rateMilliPerMin", fmt.Sprintf("rateMilliPerMin × slotMs を %d 以下にする（%d × %d）", maxRateTimesSlot, rate, slot))
		}
		rq.Random.RateMilliPerMin = rate
	}
	rq.Random.SlotMs, _ = c.reqInt("requests.random.slotMs", rd.SlotMs, 1, maxInt)
	if !rd.PopulationExponent.set {
		c.add("requests.random.populationExponent", msgRequired)
	} else {
		switch v := rd.PopulationExponent.permille; v {
		case 0, permille / 2, permille:
			rq.Random.PopulationExponentPermille = v
		default:
			c.add("requests.random.populationExponent", "0・0.5・1 のどれかにする")
		}
	}
	if rd.RateMilliPerMin.value > 0 && len(rd.Kinds) == 0 {
		c.add("requests.random.kinds", "rateMilliPerMin が 0 より大きいので 1 個以上にする")
	}
	for i, rk := range rd.Kinds {
		path := fmt.Sprintf("requests.random.kinds[%d]", i)
		k := RandomKind{Kind: rk.Kind, Resource: rk.Resource}
		want, _ := c.requestKind(path+".kind", rk.Kind)
		c.resource(path+".resource", rk.Resource, want)
		k.Weight, _ = c.reqInt(path+".weight", rk.Weight, 1, maxInt)
		rq.Random.Kinds = append(rq.Random.Kinds, k)
	}
}

func (c *checker) checkScoring(st *Stage, s *rawScoring) {
	if s == nil {
		c.add("scoring", msgRequired)
		return
	}
	sc := &st.Scoring
	if s.Target == nil {
		c.add("scoring.target", msgRequired)
	} else {
		sc.TargetSuccesses, _ = c.reqInt("scoring.target.successes", s.Target.Successes, 1, maxInt)
		sc.TargetScore3, _ = c.reqInt("scoring.target.score3", s.Target.Score3, 0, maxInt)
	}
	if s.Points == nil {
		c.add("scoring.points", msgRequired)
	} else {
		p := s.Points
		sc.PointsNormal, _ = c.reqInt("scoring.points.normal", p.Normal, 0, maxInt)
		sc.PointsCritical, _ = c.reqInt("scoring.points.critical", p.Critical, 0, maxInt)
		sc.PointsLate, _ = c.reqInt("scoring.points.late", p.Late, 0, maxInt)
		sc.PointsRejected, _ = c.reqInt("scoring.points.rejected", p.Rejected, 0, maxInt)
		sc.PointsExpiredPenalty, _ = c.reqInt("scoring.points.expiredPenalty", p.ExpiredPenalty, 0, maxInt)
	}
	sc.FerryCostMsPerPoint, _ = c.reqInt("scoring.ferryCostMsPerPoint", s.FerryCostMsPerPoint, 1, maxInt)
	sc.FairnessPerSatisfaction, _ = c.reqInt("scoring.fairnessPerSatisfaction", s.FairnessPerSatisfaction, 0, maxInt)
}

func (c *checker) checkSatisfaction(st *Stage, s *rawSatisfaction) {
	if s == nil {
		c.add("satisfaction", msgRequired)
		return
	}
	st.Satisfaction.Initial, _ = c.reqInt("satisfaction.initial", s.Initial, 0, maxSatisfaction)
	st.Satisfaction.SilenceAfterMs, _ = c.reqInt("satisfaction.silenceAfterMs", s.SilenceAfterMs, 1, maxInt)
	if s.SilencePenalty == nil {
		c.add("satisfaction.silencePenalty", msgRequired)
	} else {
		st.Satisfaction.SilencePenaltyEveryMs, _ = c.reqInt("satisfaction.silencePenalty.everyMs", s.SilencePenalty.EveryMs, 1, maxInt)
		st.Satisfaction.SilencePenaltyAmount, _ = c.reqInt("satisfaction.silencePenalty.amount", s.SilencePenalty.Amount, 0, maxInt)
	}
	st.Satisfaction.Succeeded, _ = c.reqInt("satisfaction.succeeded", s.Succeeded, 0, maxSatisfaction)
	st.Satisfaction.RejectedPenalty, _ = c.reqInt("satisfaction.rejectedPenalty", s.RejectedPenalty, 0, maxSatisfaction)
	st.Satisfaction.ExpiredPenalty, _ = c.reqInt("satisfaction.expiredPenalty", s.ExpiredPenalty, 0, maxSatisfaction)
}

func (c *checker) checkSpeed(st *Stage, s *rawSpeed) {
	if s == nil {
		c.add("speed", msgRequired)
		return
	}
	if len(s.Allowed) == 0 {
		c.add("speed.allowed", "1 個以上にする")
		return
	}
	var prev int64
	hasOne := false
	for i, d := range s.Allowed {
		path := fmt.Sprintf("speed.allowed[%d]", i)
		v, ok := c.reqDecPositive(path, d)
		if ok {
			if i > 0 && v <= prev {
				c.add(path, "狭義の昇順にする")
			}
			prev = v
			if v == permille {
				hasOne = true
			}
		}
		st.SpeedAllowedPermille = append(st.SpeedAllowedPermille, v)
	}
	if !hasOne {
		c.add("speed.allowed", "1 を含める")
	}
}
