package ferry

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"
)

var randomTypes = []pb.MessageType{
	pb.MsgApp, pb.MsgAppResp, pb.MsgHeartbeat, pb.MsgHeartbeatResp,
	pb.MsgVote, pb.MsgVoteResp, pb.MsgPreVote, pb.MsgPreVoteResp,
	pb.MsgTimeoutNow, pb.MsgSnap,
}

// runRandom は乱数で操作を混ぜて ops 回行い、各操作の結果を文字列の列と Transport を返す。
// reclaim が nil でなく reclaim(i) が真なら、操作 i のあとに Reclaim を呼び、直後に持つ手紙が
// 港・船の手紙だけで、その数が上限（島の数 ×（PortCap + ReqID の数）+ 積載量の和）以下であることを
// 確かめる。Reclaim の返り値は結果の列に入れない。毎回 CheckInvariants を確かめる。
func runRandom(t *testing.T, seed uint64, ops int, reclaim func(op int) bool) ([]string, *Transport) {
	t.Helper()
	cfg := baseConfig()
	cfg.PortCap = 6
	cfg.PerLinkAppCap = 2
	tr := newT(t, cfg)
	rng := rand.New(rand.NewPCG(seed, 1))
	islands := cfg.Islands
	ferries := []FerryID{"f1", "f2"}
	const reqIDs = 4
	bound := len(islands) * (cfg.PortCap + reqIDs)
	for _, f := range cfg.Ferries {
		bound += f.Capacity
	}
	var now int64
	var out []string
	pick := func(from uint64) uint64 {
		for {
			to := islands[rng.IntN(len(islands))]
			if to != from {
				return to
			}
		}
	}
	// randomLetters はその場所の手紙と、ときどき無関係な ID を返す。
	randomLetters := func(pool []LetterID) []LetterID {
		var ls []LetterID
		for _, id := range pool {
			if rng.IntN(2) == 0 {
				ls = append(ls, id)
			}
		}
		if rng.IntN(10) == 0 {
			ls = append(ls, LetterID(rng.IntN(int(tr.lastID)+2)))
		}
		return ls
	}
	for i := range ops {
		var res string
		switch op := rng.IntN(100); {
		case op < 45:
			from := islands[rng.IntN(len(islands))]
			n := 1 + rng.IntN(3)
			msgs := make([]*pb.Message, n)
			for j := range msgs {
				m := msg(randomTypes[rng.IntN(len(randomTypes))], from, pick(from), uint64(1+rng.IntN(5)))
				if m.GetType() == pb.MsgAppResp && rng.IntN(3) == 0 {
					m.Reject = bptr(true)
				}
				msgs[j] = m
			}
			c, d, err := tr.PostRaft(now, from, msgs)
			res = fmt.Sprintf("post %v %v %v", c, d, err)
		case op < 55:
			from := islands[rng.IntN(len(islands))]
			id, d, err := tr.PostRequest(now, from, pick(from), RequestNote{ReqID: fmt.Sprintf("r%d", rng.IntN(reqIDs))})
			res = fmt.Sprintf("req %d %v %v", id, d, err)
		case op < 70:
			f := ferries[rng.IntN(len(ferries))]
			fv, _ := tr.Ferry(f)
			err := tr.Load(f, randomLetters(tr.Port(fv.At)))
			res = fmt.Sprintf("load %s %v", f, err)
		case op < 78:
			f := ferries[rng.IntN(len(ferries))]
			fv, _ := tr.Ferry(f)
			d, err := tr.Unload(f, randomLetters(fv.Cargo))
			res = fmt.Sprintf("unload %s %v %v", f, d, err)
		case op < 90:
			f := ferries[rng.IntN(len(ferries))]
			at, err := tr.Depart(now, f, islands[rng.IntN(len(islands))])
			res = fmt.Sprintf("depart %s %d %v", f, at, err)
		case op < 97:
			// 時刻を進めて、到着の時刻の早い船から到着させる。
			now += int64(rng.IntN(3000))
			fs := tr.Ferries()
			slices.SortStableFunc(fs, func(a, b Ferry) int { return int(a.ArriveAt - b.ArriveAt) })
			for _, f := range fs {
				if f.State == Sailing && f.ArriveAt <= now {
					d, err := tr.Arrive(f.ArriveAt, f.ID)
					res += fmt.Sprintf("arrive %s %d %v %v;", f.ID, f.ArriveAt, d, err)
				}
			}
			res += fmt.Sprintf("now %d", now)
		default:
			pairs := [][2]uint64{{1, 2}, {2, 3}, {1, 3}}
			p := pairs[rng.IntN(len(pairs))]
			err := tr.Block([][2]uint64{p}, now+int64(rng.IntN(10000)))
			res = fmt.Sprintf("block %v %v", p, err)
		}
		if reclaim != nil && reclaim(i) {
			tr.Reclaim()
			if len(tr.letters) > bound {
				t.Fatalf("op %d: %d letters after Reclaim, bound %d", i, len(tr.letters), bound)
			}
			for _, l := range tr.letters {
				if k := l.Location.Kind; k != LocPort && k != LocFerry {
					t.Fatalf("op %d: letter %d at %+v after Reclaim", i, l.ID, l.Location)
				}
			}
		}
		if err := tr.CheckInvariants(); err != nil {
			t.Fatalf("op %d (%s): %v", i, res, err)
		}
		out = append(out, res)
	}
	return out, tr
}

func TestBoundRandom(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			runRandom(t, seed, 20000, nil)
		})
	}
}

func TestDeterministic(t *testing.T) {
	t.Parallel()
	a, _ := runRandom(t, 7, 20000, nil)
	b, _ := runRandom(t, 7, 20000, nil)
	// C-7: Reclaim を呼ぶ時点を変えても、結果の列は同じ。
	c, _ := runRandom(t, 7, 20000, func(int) bool { return true })
	d, _ := runRandom(t, 7, 20000, func(i int) bool { return i%13 == 0 })
	for _, r := range []struct {
		name string
		x    []string
	}{{"b", b}, {"c (every op)", c}, {"d (i%13)", d}} {
		name, x := r.name, r.x
		if !slices.Equal(a, x) {
			for i := range min(len(a), len(x)) {
				if a[i] != x[i] {
					t.Fatalf("%s: op %d differs:\n%s\n%s", name, i, a[i], x[i])
				}
			}
			t.Fatalf("%s: results differ", name)
		}
	}
}

// C-6: 毎回 Reclaim すれば、10 万通以上を流しても持つ手紙の数は港・船の上限以下に保たれる
// （上限と場所は runRandom の中で確かめる）。
func TestBoundRandomReclaim(t *testing.T) {
	t.Parallel()
	_, tr := runRandom(t, 1, 120000, func(int) bool { return true })
	if tr.lastID < 100000 {
		t.Fatalf("lastID = %d, want >= 100000", tr.lastID)
	}
}
