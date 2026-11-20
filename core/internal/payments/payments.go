package payments

import (
	"errors"
)

var (
	ErrNotExecutable      = errors.New("intent is not in a state that can be executed")
	ErrReservationRefused = errors.New("budget reservation refused")

	ErrTooEarly = errors.New("too early to reconcile this payment")
)
