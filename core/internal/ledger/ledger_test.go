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
func snap(t *testing.T, pool *pgxpool.Pool, id string) ledger.Snapshot {
	t.Helper()
	s, err := ledger.GetSnapshot(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestConcurrentReservationsNeverExceedBudget(t *testing.T) {
	pool := testutil.NewPool(t)
	const agents, each, limit = 100, 300, 10_000
	budget := mustBudget(t, pool, "root", limit, "", nil)

	intents := make([]string, agents)
	for i := range intents {
		intents[i] = testutil.InsertIntent(t, pool, budget, each, "USD")
	}
	var ok, denied atomic.Int64
	var wg sync.WaitGroup
	for _, id := range intents {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := reserve(pool, id, budget, each, time.Now())
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ledger.ErrBudgetExceeded):
				denied.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(id)
	}
	wg.Wait()