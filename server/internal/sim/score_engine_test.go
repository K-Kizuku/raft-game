package sim

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	pb "go.etcd.io/raft/v3/raftpb"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// scoreTestConfig は採点のテストの基本の設定（値はテストの中の値）。
// SilenceAfterMs はラウンドより長くして、音信不通を起こさない（音信不通のテストだけ短くする）。
func scoreTestConfig(script ...game.ScriptRequest) Config {
	cfg := reqTestConfig(script...)
	cfg.Score = testScore()
	return cfg
}

func testScore() game.ScoreConfig {
	return game.ScoreConfig{
		PointsNormal: 100, PointsCritical: 300, PointsLate: 30, PointsRejected: 0, ExpiredPenalty: 50,
		FerryCostMsPerPoint: 2000, FairnessPerSatisfaction: 5, TargetSuccesses: 1, TargetScore3: 500,
		SatInitial: 60, SatSucceeded: 10, SatRejectedPenalty: 3, SatExpiredPenalty: 10,
		SilenceAfterMs: 1000000, SilenceEveryMs: 10000, SilenceAmount: 2,
	}
}

// silenceTestConfig は S-7・S-8 の設定（依頼なし、SilenceAfterMs: 30000）。
func silenceTestConfig() Config {
	cfg := testConfig()
	cfg.Score = testScore()
	cfg.Score.SilenceAfterMs = 30000
	return cfg
}

// newStarted は cfg の Engine を作って cmd.game.start を適用する（イベントは取り出さない）。
func newStarted(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e := newEngine(t, cfg, Options{CheckInvariants: true})
	mustApply(t, e, Command{Type: CmdGameStart})
	return e
}

// runE1 は「E-1 の運航」（#30 の E-1）を行う。各操作の後に hook を呼ぶ。
// load → depart → 2（時刻 0）→ RunUntil(3750) → 返事を積んで RunUntil(5250) → depart → 1（時刻 5250）→ RunUntil(9000)。
func runE1(t *testing.T, e *Engine, hook func(name string)) {
	t.Helper()
	e.RunUntil(0)
	hook("0")
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1}})
	hook("load")
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	hook("depart 2")
	e.RunUntil(3750)
	hook("3750")
	resp := raftLetters(e, 2, 1, pb.MsgAppResp)
	if len(resp) == 0 {
		t.Fatal("no MsgAppResp at island 2")
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: resp})
	hook("load resp")
	e.RunUntil(5250)
	hook("5250")
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1})
	hook("depart 1")
	e.RunUntil(9000)
	hook("9000")
}

// e1Events は cfg で開始して E-1 の運航を行い、until まで進めた全イベントを返す。
func e1Events(t *testing.T, cfg Config, until SimTime) (*Engine, []DomainEvent) {
	t.Helper()
	e := newStarted(t, cfg)
	all := e.DrainEvents()
	runE1(t, e, func(string) { all = append(all, e.DrainEvents()...) })
	e.RunUntil(until)
	all = append(all, e.DrainEvents()...)
	return e, all
}

// timed は時刻つきのイベントの中身。
type timed[T EventData] struct {
	T SimTime
	D T
}

func ofTypeAt[T EventData](evs []DomainEvent) []timed[T] {
	var out []timed[T]
	for _, ev := range evs {
		if d, ok := ev.Data.(T); ok {
			out = append(out, timed[T]{ev.T, d})
		}
	}
	return out
}

// scoreOnly は依頼・島・スコア・フェーズのイベントだけを並べる。
func scoreOnly(evs []DomainEvent) []EventData {
	var out []EventData
	for _, ev := range evs {
		switch ev.Data.(type) {
		case RequestUpdated, IslandUpdated, ScoreUpdated, PhaseChanged:
			out = append(out, ev.Data)
		}
	}
	return out
}

func scoreUp(total, successes int64, critical ...string) ScoreUpdated {
	if critical == nil {
		critical = []string{}
	}
	return ScoreUpdated{Total: total, Successes: successes, CriticalSecured: critical}
}

