package islandcore

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/cluster"
	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

func testParams() NodeParams {
	return NodeParams{
		Islands:         3,
		InitialLeader:   1,
		ElectionTick:    10,
		HeartbeatTick:   2,
		MaxSizePerMsg:   4096,
		MaxInflightMsgs: 4,
		Resources: []ledger.ResourceDef{
			{ID: "firework:a", Kind: ledger.ResourceFirework},
			{ID: "stall:main", Kind: ledger.ResourceStall, Capacity: 2},
		},
		RandIntn: func(id uint64) func(int) int { return rand.New(rand.NewPCG(7, id)).IntN },
	}
}

func testRequests() []game.Request {
	return []game.Request{
		{ID: "t-0001", Origin: 1, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 0, Deadline: 60000},
		{ID: "t-0002", Origin: 2, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 1200, Deadline: 60000},
		{ID: "t-0003", Origin: 3, Kind: ledger.KindFireworkReserve, Resource: "firework:a", CreatedAt: 1200, Deadline: 60000},
		{ID: "t-0004", Origin: 1, Kind: ledger.KindFireworkReserve, Resource: "firework:a", CreatedAt: 7000, Deadline: 60000},
		{ID: "t-0005", Origin: 2, Kind: ledger.KindStallReserve, Resource: "stall:main", CreatedAt: 26000, Deadline: 60000},
	}
}

func testConfig(island uint64) Config {
	return Config{
		Island:        island,
		Islands:       3,
		TickMs:        500,
		RetryMs:       4000,
		PortCap:       6,
		PerLinkAppCap: 2,
		RelayCap:      4,
		Requests:      testRequests(),
	}
}

// newIsland は開会式を済ませた島 island の核を作り、開会式の役割の変化を時刻 0 に当てはめて Start(0) する
// （分散版の島の Actor の init と同じ手順）。
func newIsland(t *testing.T, island uint64) *Core {
	t.Helper()
	node, l, rcs, err := Opening(testParams(), island)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(testConfig(island), node, l)
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range rcs {
		c.ApplyRoleChange(0, rc)
	}
	c.Process(0, ClassStart)
	c.ReportState(0, ClassStart)
	c.Start(0)
	return c
}

// islandState は比べるための島の状態の要約。
type islandState struct {
	Status  cluster.Status
	Port    []ferry.LetterID
	Relay   []ferry.LetterID
	Events  []Event
	Waits   []Wait
	Now     int64
	Horizon Mark
}

func stateOf(c *Core) islandState {
	return islandState{
		Status:  c.Node().Status(),
		Port:    c.Port().Port(),
		Relay:   c.Port().Relay(),
		Events:  c.Events(),
		Waits:   c.Waits(),
		Now:     c.Now(),
		Horizon: c.Horizon(),
	}
}

// heartbeatFrom は from から to への、Term term の MsgHeartbeat の手紙。
func heartbeatFrom(id ferry.LetterID, from, to, term uint64) ferry.Letter {
	u := func(v uint64) *uint64 { return &v }
	return ferry.Letter{
		ID: id, Kind: ferry.LetterRaft, From: from, To: to, CreatedAt: 0,
		Raft:     &pb.Message{Type: pb.MsgHeartbeat.Enum(), From: u(from), To: u(to), Term: u(term)},
		Location: ferry.Location{Kind: ferry.LocDelivered, Island: to},
	}
}

// advanceSplit は、ends の各上限まで、乱数で区切った上限で AdvanceTo を繰り返す。
func advanceSplit(t *testing.T, c *Core, r *rand.Rand, end Mark) {
	t.Helper()
	for c.Horizon().Less(end) {
		h := Mark{T: c.Horizon().T + int64(r.IntN(900)), Class: Class(r.IntN(9))}
		if end.Less(h) {
			h = end
		}
		if h.Less(c.Horizon()) {
			h = c.Horizon()
		}
		if err := c.AdvanceTo(h); err != nil {
			t.Fatal(err)
		}
		if h == c.Horizon() && h == end {
			return
		}
	}
}

