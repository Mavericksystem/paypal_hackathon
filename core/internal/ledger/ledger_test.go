package ledger_test

import (
	"context"
	"testing"
	"time"

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
