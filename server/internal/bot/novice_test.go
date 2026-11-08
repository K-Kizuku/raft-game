package bot

import (
	"reflect"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// novicePlanner は s から航路を作った novice。
func novicePlanner(s sim.Snapshot) *noviceBot {
	b := newNovice()
	b.observe(s)
	return b
}

// N-1 島を順に回る行き先。
func TestNextInTurn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		open    []uint64
		came    uint64
		hasCame bool
		want    uint64
	}{
		{"通れる島がない", nil, 0, false, 0},
		{"通れる島がない（来た島あり）", nil, 2, true, 0},
		{"最初は最小の ID", []uint64{2, 5}, 0, false, 2},
		{"線の端から戻る", []uint64{2}, 2, true, 2},
		{"線の中ほどは来た島の反対へ", []uint64{1, 3}, 1, true, 3},
		{"線の中ほどは来た島の反対へ（逆向き）", []uint64{1, 3}, 3, true, 1},
		{"来た島より大きい ID の最小", []uint64{1, 2, 4, 6}, 3, true, 4},
		{"来た島より大きい ID がなければ最小", []uint64{1, 2, 4}, 5, true, 1},
		{"来た島を除いて次の ID", []uint64{1, 2, 4}, 2, true, 4},
		{"来た島を除いて折り返す", []uint64{1, 2, 4}, 4, true, 1},
		{"来た島が嵐で通れない", []uint64{3}, 1, true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := nextInTurn(tt.open, tt.came, tt.hasCame); got != tt.want {
				t.Fatalf("nextInTurn(%v, %d, %v) = %d, want %d", tt.open, tt.came, tt.hasCame, got, tt.want)
			}
		})
	}
}

// N-2 計画: 着いてから 3 秒待ち、行き先宛ての手紙だけをおまかせで積んで、島を順に回る。
func TestNovicePlan(t *testing.T) {
	t.Parallel()
	line := routes(lineRoutes...)
	port1 := []sim.LetterView{
		inPort(1, "append", 1, 3), // 経由の手紙（積まない）
		inPort(2, "heartbeat", 1, 2),
	}

	t.Run("着いてから 3 秒たつまで待つ", func(t *testing.T) {
		t.Parallel()
		s := snap(noviceThinkMs-1, 3, line, []sim.FerryView{docked("f1", 1, 4)}, port1)
		if got := novicePlanner(s).plan(s, 0); got != nil {
			t.Fatalf("plan = %+v, want nil", got)
		}
	})

	t.Run("行き先宛てをおまかせで積んで出航", func(t *testing.T) {
		t.Parallel()
		s := snap(noviceThinkMs, 3, line, []sim.FerryView{docked("f1", 1, 4)}, port1)
		b := novicePlanner(s)
		want := []sim.Command{cmdAutoload("f1", 2), cmdDepart("f1", 2)}
		if got := b.plan(s, 0); !reflect.DeepEqual(got, want) {
			t.Fatalf("plan = %+v, want %+v", got, want)
		}
		if came, ok := b.came["f1"]; !ok || came != 1 {
			t.Fatalf("came = %d, %v; want 1", came, ok)
		}
	})

	t.Run("行き先宛てがなければ積まずに出航", func(t *testing.T) {
		t.Parallel()
		s := snap(noviceThinkMs, 3, line, []sim.FerryView{docked("f1", 1, 4)}, []sim.LetterView{inPort(1, "append", 1, 3)})
		want := []sim.Command{cmdDepart("f1", 2)}
		if got := novicePlanner(s).plan(s, 0); !reflect.DeepEqual(got, want) {
			t.Fatalf("plan = %+v, want %+v", got, want)
		}
	})

	t.Run("満載なら積まずに出航", func(t *testing.T) {
		t.Parallel()
		s := snap(noviceThinkMs, 3, line, []sim.FerryView{docked("f1", 2, 1, 9)}, []sim.LetterView{
			onFerry(9, "heartbeat", 1, 3, "f1"),
			inPort(3, "heartbeat_resp", 2, 3),
		})
		b := novicePlanner(s)
		b.came["f1"] = 1
		want := []sim.Command{cmdDepart("f1", 3)}
		if got := b.plan(s, 0); !reflect.DeepEqual(got, want) {
			t.Fatalf("plan = %+v, want %+v", got, want)
		}
	})

	t.Run("着いた時刻から数える", func(t *testing.T) {
		t.Parallel()
		port2 := []sim.LetterView{inPort(1, "append_resp", 2, 1), inPort(2, "heartbeat_resp", 2, 3)}
		s := snap(20000, 3, line, []sim.FerryView{docked("f1", 2, 4)}, port2)
		b := novicePlanner(s)
		b.came["f1"] = 3
		b.arrived["f1"] = 20000 - noviceThinkMs + 1
		if got := b.plan(s, 0); got != nil {
			t.Fatalf("plan before think = %+v, want nil", got)
		}
		b.arrived["f1"] = 20000 - noviceThinkMs
		want := []sim.Command{cmdAutoload("f1", 1), cmdDepart("f1", 1)}
		if got := b.plan(s, 0); !reflect.DeepEqual(got, want) {
			t.Fatalf("plan = %+v, want %+v", got, want)
		}
	})

	t.Run("通れる島がなければ待つ", func(t *testing.T) {
		t.Parallel()
		s := snap(noviceThinkMs, 3, blocked(line, "1-2"), []sim.FerryView{docked("f1", 1, 4)}, port1)
		b := novicePlanner(s)
		if got := b.plan(s, 0); got != nil {
			t.Fatalf("plan = %+v, want nil", got)
		}
		if _, ok := b.came["f1"]; ok {
			t.Fatal("came is set while waiting")
		}
	})
}

// N-3 Engine の上で、操作は おまかせ積み込み と 出航 だけで、出航は着いてから 3 秒より後。線の上を往復する。
func TestNovicePlayMoves(t *testing.T) {
	t.Parallel()
	cfg := lineConfig()
	e, evs := playRun(t, "novice", cfg, 1)
	arrivedAt := map[string]sim.SimTime{}
	at := map[string]uint64{}
	for _, f := range cfg.Ferries {
		at[string(f.ID)] = f.Home
	}
	var path []uint64
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case sim.FerryArrived:
			arrivedAt[d.FerryID] = ev.T
			at[d.FerryID] = d.IslandID
		case sim.FerryDeparted:
			if ev.T < arrivedAt[d.FerryID]+noviceThinkMs {
				t.Fatalf("%s departed at %d, arrived at %d", d.FerryID, ev.T, arrivedAt[d.FerryID])
			}
			if d.From != at[d.FerryID] {
				t.Fatalf("%s departed from %d, at %d", d.FerryID, d.From, at[d.FerryID])
			}
			path = append(path, d.To)
		}
	}
	if len(path) < 4 {
		t.Fatalf("path = %v, want at least 4 departures", path)
	}
	want := []uint64{2, 3, 2, 1}
	for k, to := range path {
		if to != want[k%len(want)] {
			t.Fatalf("path = %v, want repeat of %v", path, want)
		}
	}
	for _, in := range e.InputLog()[1:] {
		switch in.Cmd.Type {
		case sim.CmdFerryAutoload, sim.CmdFerryDepart:
		default:
			t.Fatalf("unexpected command %+v", in)
		}
	}
}