func islandUp(id uint64, sat int64, last SimTime) IslandUpdated {
	return IslandUpdated{ID: id, Satisfaction: sat, LastDeliveredAt: last}
}

func mustResult(t *testing.T, e *Engine) RoundResult {
	t.Helper()
	r, ok := e.Result()
	if !ok {
		t.Fatalf("Result() = false in phase %s", e.Phase())
	}
	return r
}

func checkBreakdown(t *testing.T, r RoundResult) {
	t.Helper()
	b := r.Score.Breakdown
	if b.Requests+b.Expired+b.FerryCost+b.Fairness != r.Score.Total {
		t.Errorf("breakdown %+v does not add up to %d", b, r.Score.Total)
	}
	if b.Expired > 0 || b.FerryCost > 0 {
		t.Errorf("breakdown %+v: expired and ferryCost must be <= 0", b)
	}
}

func TestScoreOnTime(t *testing.T) { // S-1
	t.Parallel()
	e, all := e1Events(t, scoreTestConfig(stallReq(0, 1)), 9000)

	scores := ofTypeAt[ScoreUpdated](all)
	wantScores := []timed[ScoreUpdated]{{0, scoreUp(-2, 0)}, {5250, scoreUp(-4, 0)}, {9000, scoreUp(96, 1)}}
	if !reflect.DeepEqual(scores, wantScores) {
		t.Errorf("score.updated = %+v, want %+v", scores, wantScores)
	}
	iDep := slices.IndexFunc(all, func(ev DomainEvent) bool { return ev.Type == EvFerryDeparted })
	iScore := slices.IndexFunc(all, func(ev DomainEvent) bool { return ev.Type == EvScoreUpdated })
	if iDep < 0 || iScore <= iDep {
		t.Errorf("ferry.departed %d, score.updated %d", iDep, iScore)
	}
	islands := ofTypeAt[IslandUpdated](all)
	wantIslands := []timed[IslandUpdated]{{3750, islandUp(2, 60, 3750)}, {9000, islandUp(1, 70, 9000)}}
	if !reflect.DeepEqual(islands, wantIslands) {
		t.Errorf("island.updated = %+v, want %+v", islands, wantIslands)
	}

	at9000 := at(all, 9000)
	iApplied := indexOf(at9000, func(d EventData) bool {
		return d == LedgerApplied{IslandID: 1, Index: 5, ReqID: "t-0001", Result: "ok"}
	})
	iReq := indexOf(at9000, func(d EventData) bool {
		u, ok := d.(RequestUpdated)
		return ok && u.ID == "t-0001" && u.Status == "succeeded"
	})
	iIsl := indexOf(at9000, func(d EventData) bool { return d == islandUp(1, 70, 9000) })
	iSc := indexOf(at9000, func(d EventData) bool { _, ok := d.(ScoreUpdated); return ok })
	if iApplied < 0 || iReq <= iApplied || iIsl <= iReq || iSc <= iIsl {
		t.Errorf("order at 9000: applied %d, request %d, island %d, score %d in %+v", iApplied, iReq, iIsl, iSc, datas(at9000))
	}

	e.RunUntil(120000)
	end := scoreOnly(at(e.DrainEvents(), 120000))
	if want := []EventData{scoreUp(396, 1), PhaseChanged{Phase: PhaseEnded}}; !reflect.DeepEqual(end, want) {
		t.Errorf("events at 120000 = %+v, want %+v", end, want)
	}
	want := RoundResult{
		Score: ResultScore{Total: 396, Breakdown: ScoreBreakdown{Requests: 100, Expired: 0, FerryCost: -4, Fairness: 300}},
		Stars: 1,
		Stats: ResultStats{Succeeded: 1},
	}
	if got := mustResult(t, e); !reflect.DeepEqual(got, want) {
		t.Errorf("Result() = %+v, want %+v", got, want)
	}
}