// D-9: 島の状態と出力は、AdvanceTo の上限の区切り方によらない（simulation-time.md §2.1）。
func TestAdvanceToSplitIndependent(t *testing.T) {
	t.Parallel()
	for island := uint64(1); island <= 3; island++ {
		for split := uint64(1); split <= 5; split++ {
			whole := newIsland(t, island)
			parts := newIsland(t, island)
			r := rand.New(rand.NewPCG(split, island))
			// 区切らない: 配達の直前まで → 配達 → 終わりまで。
			mid := Mark{T: 6000, Class: ClassArrive}
			end := Mark{T: 40000, Class: ClassCommand}
			from := island%3 + 1
			deliver := []ferry.Letter{heartbeatFrom(9_000_000_001, from, island, 1)}
			if err := whole.AdvanceTo(mid); err != nil {
				t.Fatal(err)
			}
			whole.Deliver(mid.T, deliver)
			if err := whole.AdvanceTo(end); err != nil {
				t.Fatal(err)
			}
			// 細かく区切る。
			advanceSplit(t, parts, r, mid)
			parts.Deliver(mid.T, deliver)
			advanceSplit(t, parts, r, end)

			gotW, gotP := whole.Drain(), parts.Drain()
			if len(gotW) == 0 {
				t.Fatalf("island %d: no outputs", island)
			}
			if !reflect.DeepEqual(gotW, gotP) {
				n := min(len(gotW), len(gotP))
				for i := range n {
					if !reflect.DeepEqual(gotW[i], gotP[i]) {
						t.Fatalf("island %d split %d: output %d differs:\nwhole %+v\nparts %+v", island, split, i, gotW[i], gotP[i])
					}
				}
				t.Fatalf("island %d split %d: %d outputs vs %d", island, split, len(gotW), len(gotP))
			}
			if sw, sp := stateOf(whole), stateOf(parts); !reflect.DeepEqual(sw, sp) {
				t.Fatalf("island %d split %d: state differs:\nwhole %+v\nparts %+v", island, split, sw, sp)
			}
		}
	}
}

// 出力の (T, Class, Seq) は、Seq が 1 から連番で、(T, Class) は AdvanceTo の中で減らない。
func TestOutputsOrdered(t *testing.T) {
	t.Parallel()
	c := newIsland(t, 2)
	if err := c.AdvanceTo(Mark{T: 30000, Class: ClassCommand}); err != nil {
		t.Fatal(err)
	}
	outs := c.Drain()
	types := map[string]bool{}
	for i, o := range outs {
		if o.Seq != uint64(i+1) {
			t.Fatalf("output %d: seq %d", i, o.Seq)
		}
		if i > 0 && (Mark{T: o.T, Class: o.Class}).Less(Mark{T: outs[i-1].T, Class: outs[i-1].Class}) {
			t.Fatalf("output %d: mark %d/%d before %d/%d", i, o.T, o.Class, outs[i-1].T, outs[i-1].Class)
		}
		types[o.Type] = true
	}
	for _, want := range []string{OutLetterCreated, OutNodeState, OutTermChanged, OutRequestRouted} {
		if !types[want] {
			t.Errorf("no %s output (types %v)", want, types)
		}
	}
}

// 手紙の ID は、NewLetterID がなければ islandId × 10^9 + n（n は 1 から）。
func TestPerIslandLetterIDs(t *testing.T) {
	t.Parallel()
	c := newIsland(t, 3)
	if err := c.AdvanceTo(Mark{T: 30000, Class: ClassCommand}); err != nil {
		t.Fatal(err)
	}
	var ids []ferry.LetterID
	for _, o := range c.Drain() {
		if d, ok := o.Data.(LetterCreatedOut); ok {
			ids = append(ids, d.Letter.ID)
			if d.Letter.Kind == ferry.LetterRaft && d.MsgTerm != d.Letter.Raft.GetTerm() {
				t.Errorf("letter %d: msgTerm %d, raft term %d", d.Letter.ID, d.MsgTerm, d.Letter.Raft.GetTerm())
			}
		}
	}
	if len(ids) == 0 {
		t.Fatal("no letters")
	}
	for i, id := range ids {
		if want := ferry.LetterID(3*LetterIDBase + uint64(i+1)); id != want {
			t.Fatalf("letter %d: id %d, want %d", i, id, want)
		}
	}
}

func TestAdvanceToRejectsEarlierHorizon(t *testing.T) {
	t.Parallel()
	c := newIsland(t, 1)
	if err := c.AdvanceTo(Mark{T: 1000, Class: ClassRaftTick}); err != nil {
		t.Fatal(err)
	}
	before := stateOf(c)
	if err := c.AdvanceTo(Mark{T: 1000, Class: ClassArrive}); err == nil {
		t.Fatal("AdvanceTo(earlier class) = nil, want error")
	}
	if err := c.AdvanceTo(Mark{T: 999, Class: ClassCommand}); err == nil {
		t.Fatal("AdvanceTo(earlier time) = nil, want error")
	}
	if after := stateOf(c); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed by rejected AdvanceTo")
	}
	if err := c.AdvanceTo(Mark{T: 1000, Class: ClassRaftTick}); err != nil {
		t.Errorf("AdvanceTo(same horizon) = %v", err)
	}
}

