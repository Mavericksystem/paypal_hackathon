package ledger_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

	if ok.Load() != 33 || denied.Load() != 67 {
		t.Fatalf("want 33 ok and 67 denied, got %d and %d", ok.Load(), denied.Load())
	}
	s := snap(t, pool, budget)
	if s.Reserved != 33*each || s.Reserved > s.Limit {
		t.Fatalf("reserved %d, limit %d", s.Reserved, s.Limit)
	}
}
func TestChildCannotExceedParent(t *testing.T) {
	pool := testutil.NewPool(t)
	parent := mustBudget(t, pool, "parent", 1000, "", nil)
	a := mustBudget(t, pool, "child-a", 800, parent, nil)
	b := mustBudget(t, pool, "child-b", 800, parent, nil)

	ia := testutil.InsertIntent(t, pool, a, 600, "USD")
	ib := testutil.InsertIntent(t, pool, b, 600, "USD")

	if err := reserve(pool, ia, a, 600, time.Now()); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := reserve(pool, ib, b, 600, time.Now()); !errors.Is(err, ledger.ErrBudgetExceeded) {
		t.Fatalf("second reserve: want ErrBudgetExceeded, got %v", err)
	}
	if err := settle(pool, ia, ledger.Release); err != nil {
		t.Fatal(err)
	}
	if err := reserve(pool, ib, b, 600, time.Now()); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if s := snap(t, pool, parent); s.Reserved != 600 {
		t.Fatalf("parent reserved %d, want 600", s.Reserved)
	}
}


func TestConcurrentSiblingsRespectParent(t *testing.T) {
	pool := testutil.NewPool(t)
	parent := mustBudget(t, pool, "parent", 1000, "", nil)
	children := []string{
		mustBudget(t, pool, "c1", 1000, parent, nil),
		mustBudget(t, pool, "c2", 1000, parent, nil),
		mustBudget(t, pool, "c3", 1000, parent, nil),
	}

	var ok atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		child := children[i%len(children)]
		intent := testutil.InsertIntent(t, pool, child, 100, "USD")
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := reserve(pool, intent, child, 100, time.Now()); err == nil {
				ok.Add(1)
			} else if !errors.Is(err, ledger.ErrBudgetExceeded) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()