func TestScoreLate(t *testing.T) { // S-2
	t.Parallel()
	cfg := scoreTestConfig(stallReq(0, 1))
	cfg.Requests.DeadlineNormalMs = 5000
	e, all := e1Events(t, cfg, 9000)

	if got, want := scoreOnly(at(all, 5000)), []EventData{
		RequestUpdated{ID: "t-0001", Status: "proposed", Expired: true},
		islandUp(1, 50, 0),
		scoreUp(-52, 0),
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("events at 5000 = %+v, want %+v", got, want)
	}
	if got, want := scoreOnly(at(all, 9000)), []EventData{
		RequestUpdated{ID: "t-0001", Status: "succeeded", Expired: true, Result: resultOK},
		islandUp(1, 60, 9000),
		scoreUp(-24, 1),
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("events at 9000 = %+v, want %+v", got, want)
	}
	e.RunUntil(120000)
	want := RoundResult{
		Score: ResultScore{Total: 276, Breakdown: ScoreBreakdown{Requests: 30, Expired: -50, FerryCost: -4, Fairness: 300}},
		Stars: 1,
		Stats: ResultStats{Succeeded: 1, Expired: 1},
	}
	if got := mustResult(t, e); !reflect.DeepEqual(got, want) {
		t.Errorf("Result() = %+v, want %+v", got, want)
	}
}

func TestScoreCritical(t *testing.T) { // S-3
	t.Parallel()
	e, all := e1Events(t, scoreTestConfig(fireworkReq(0, 1)), 9000)
	scores := ofTypeAt[ScoreUpdated](at(all, 9000))
	if want := []timed[ScoreUpdated]{{9000, scoreUp(296, 1, "firework:a")}}; !reflect.DeepEqual(scores, want) {
		t.Errorf("score.updated at 9000 = %+v, want %+v", scores, want)
	}
	e.RunUntil(120000)
	r := mustResult(t, e)
	if r.Score.Total != 596 || r.Stars != 3 {
		t.Errorf("Result() = %+v, want total 596 and 3 stars", r)
	}
	checkBreakdown(t, r)
}

func TestScoreRejected(t *testing.T) { // S-4
	t.Parallel()
	e := newStarted(t, scoreTestConfig(fireworkReq(0, 1), fireworkReq(100, 1)))
	e.RunUntil(100)
	all := e.DrainEvents()
	apps := raftLetters(e, 1, 2, pb.MsgApp)
	if len(apps) != 2 {
		t.Fatalf("MsgApp to 2 = %v, want 2", apps)
	}
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: apps})
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	e.RunUntil(3850)
	mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: raftLetters(e, 2, 1, pb.MsgAppResp)})
	e.RunUntil(5350)
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 1})
	e.RunUntil(9100)
	all = append(all, e.DrainEvents()...)

	ups := ofTypeAt[RequestUpdated](all)
	var final []timed[RequestUpdated]
	for _, u := range ups {
		if u.D.Status == "succeeded" || u.D.Status == "rejected" {
			final = append(final, u)
		}
	}
	if len(final) != 2 || final[0].T != final[1].T {
		t.Fatalf("final request.updated = %+v", final)
	}
	ts := final[0].T
	cost := int64(4)
	want := []EventData{
		updated("t-0001", game.StatusSucceeded, resultOK),
		updated("t-0002", game.StatusRejected, &RequestResult{Status: "rejected", Reason: "already_reserved"}),
		islandUp(1, 67, ts),
		scoreUp(300-cost, 1, "firework:a"),
	}
	if got := scoreOnly(at(all, ts)); !reflect.DeepEqual(got, want) {
		t.Errorf("events at %d = %+v, want %+v", ts, got, want)
	}
	e.RunUntil(120000)
	r := mustResult(t, e)
	if want := (ResultStats{Succeeded: 1, Rejected: 1}); r.Stats != want || r.Score.Breakdown.Requests != 300 {
		t.Errorf("Result() = %+v, want stats %+v and requests 300", r, want)
	}
	checkBreakdown(t, r)
}