// 開会式: Opening は OpeningAll の島のノードと同じ状態・同じ乱数の状態のノードを返し、役割の変化は自島のものだけ。
func TestOpeningKeepsOwnNode(t *testing.T) {
	t.Parallel()
	p := testParams()
	nodes, _, res, err := OpeningAll(p)
	if err != nil {
		t.Fatal(err)
	}
	for island := uint64(1); island <= 3; island++ {
		node, l, rcs, err := Opening(p, island)
		if err != nil {
			t.Fatal(err)
		}
		if l == nil || node.ID() != island {
			t.Fatalf("island %d: node %d, ledger %v", island, node.ID(), l)
		}
		if got, want := node.Status(), nodes[island-1].Status(); got != want {
			t.Errorf("island %d: status %+v, want %+v", island, got, want)
		}
		var want []cluster.RoleChange
		for _, rc := range res.Events {
			if rc.Node == island {
				want = append(want, rc)
			}
		}
		if !reflect.DeepEqual(rcs, want) {
			t.Errorf("island %d: role changes %+v, want %+v", island, rcs, want)
		}
		// 乱数の状態も同じ: 同じだけ Tick すると同じ時点で選挙を始める。
		ref, _, _, err := Opening(p, island)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 200 {
			node.Tick()
			ref.Tick()
			a, _ := node.Process()
			b, _ := ref.Process()
			if len(a.Messages) != len(b.Messages) || node.Status() != ref.Status() {
				t.Fatalf("island %d tick %d: diverged", island, i)
			}
		}
	}
	if _, _, _, err := Opening(p, 4); err == nil {
		t.Error("Opening(island 4) = nil error")
	}
}

// 依頼の窓口: 依頼元は、自島の台帳で結果が出たら route しない。ほかの島は MarkResolved で止まる。
func TestRouteResolved(t *testing.T) {
	t.Parallel()
	c := newIsland(t, 2)
	c.MarkCreated("t-0001")
	c.MarkResolved("t-0001")
	c.Route(100, 0, 1)
	for _, o := range c.Drain() {
		if o.Type == OutRequestRouted || o.Type == OutLetterCreated {
			t.Errorf("resolved request routed: %+v", o)
		}
	}
	// 依頼書が届いても route しない。
	c.Deliver(200, []ferry.Letter{{ID: 1, Kind: ferry.LetterRequest, From: 1, To: 2, Req: &ferry.RequestNote{ReqID: "t-0001"}}})
	for _, o := range c.Drain() {
		if o.Type == OutRequestRouted {
			t.Errorf("resolved request routed after delivery: %+v", o)
		}
	}
}

func TestStepDownReason(t *testing.T) { // T-6
	t.Parallel()
	tests := []struct {
		leaderTerm, term uint64
		want             string
	}{
		{2, 2, "check_quorum"},
		{2, 3, "higher_term"},
	}
	for _, tt := range tests {
		if got := stepDownReason(tt.leaderTerm, tt.term); got != tt.want {
			t.Errorf("stepDownReason(%d, %d) = %q, want %q", tt.leaderTerm, tt.term, got, tt.want)
		}
	}
}

// Take・Unload・TakeRelay: 港から積んだ手紙を戻すと規則をかけ直し、経由の手紙は倉庫に入る。
func TestTakeUnloadRelay(t *testing.T) {
	t.Parallel()
	c := newIsland(t, 1)
	if err := c.AdvanceTo(Mark{T: 3000, Class: ClassCommand}); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	port := c.Port().Port()
	if len(port) == 0 {
		t.Fatal("empty port (precondition)")
	}
	taken, err := c.Take(port[:1])
	if err != nil || len(taken) != 1 {
		t.Fatalf("Take = %v, %v", taken, err)
	}
	if slices.Contains(c.Port().Port(), port[0]) {
		t.Fatalf("letter %d still in port", port[0])
	}
	if _, err := c.Take(port[:1]); err == nil {
		t.Fatal("Take twice = nil error")
	}
	via := ferry.Letter{ID: 2_000_000_001, Kind: ferry.LetterRaft, From: 2, To: 3, Raft: heartbeatFrom(0, 2, 3, 1).Raft,
		Location: ferry.Location{Kind: ferry.LocRelay, Island: 1}}
	back := taken[0]
	back.Location = ferry.Location{Kind: ferry.LocPort, Island: 1}
	c.Unload(3000, []ferry.Letter{back, via})
	if !slices.Contains(c.Port().Port(), back.ID) && len(c.Drain()) == 0 {
		t.Errorf("returned letter %d is neither in port nor dropped", back.ID)
	}
	if got := c.Port().Relay(); !reflect.DeepEqual(got, []ferry.LetterID{via.ID}) {
		t.Errorf("relay = %v, want [%d]", got, via.ID)
	}
	if _, err := c.TakeRelay([]ferry.LetterID{via.ID}); err != nil {
		t.Fatal(err)
	}
	if err := c.Port().CheckInvariants(); err != nil {
		t.Error(err)
	}
}
