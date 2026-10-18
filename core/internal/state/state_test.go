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