func TestScoreExpiredNeverDone(t *testing.T) { // S-5
	t.Parallel()
	e := newStarted(t, scoreTestConfig(stallReq(0, 2)))
	e.RunUntil(120000)
	all := e.DrainEvents()
	if got, want := scoreOnly(at(all, 60000)), []EventData{
		RequestUpdated{ID: "t-0001", Status: "waiting", Expired: true},
		islandUp(2, 50, 0),
		scoreUp(-50, 0),
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("events at 60000 = %+v, want %+v", got, want)
	}
	if got, want := scoreOnly(at(all, 120000)), []EventData{scoreUp(200, 0), PhaseChanged{Phase: PhaseEnded}}; !reflect.DeepEqual(got, want) {
		t.Errorf("events at 120000 = %+v, want %+v", got, want)
	}
	var expired []timed[RequestUpdated]
	for _, u := range ofTypeAt[RequestUpdated](all) {
		if u.D.Expired {
			expired = append(expired, u)
		}
	}
	if len(expired) != 1 || expired[0].T != 60000 {
		t.Errorf("expired request.updated = %+v, want 1 at 60000", expired)
	}
	r := mustResult(t, e)
	if r.Stars != 0 || r.Stats != (ResultStats{Expired: 1}) {
		t.Errorf("Result() = %+v, want 0 stars and 1 expired", r)
	}
	checkBreakdown(t, r)
}

func TestScoreDeadlineAfterRound(t *testing.T) { // S-6
	t.Parallel()
	e := newStarted(t, scoreTestConfig(stallReq(60000, 2), stallReq(100000, 3)))
	e.RunUntil(120000)
	all := e.DrainEvents()
	if len(ofType[RequestCreated](all)) != 2 {
		t.Fatalf("request.created = %+v, want 2", ofType[RequestCreated](all))
	}
	for _, u := range ofType[RequestUpdated](all) {
		if u.Expired {
			t.Errorf("expired request.updated: %+v", u)
		}
	}
	if r := mustResult(t, e); r.Stats.Expired != 0 {
		t.Errorf("Result().Stats = %+v, want 0 expired", r.Stats)
	}
}

func TestScoreSilence(t *testing.T) { // S-7
	t.Parallel()
	e := newStarted(t, silenceTestConfig())
	e.RunUntil(120000)
	all := e.DrainEvents()
	var want []timed[IslandUpdated]
	sat := int64(60)
	for ts := SimTime(30000); ts <= 110000; ts += 10000 {
		sat -= 2
		for id := uint64(1); id <= 3; id++ {
			want = append(want, timed[IslandUpdated]{ts, islandUp(id, sat, 0)})
		}
	}
	if got := ofTypeAt[IslandUpdated](all); !reflect.DeepEqual(got, want) {
		t.Errorf("island.updated =\n %+v\nwant\n %+v", got, want)
	}
	if got, want := scoreOnly(at(all, 120000)), []EventData{scoreUp(210, 0), PhaseChanged{Phase: PhaseEnded}}; !reflect.DeepEqual(got, want) {
		t.Errorf("events at 120000 = %+v, want %+v", got, want)
	}
}

func TestScoreDeliveryStopsSilence(t *testing.T) { // S-8
	t.Parallel()
	t.Run("delivered", func(t *testing.T) {
		t.Parallel()
		e := newStarted(t, silenceTestConfig())
		e.RunUntil(1000)
		var lid uint64
		for _, l := range e.Snapshot().Letters {
			if l.From == 1 && l.To == 2 && l.Loc == portLoc(1) {
				lid = l.ID
				break
			}
		}
		if lid == 0 {
			t.Fatal("no letter for island 2 at the port of island 1")
		}
		mustApply(t, e, Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{lid}})
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(120000)
		all := e.DrainEvents()
		const ta = SimTime(4750)
		var two []timed[IslandUpdated]
		var at30000 []uint64
		for _, u := range ofTypeAt[IslandUpdated](all) {
			if u.D.ID == 2 {
				two = append(two, u)
			}
			if u.T == 30000 {
				at30000 = append(at30000, u.D.ID)
			}
		}
		want := []timed[IslandUpdated]{{ta, islandUp(2, 60, ta)}}
		sat := int64(60)
		for ts := ta + 30000; ts < 120000; ts += 10000 {
			sat -= 2
			want = append(want, timed[IslandUpdated]{ts, islandUp(2, sat, ta)})
		}
		if !reflect.DeepEqual(two, want) {
			t.Errorf("island.updated of 2 =\n %+v\nwant\n %+v", two, want)
		}
		if !reflect.DeepEqual(at30000, []uint64{1, 3}) {
			t.Errorf("island.updated at 30000 for %v, want [1 3]", at30000)
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		e := newStarted(t, silenceTestConfig())
		e.RunUntil(1000)
		mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
		e.RunUntil(30000)
		all := e.DrainEvents()
		if got := ofType[IslandUpdated](at(all, 4750)); len(got) != 0 {
			t.Errorf("island.updated at 4750 = %+v, want none", got)
		}
		var ids []uint64
		for _, u := range ofType[IslandUpdated](at(all, 30000)) {
			ids = append(ids, u.ID)
		}
		if !reflect.DeepEqual(ids, []uint64{1, 2, 3}) {
			t.Errorf("island.updated at 30000 for %v, want [1 2 3]", ids)
		}
	})
}

