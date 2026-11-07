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

// runRandom は乱数で操作を混ぜて ops 回行い、各操作の結果を文字列の列で返す。
// 毎回 CheckInvariants を確かめる。
func runRandom(t *testing.T, seed uint64, ops int) []string {
	t.Helper()
	cfg := baseConfig()
	cfg.PortCap = 6
	cfg.PerLinkAppCap = 2
	tr := newT(t, cfg)
	rng := rand.New(rand.NewPCG(seed, 1))
	islands := cfg.Islands
	ferries := []FerryID{"f1", "f2"}
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
			ls = append(ls, LetterID(rng.IntN(len(tr.letters)+2)))
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
			id, d, err := tr.PostRequest(now, from, pick(from), RequestNote{ReqID: fmt.Sprintf("r%d", rng.IntN(4))})
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
		if err := tr.CheckInvariants(); err != nil {
			t.Fatalf("op %d (%s): %v", i, res, err)
		}
		out = append(out, res)
	}
	return out
}

func TestBoundRandom(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			runRandom(t, seed, 20000)
		})
	}
}

func TestDeterministic(t *testing.T) {
	t.Parallel()
	a := runRandom(t, 7, 20000)
	b := runRandom(t, 7, 20000)
	if !slices.Equal(a, b) {
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("op %d differs:\n%s\n%s", i, a[i], b[i])
			}
		}
		t.Fatal("results differ")
	}
}
