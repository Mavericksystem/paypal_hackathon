package state

import (
	"testing"
)

func TestTerminalStatesHaveNoExits(t *testing.T) {
	for _, s := range []State{Denied, Rejected, Failed, Settled} {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
}