func TestScoreSatisfactionClamp(t *testing.T) { // S-9
	t.Parallel()
	t.Run("max", func(t *testing.T) {
		t.Parallel()
		cfg := scoreTestConfig(stallReq(0, 1))
		cfg.Score.SatInitial = 100
		_, all := e1Events(t, cfg, 120000)
		if got := ofType[IslandUpdated](at(all, 9000)); !reflect.DeepEqual(got, []IslandUpdated{islandUp(1, 100, 9000)}) {
			t.Errorf("island.updated at 9000 = %+v", got)
		}
		for _, u := range ofType[IslandUpdated](all) {
			if u.ID == 3 {
				t.Errorf("island.updated for island 3 without delivery: %+v", u)
			}
		}
	})
	t.Run("min", func(t *testing.T) {
		t.Parallel()
		cfg := scoreTestConfig(stallReq(0, 2))
		cfg.Score.SatInitial = 5
		e := newStarted(t, cfg)
		e.RunUntil(60000)
		if got := ofType[IslandUpdated](at(e.DrainEvents(), 60000)); !reflect.DeepEqual(got, []IslandUpdated{islandUp(2, 0, 0)}) {
			t.Errorf("island.updated at 60000 = %+v", got)
		}
	})
}

func TestScoreVoyageCost(t *testing.T) { // S-10
	t.Parallel()
	cfg := scoreTestConfig()
	cfg.Ferries[0].SpeedPermille = 1200
	e := newStarted(t, cfg)
	e.DrainEvents()
	if ack := e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: 3}); ack.OK || ack.Code != CodeNoRoute {
		t.Fatalf("depart to 3 = %+v, want no_route", ack)
	}
	if got := ofType[ScoreUpdated](e.DrainEvents()); len(got) != 0 {
		t.Errorf("score.updated after a failed depart: %+v", got)
	}
	mustApply(t, e, Command{Type: CmdFerryDepart, FerryID: "f1", To: 2})
	evs := e.DrainEvents()
	if deps := ofType[FerryDeparted](evs); len(deps) != 1 || deps[0].ArriveAt != 4500 {
		t.Fatalf("ferry.departed = %+v, want arrive at 4500", deps)
	}
	if got := ofType[ScoreUpdated](evs); !reflect.DeepEqual(got, []ScoreUpdated{scoreUp(-3, 0)}) {
		t.Errorf("score.updated = %+v, want -3", got)
	}
}

func TestScoreDisabled(t *testing.T) { // S-11
	t.Parallel()
	e, all := e1Events(t, reqTestConfig(stallReq(0, 1)), 120000)
	if e.Phase() != PhaseEnded {
		t.Fatalf("phase = %s", e.Phase())
	}
	for _, ev := range all {
		if ev.Type == EvIslandUpdated || ev.Type == EvScoreUpdated {
			t.Errorf("event without scoring: %+v", ev)
		}
	}
	s := e.Snapshot()
	if s.Islands != nil || s.Score != nil {
		t.Errorf("Snapshot().Islands = %+v, Score = %+v; want nil", s.Islands, s.Score)
	}
	if r, ok := e.Result(); ok || !reflect.DeepEqual(r, RoundResult{}) {
		t.Errorf("Result() = %+v, %v; want false", r, ok)
	}
}

