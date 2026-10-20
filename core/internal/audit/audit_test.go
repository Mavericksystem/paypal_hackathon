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