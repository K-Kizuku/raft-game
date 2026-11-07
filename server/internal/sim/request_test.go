package sim

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/game"
)

func errorsIsInvalid(err error) bool { return errors.Is(err, game.ErrInvalidConfig) }

func TestRequestJSON(t *testing.T) { // E-11
	t.Parallel()
	tests := []struct {
		ev   DomainEvent
		want string
	}{
		{
			DomainEvent{Seq: 7, T: 0, Type: EvRequestCreated, Data: RequestCreated{Request: RequestState{
				ID: "t-0001", Origin: 1, Kind: "stall.reserve", Resource: "stall:main",
				CreatedAt: 0, Deadline: 60000, Status: "received",
			}}},
			`{"seq":7,"t":0,"type":"request.created","request":{"id":"t-0001","origin":1,"kind":"stall.reserve","resource":"stall:main","critical":false,"createdAt":0,"deadline":60000,"status":"received","expired":false,"result":null}}`,
		},
		{
			DomainEvent{Seq: 12, T: 9000, Type: EvRequestUpdated, Data: RequestUpdated{
				ID: "t-0001", Status: "succeeded", Result: &RequestResult{Status: "ok"},
			}},
			`{"seq":12,"t":9000,"type":"request.updated","id":"t-0001","status":"succeeded","expired":false,"result":{"status":"ok"}}`,
		},
		{
			DomainEvent{Seq: 13, T: 9000, Type: EvRequestUpdated, Data: RequestUpdated{
				ID: "t-0002", Status: "rejected", Result: &RequestResult{Status: "rejected", Reason: "already_reserved"},
			}},
			`{"seq":13,"t":9000,"type":"request.updated","id":"t-0002","status":"rejected","expired":false,"result":{"status":"rejected","reason":"already_reserved"}}`,
		},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.ev)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tt.want {
			t.Errorf("Marshal =\n %s\nwant\n %s", b, tt.want)
		}
		if tt.ev.Type != tt.ev.Data.EventType() {
			t.Errorf("EventType() = %s, want %s", tt.ev.Data.EventType(), tt.ev.Type)
		}
	}
	for _, d := range []EventData{RequestCreated{}, RequestUpdated{}} {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(b, &keys); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"seq", "t", "type"} {
			if _, ok := keys[k]; ok {
				t.Errorf("%T has the top-level key %q", d, k)
			}
		}
	}
}

func TestRequestViewEqual(t *testing.T) {
	t.Parallel()
	ok1 := requestView{status: game.StatusSucceeded, result: &RequestResult{Status: "ok"}}
	ok2 := requestView{status: game.StatusSucceeded, result: &RequestResult{Status: "ok"}}
	rej := requestView{status: game.StatusRejected, result: &RequestResult{Status: "rejected", Reason: "full"}}
	tests := []struct {
		a, b requestView
		want bool
	}{
		{ok1, ok2, true}, // ポインタでなく中身で比べる
		{ok1, rej, false},
		{requestView{status: game.StatusWaiting}, requestView{status: game.StatusWaiting}, true},
		{requestView{status: game.StatusWaiting}, requestView{status: game.StatusWaiting, expired: true}, false},
		{requestView{status: game.StatusSucceeded}, ok1, false},
	}
	for i, tt := range tests {
		if got := tt.a.equal(tt.b); got != tt.want {
			t.Errorf("%d: equal = %v, want %v", i, got, tt.want)
		}
	}
}