// applyScore は island.updated・score.updated を model に当てはめる。
func applyScore(islands []IslandState, score ScoreState, evs []DomainEvent) ([]IslandState, ScoreState) {
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case IslandUpdated:
			islands[d.ID-1] = IslandState(d)
		case ScoreUpdated:
			score.Total, score.Successes, score.CriticalSecured = d.Total, d.Successes, d.CriticalSecured
		}
	}
	return islands, score
}

func TestScoreSnapshot(t *testing.T) { // S-12
	t.Parallel()
	e := newEngine(t, scoreTestConfig(fireworkReq(0, 1)), Options{CheckInvariants: true})
	s := e.Snapshot()
	wantIslands := []IslandState{{1, 60, 0}, {2, 60, 0}, {3, 60, 0}}
	wantScore := &ScoreState{Total: 0, Successes: 0, Target: 1, CriticalSecured: []string{}}
	if !reflect.DeepEqual(s.Islands, wantIslands) || !reflect.DeepEqual(s.Score, wantScore) {
		t.Errorf("Snapshot() in intro: islands %+v score %+v", s.Islands, s.Score)
	}
	if s.Score == nil || s.Score.CriticalSecured == nil {
		t.Fatal("CriticalSecured is nil")
	}

	islands := slices.Clone(wantIslands)
	score := *wantScore
	check := func(name string) {
		t.Helper()
		pending, hash := len(e.pending), e.TraceHash()
		s := e.Snapshot()
		if e.TraceHash() != hash || len(e.pending) != pending {
			t.Errorf("%s: Snapshot changed the events", name)
		}
		islands, score = applyScore(islands, score, e.DrainEvents())
		if !reflect.DeepEqual(s.Islands, islands) {
			t.Errorf("%s: Snapshot().Islands = %+v, want %+v", name, s.Islands, islands)
		}
		if s.Score == nil || !reflect.DeepEqual(*s.Score, score) {
			t.Errorf("%s: Snapshot().Score = %+v, want %+v", name, s.Score, score)
		}
	}
	mustApply(t, e, Command{Type: CmdGameStart})
	check("start")
	runE1(t, e, check)
	e.RunUntil(60000)
	check("60000")
	e.RunUntil(120000)
	check("120000")
	if score.Total != 596 || !reflect.DeepEqual(score.CriticalSecured, []string{"firework:a"}) {
		t.Errorf("final score = %+v", score)
	}
}

func TestScoreInvalidConfig(t *testing.T) { // S-14
	t.Parallel()
	cfg := scoreTestConfig(stallReq(0, 1))
	cfg.Score.FerryCostMsPerPoint = 0
	e, err := New(cfg, testSeed, Options{})
	if e != nil || !errors.Is(err, game.ErrInvalidScoreConfig) {
		t.Errorf("New = %v, %v; want game.ErrInvalidScoreConfig", e, err)
	}
}

// randomScoreRun は S-16 のランダムな操作（#30 の E-14 と同じ）をラウンドの終わりまで行う。
func randomScoreRun(t *testing.T, cfg Config, seed uint64) ([]DomainEvent, [32]byte, RoundResult) {
	t.Helper()
	e, err := New(cfg, seed, Options{CheckInvariants: true})
	if err != nil {
		t.Fatal(err)
	}
	all := randomScoreOps(t, e, seed)
	return all, e.TraceHash(), mustResult(t, e)
}

