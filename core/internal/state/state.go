package state
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