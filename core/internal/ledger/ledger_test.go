package ledger_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/ledger"
)

func mustBudget(t *testing.T, pool *pgxpool.Pool, name string, limit int64, parent string, exp *time.Time) string {
	t.Helper()
	id, err := ledger.CreateBudget(context.Background(), pool, name, "USD", limit, parent, exp)
	if err != nil {
		t.Fatalf("create budget %s: %v", name, err)
	}
	return id
}
func reserve(pool *pgxpool.Pool, intent, budget string, amount int64, now time.Time) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := ledger.Reserve(ctx, tx, intent, budget, amount, "USD", now); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
func settle(pool *pgxpool.Pool, intent string, fn func(context.Context, pgx.Tx, string) error) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(ctx, tx, intent); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