func TestScoreRandom(t *testing.T) { // S-16
	t.Parallel()
	cfg := scoreTestConfig()
	cfg.DurationMs = 600000
	cfg.Score.SilenceAfterMs = 30000
	cfg.Requests.Random = game.RandomConfig{
		RateMilliPerMin: 6000,
		SlotMs:          10000,
		Weights:         []int64{500, 300, 200},
		Kinds: []game.RandomKind{
			{Kind: ledger.KindStallReserve, Resource: "stall:main", Weight: 1},
			{Kind: ledger.KindFireworkReserve, Resource: "firework:a", Weight: 1},
		},
	}
	for _, seed := range []uint64{1, 2} {
		a, ha, ra := randomScoreRun(t, cfg, seed)
		b, hb, rb := randomScoreRun(t, cfg, seed)
		if !reflect.DeepEqual(a, b) || ha != hb || ra != rb {
			t.Errorf("seed %d: same seed gave different runs", seed)
		}
		checkBreakdown(t, ra)

		var last ScoreUpdated
		expired := strSet{}
		for _, ev := range a {
			switch d := ev.Data.(type) {
			case IslandUpdated:
				if d.Satisfaction < 0 || d.Satisfaction > game.SatisfactionMax {
					t.Errorf("seed %d: satisfaction out of range: %+v", seed, d)
				}
			case ScoreUpdated:
				if d.Successes < last.Successes {
					t.Errorf("seed %d: successes decreased: %+v → %+v", seed, last, d)
				}
				for _, c := range last.CriticalSecured {
					if !slices.Contains(d.CriticalSecured, c) {
						t.Errorf("seed %d: criticalSecured lost %s: %+v", seed, c, d)
					}
				}
				last = d
			case RequestUpdated:
				if d.Expired {
					expired.add(d.ID)
				}
			}
		}
		if ra.Stats.Succeeded != last.Successes {
			t.Errorf("seed %d: Stats.Succeeded %d, last score.updated %+v", seed, ra.Stats.Succeeded, last)
		}
		if ra.Stats.Expired != int64(len(expired)) {
			t.Errorf("seed %d: Stats.Expired %d, expired requests %d", seed, ra.Stats.Expired, len(expired))
		}
		t.Logf("seed %d: %+v", seed, ra)
	}
}

// strSet は文字列の集合（追加の順を保つスライス）。
type strSet []string

func (s *strSet) add(v string) {
	if !slices.Contains(*s, v) {
		*s = append(*s, v)
	}
}

// randomScoreOps は #30 の E-14 と同じ操作（港の手紙のランダムな load・unload・隣の島への depart・おまかせ積み込み・
// 50〜2000 ずつの RunUntil）を、rand.New(rand.NewPCG(seed, 1)) で混ぜてラウンドの終わりまで行い、全イベントを返す。
func randomScoreOps(t *testing.T, e *Engine, seed uint64) []DomainEvent {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, 1))
	mustApply(t, e, Command{Type: CmdGameStart})
	all := e.DrainEvents()
	for e.Phase() != PhaseEnded {
		f, _ := e.tr.Ferry("f1")
		neighbor := func() uint64 {
			if f.At == 2 {
				return 1 + 2*uint64(r.IntN(2))
			}
			return 2
		}
		switch r.IntN(5) {
		case 0:
			if f.State == ferry.Docked {
				if port := e.tr.Port(f.At); len(port) > 0 {
					n := 1 + r.IntN(min(len(port), 5))
					var ids []uint64
					for _, i := range r.Perm(len(port))[:n] {
						ids = append(ids, uint64(port[i]))
					}
					e.Apply(Command{Type: CmdFerryLoad, FerryID: "f1", LetterIDs: ids})
				}
			}
		case 1:
			if len(f.Cargo) > 0 {
				e.Apply(Command{Type: CmdFerryUnload, FerryID: "f1", LetterIDs: []uint64{uint64(f.Cargo[r.IntN(len(f.Cargo))])}})
			}
		case 2:
			if f.State == ferry.Docked {
				e.Apply(Command{Type: CmdFerryDepart, FerryID: "f1", To: neighbor()})
			}
		case 3:
			if f.State == ferry.Docked {
				e.Apply(Command{Type: CmdFerryAutoload, FerryID: "f1", To: neighbor()})
			}
		case 4:
			e.RunUntil(e.Now() + SimTime(50+r.IntN(1951)))
		}
		all = append(all, e.DrainEvents()...)
	}
	return all
}
