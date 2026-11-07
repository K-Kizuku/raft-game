package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
)

// P-1: panic はエラーとスタックになり、エラーはそのまま返る。
func TestSafely(t *testing.T) {
	t.Parallel()
	stack, err := safely(func() error { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "panic: boom") {
		t.Errorf("err = %v, want panic: boom", err)
	}
	if len(stack) == 0 {
		t.Error("stack is empty")
	}

	errX := errors.New("x")
	stack, err = safely(func() error { return errX })
	if err != errX { // 包まずにそのままの値が返ること
		t.Errorf("err = %v, want errX", err)
	}
	if len(stack) != 0 {
		t.Errorf("stack = %q, want empty", stack)
	}
}

// P-2: 成功は nil。
func TestSafelyNil(t *testing.T) {
	t.Parallel()
	stack, err := safely(func() error { return nil })
	if err != nil || len(stack) != 0 {
		t.Errorf("safely = %q, %v; want nil", stack, err)
	}
}

// inputsDiffer は最初に違う位置を返す。
func TestInputsDiffer(t *testing.T) {
	t.Parallel()
	a := []sim.InputEntry{
		{T: 0, Cmd: sim.Command{Type: sim.CmdGameStart}},
		{T: 5, Cmd: sim.Command{Type: sim.CmdFerryLoad, FerryID: "f1", LetterIDs: []uint64{1, 2}}},
	}
	clone := func() []sim.InputEntry {
		b := make([]sim.InputEntry, len(a))
		copy(b, a)
		b[1].Cmd.LetterIDs = []uint64{1, 2}
		return b
	}
	if got := inputsDiffer(a, clone()); got != -1 {
		t.Errorf("same: got %d, want -1", got)
	}
	b := clone()
	b[1].Cmd.LetterIDs[1] = 3
	if got := inputsDiffer(a, b); got != 1 {
		t.Errorf("letterIds: got %d, want 1", got)
	}
	b = clone()
	b[0].T = 1
	if got := inputsDiffer(a, b); got != 0 {
		t.Errorf("t: got %d, want 0", got)
	}
	if got := inputsDiffer(a, a[:1]); got != 1 {
		t.Errorf("length: got %d, want 1", got)
	}
}
