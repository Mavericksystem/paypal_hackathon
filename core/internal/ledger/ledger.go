package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidAmount       = errors.New("amount must be positive")
	ErrBudgetNotFound      = errors.New("budget not found")
	ErrBudgetExceeded      = errors.New("budget exceeded")
	ErrBudgetExpired       = errors.New("budget expired")
	ErrCurrencyMismatch    = errors.New("currency mismatch")
	ErrAlreadyReserved     = errors.New("intent already has a reservation")
	ErrReservationNotFound = errors.New("reservation not found")
	ErrBadReservationState = errors.New("reservation is in a conflicting state")
)

const (
	statusReserved  = "reserved"
	statusCommitted = "committed"
	statusReleased  = "released"
)

type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Snapshot struct {
	ID        string
	Limit     int64
	Reserved  int64
	Committed int64
}

func (s Snapshot) Available() int64 { return s.Limit - s.Reserved - s.Committed }

type budgetRow struct {
	id        string
	currency  string
	limit     int64
	reserved  int64
	committed int64
	expiresAt *time.Time
}
