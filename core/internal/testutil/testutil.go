package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/db"
)

func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	schema := "t_" + hex.EncodeToString(buf)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	pool, err := db.Connect(ctx, url, map[string]string{"search_path": schema})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}
func InsertIntent(t *testing.T, pool *pgxpool.Pool, budgetID string, amountMinor int64, currency string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO intents (agent_id, budget_id, state, amount_minor, currency, merchant, recipient, idempotency_key)
		 VALUES ('test-agent', $1::uuid, 'created', $2, $3, 'test-merchant', 'test-recipient', gen_random_uuid()::text)
		 RETURNING id::text`, budgetID, amountMinor, currency).Scan(&id)
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	return id
}
