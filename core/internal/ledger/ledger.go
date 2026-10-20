package ledger

import (
	"context"
	"errors"
	"fmt"
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

func CreateBudget(ctx context.Context, q Querier, name, currency string, limitMinor int64, parentID string, expiresAt *time.Time) (string, error) {
	if limitMinor < 0 {
		return "", ErrInvalidAmount
	}
	var parent any
	if parentID != "" {
		var parentCurrency string
		err := q.QueryRow(ctx, `SELECT currency FROM budgets WHERE id = $1::uuid`, parentID).Scan(&parentCurrency)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrBudgetNotFound
		}
		if err != nil {
			return "", fmt.Errorf("read parent budget: %w", err)
		}
		if parentCurrency != currency {
			return "", fmt.Errorf("%w: parent is %s, child is %s", ErrCurrencyMismatch, parentCurrency, currency)
		}
		parent = parentID
	}
	var id string
	err := q.QueryRow(ctx,
		`INSERT INTO budgets (parent_id, name, currency, limit_minor, expires_at)
		 VALUES ($1::uuid, $2, $3, $4, $5) RETURNING id::text`,
		parent, name, currency, limitMinor, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert budget: %w", err)
	}
	return id, nil
}
