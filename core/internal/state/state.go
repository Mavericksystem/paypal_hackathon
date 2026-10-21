package state

import (
	"errors"
	"fmt"
)

type State string

const (
	Created         State = "created"
	PolicyChecked   State = "policy_checked"
	Denied          State = "denied"
	PendingApproval State = "pending_approval"
	Approved        State = "approved"
	Rejected        State = "rejected"
	Reserved        State = "reserved"
	Executing       State = "executing"
	Captured        State = "captured"
	Settled         State = "settled"
	Failed          State = "failed"
	// Uncertain means a PayPal call may or may not have succeeded (timeout,
	// dropped response). The only way out is reconciliation against PayPal.
	Uncertain State = "uncertain"
)

var transitions = map[State][]State{
	Created:         {PolicyChecked},
	PolicyChecked:   {Denied, PendingApproval, Approved},
	PendingApproval: {Approved, Rejected},
	Approved:        {Reserved, Denied},
	Reserved:        {Executing, Failed},
	Executing:       {Captured, Failed, Uncertain},
	Uncertain:       {Captured, Failed},
	Captured:        {Settled},
	// Denied, Rejected, Failed and Settled are terminal.
}
var ErrIllegalTransition = errors.New("illegal state transition")

// All returns every known state.
func All() []State {
	return []State{
		Created, PolicyChecked, Denied, PendingApproval, Approved, Rejected,
		Reserved, Executing, Captured, Settled, Failed, Uncertain,
	}
}

func (s State) Valid() bool {
	for _, k := range All() {
		if k == s {
			return true
		}
	}
	return false
}

func (s State) IsTerminal() bool {
	return len(transitions[s]) == 0
}

// Next returns the states reachable in one step from s.
func Next(s State) []State {
	out := make([]State, len(transitions[s]))
	copy(out, transitions[s])
	return out
}

func CanTransition(from, to State) bool {
	for _, n := range transitions[from] {
		if n == to {
			return true
		}
	}
	return false
}

// Check returns nil if from -> to is allowed, otherwise ErrIllegalTransition.
func Check(from, to State) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}
