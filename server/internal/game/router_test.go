package game

import "testing"

func TestDecide(t *testing.T) { // G-1
	t.Parallel()
	tests := []struct {
		isLeader     bool
		lead, island uint64
		want         Decision
	}{
		{true, 0, 1, Decision{Action: ActPropose}},
		{true, 1, 1, Decision{Action: ActPropose}},
		{false, 0, 2, Decision{Action: ActWait}},
		{false, 2, 2, Decision{Action: ActWait}},
		{false, 1, 2, Decision{Action: ActLetter, To: 1}},
	}
	for _, tt := range tests {
		if got := Decide(tt.isLeader, tt.lead, tt.island); got != tt.want {
			t.Errorf("Decide(%v, %d, %d) = %+v, want %+v", tt.isLeader, tt.lead, tt.island, got, tt.want)
		}
	}
}
