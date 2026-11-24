package policystore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policy"
)

var (
	ErrNoPolicy = errors.New("no policy for this agent and budget")
	ErrInvalid  = errors.New("invalid policy")
)

type Store struct {
	pool *pgxpool.Pool

	Now func() time.Time
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, Now: time.Now}
}

type Input struct {
	AgentID             string
	BudgetID            string
	AllowedMerchants    []string
	Currency            string
	MaxPerPaymentMinor  int64
	AutoApproveMaxMinor int64
}

func (in Input) validate() error {
	switch {
	case strings.TrimSpace(in.AgentID) == "":
		return fmt.Errorf("%w: agent_id is required", ErrInvalid)
	case strings.TrimSpace(in.BudgetID) == "":
		return fmt.Errorf("%w: budget_id is required", ErrInvalid)
	case !validCurrency(in.Currency):
		return fmt.Errorf("%w: currency must be 3 upper case letters A to Z", ErrInvalid)
	case in.MaxPerPaymentMinor < 0:
		return fmt.Errorf("%w: max_per_payment_minor must not be negative", ErrInvalid)
	case in.AutoApproveMaxMinor < 0:
		return fmt.Errorf("%w: auto_approve_max_minor must not be negative", ErrInvalid)
	}
	for _, m := range in.AllowedMerchants {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("%w: allowed_merchants must not contain an empty name", ErrInvalid)
		}
	}
	return nil
}

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
}

func isPgCode(err error, code string) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == code
}

func (s *Store) For(ctx context.Context, agentID, budgetID string) (policy.Policy, error) {
	var p policy.Policy
	err := s.pool.QueryRow(ctx,
		`SELECT allowed_merchants, currency, max_per_payment_minor, auto_approve_max_minor
		 FROM policies WHERE agent_id = $1 AND budget_id = $2::uuid`,
		agentID, budgetID).Scan(&p.AllowedMerchants, &p.Currency, &p.MaxPerPayment, &p.AutoApproveMax)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "22P02") {
		return policy.Policy{}, ErrNoPolicy
	}
	if err != nil {
		return policy.Policy{}, fmt.Errorf("read policy: %w", err)
	}
	return p, nil
}

func (s *Store) Put(ctx context.Context, changedBy string, in Input) error {
	if strings.TrimSpace(changedBy) == "" {
		return fmt.Errorf("%w: changed_by is required", ErrInvalid)
	}
	if err := in.validate(); err != nil {
		return err
	}
	merchants := in.AllowedMerchants
	if merchants == nil {
		merchants = []string{}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	now := s.Now()
	var version int64
	err = tx.QueryRow(ctx,
		`INSERT INTO policies (agent_id, budget_id, allowed_merchants, currency, max_per_payment_minor, auto_approve_max_minor, updated_at)
		 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7)
		 ON CONFLICT (agent_id, budget_id) DO UPDATE SET
		   allowed_merchants = EXCLUDED.allowed_merchants,
		   currency = EXCLUDED.currency,
		   max_per_payment_minor = EXCLUDED.max_per_payment_minor,
		   auto_approve_max_minor = EXCLUDED.auto_approve_max_minor,
		   version = policies.version + 1,
		   updated_at = EXCLUDED.updated_at
		 RETURNING version`,
		in.AgentID, in.BudgetID, merchants, in.Currency, in.MaxPerPaymentMinor, in.AutoApproveMaxMinor, now).Scan(&version)
	switch {
	case err == nil:
	case isPgCode(err, "23503"):
		return ledger.ErrBudgetNotFound
	case isPgCode(err, "22P02"):
		return fmt.Errorf("%w: budget_id is not a valid UUID", ErrInvalid)
	default:
		return fmt.Errorf("write policy: %w", err)
	}

	payload := map[string]any{
		"agent_id":               in.AgentID,
		"budget_id":              in.BudgetID,
		"allowed_merchants":      merchants,
		"currency":               in.Currency,
		"max_per_payment_minor":  in.MaxPerPaymentMinor,
		"auto_approve_max_minor": in.AutoApproveMaxMinor,
		"version":                version,
	}
	if _, err := audit.Append(ctx, tx, "", "admin:"+changedBy, "policy_set", payload, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
