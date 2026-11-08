package protocol

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
)

// Role は Actor の役割。
type Role uint8

// 役割。
const (
	RoleCoordinator Role = 1 << iota
	RoleIsland
	RoleFerry
)

// String は役割の名前（ROLE の値と同じ）を返す。
func (r Role) String() string {
	switch r {
	case RoleCoordinator:
		return "coordinator"
	case RoleIsland:
		return "island"
	case RoleFerry:
		return "ferry"
	}
	return "unknown"
}

// ActorRef は Actor の宛先（actor-protocol.md §2.1 の actor）。
// coordinator・island:<raftNodeId>（例 island:2）・ferry:<ferryId>（ステージの ferries[].id。例 ferry:f1）。
type ActorRef string

// CoordinatorRef は Coordinator の宛先。
const CoordinatorRef ActorRef = "coordinator"

// ferryIDRe はステージ定義の ferries[].id の形（server/internal/stage の keyRe と同じ）。
var ferryIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// IslandRef は島 id の宛先を返す。
func IslandRef(id uint64) ActorRef { return ActorRef("island:" + strconv.FormatUint(id, 10)) }

// FerryRef は船 id の宛先を返す。
func FerryRef(id ferry.FerryID) ActorRef { return ActorRef("ferry:" + string(id)) }

// ParseActorRef は s を確かめて返す。形が違えば invalid。
func ParseActorRef(s string) (ActorRef, error) {
	if _, err := ActorRef(s).parse(); err != nil {
		return "", err
	}
	return ActorRef(s), nil
}

// Valid は形が正しいか。
func (a ActorRef) Valid() bool {
	_, err := a.parse()
	return err == nil
}

// Role は役割を返す（形が違えば 0）。
func (a ActorRef) Role() Role {
	r, _ := a.parse()
	return r
}

// IslandID は island:<id> の id を返す。
func (a ActorRef) IslandID() (uint64, bool) {
	s, ok := strings.CutPrefix(string(a), "island:")
	if !ok || a.Role() != RoleIsland {
		return 0, false
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n, true
}

// FerryID は ferry:<id> の id を返す。
func (a ActorRef) FerryID() (ferry.FerryID, bool) {
	s, ok := strings.CutPrefix(string(a), "ferry:")
	if !ok || a.Role() != RoleFerry {
		return "", false
	}
	return ferry.FerryID(s), true
}

func (a ActorRef) parse() (Role, *Error) {
	s := string(a)
	if s == string(CoordinatorRef) {
		return RoleCoordinator, nil
	}
	if id, ok := strings.CutPrefix(s, "island:"); ok {
		// raftNodeId: 1 以上の 10 進（先頭の 0 なし）、2^53 − 1 以下
		if id == "" || id[0] == '0' || len(id) > 16 {
			return 0, Errorf(CodeInvalid, "invalid actor %q", s)
		}
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil || n > MaxSafeInteger {
			return 0, Errorf(CodeInvalid, "invalid actor %q", s)
		}
		return RoleIsland, nil
	}
	if id, ok := strings.CutPrefix(s, "ferry:"); ok {
		if !ferryIDRe.MatchString(id) {
			return 0, Errorf(CodeInvalid, "invalid actor %q", s)
		}
		return RoleFerry, nil
	}
	return 0, Errorf(CodeInvalid, "invalid actor %q", s)
}
