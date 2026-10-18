package state

import (
	"sort"
	"testing"
)

func TestTerminalStatesHaveNoExits(t *testing.T) {
	for _, s := range []State{Denied, Rejected, Failed, Settled} {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
}

func TestUncertainOnlyResolves(t *testing.T) {
	got := Next(Uncertain)
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = string(s)
	}
	sort.Strings(names)
	want := []string{string(Captured), string(Failed)}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("Uncertain exits = %v, want %v", names, want)
	}
	if CanTransition(Uncertain, Executing) || CanTransition(Uncertain, Reserved) {
		t.Fatal("retry out of Uncertain must be impossible")
	}
}

func TestOnlyExecutingAndUncertainReachCaptured(t *testing.T) {
	var from []string
	for _, s := range All() {
		if CanTransition(s, Captured) {
			from = append(from, string(s))
		}
	}
	sort.Strings(from)
	if len(from) != 2 || from[0] != string(Executing) || from[1] != string(Uncertain) {
		t.Fatalf("states leading to Captured = %v", from)
	}
}
