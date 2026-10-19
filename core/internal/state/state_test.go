package state

import (
	"errors"
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

func reachable(target, forbidden State) bool {
	seen := map[State]bool{Created: true}
	queue := []State{Created}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			return true
		}
		for _, n := range Next(cur) {
			if n == forbidden || seen[n] {
				continue
			}
			seen[n] = true
			queue = append(queue, n)
		}
	}
	return false
}

func TestNoCaptureWithoutReservation(t *testing.T) {
	if !reachable(Captured, "") {
		t.Fatal("Captured must be reachable at all")
	}
	if reachable(Captured, Reserved) {
		t.Fatal("found a path to Captured that skips Reserved")
	}
}
func TestNoCaptureWithoutApproval(t *testing.T) {
	if reachable(Captured, Approved) {
		t.Fatal("found a path to Captured that skips Approved")
	}
}
func TestNoCaptureWithoutPolicyCheck(t *testing.T) {
	if reachable(Captured, PolicyChecked) {
		t.Fatal("found a path to Captured that skips PolicyChecked")
	}
}
func TestEveryStateReachableFromCreated(t *testing.T) {
	for _, s := range All() {
		if !reachable(s, "") {
			t.Errorf("%s is unreachable", s)
		}
	}
}
func TestNoCycles(t *testing.T) {
	const (
		white = iota
		grey
		black
	)
	color := map[State]int{}
	var visit func(State) bool
	visit = func(s State) bool {
		color[s] = grey
		for _, n := range Next(s) {
			if color[n] == grey {
				return true
			}
			if color[n] == white && visit(n) {
				return true
			}
		}
		color[s] = black
		return false
	}
	for _, s := range All() {
		if color[s] == white && visit(s) {
			t.Fatal("state machine contains a cycle")
		}
	}
}
func TestCheckReturnsTypedError(t *testing.T) {
	if err := Check(Created, Captured); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("got %v, want ErrIllegalTransition", err)
	}
	if err := Check(Created, PolicyChecked); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestAllStatesValid(t *testing.T) {
	for _, s := range All() {
		if !s.Valid() {
			t.Errorf("%s not valid", s)
		}
	}
	if State("bogus").Valid() {
		t.Fatal("bogus state accepted")
	}
}
