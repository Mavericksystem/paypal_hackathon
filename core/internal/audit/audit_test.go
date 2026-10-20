package audit_test
package audit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/testutil"
)
func appendOne(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("begin: %v", err)
		return
	}
	_, err = audit.Append(ctx, tx, "", "test", "event", map[string]any{"n": n, "amount_minor": 1200}, time.Now())
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Errorf("append: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit: %v", err)
	}
}
func seed(t *testing.T, pool *pgxpool.Pool, workers, each int) {
	t.Helper()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				appendOne(t, pool, w*1000+i)
			}
		}(w)
	}
	wg.Wait()
}

func TestConcurrentAppendsKeepChainValid(t *testing.T) {
	pool := testutil.NewPool(t)
	seed(t, pool, 20, 5)

	res, err := audit.Verify(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Entries != 100 {
		t.Fatalf("want ok with 100 entries, got %+v", res)
	}
}
func TestRolledBackAppendLeavesNoGap(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := context.Background()
	appendOne(t, pool, 1)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Append(ctx, tx, "", "test", "will_roll_back", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	appendOne(t, pool, 2)
	res, err := audit.Verify(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Entries != 2 {
		t.Fatalf("want ok with 2 entries, got %+v", res)
	}
}

func TestEditedPayloadIsDetected(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := context.Background()
	seed(t, pool, 1, 20)

	_, err := pool.Exec(ctx, `UPDATE audit_log SET payload = '{"amount_minor": 999999}'::jsonb WHERE seq = 10`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := audit.Verify(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenAtSeq != 10 {
		t.Fatalf("want break at seq 10, got %+v", res)
	}
	fmt.Println("tamper detected:", res.Reason)
}