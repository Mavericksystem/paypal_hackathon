package policystore_test

import (
	"context"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/intents"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policystore"
	"github.com/Mavericksystem/warden/core/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ intents.PolicySource = (*policystore.Store)(nil)

const unknownBudget = "00000000-0000-4000-8000-000000000001"

func newStore(t *testing.T) (*policystore.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewPool(t) // skips when DATABASE_URL is unset
	return policystore.New(pool), pool
}

func newBudget(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id, err := ledger.CreateBudget(context.Background(), pool, "test-budget", "USD", 1_000_000, "", nil)
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	return id
}

func validInput(budgetID string) policystore.Input {
	return policystore.Input{
		AgentID:             "agent-1",
		BudgetID:            budgetID,
		AllowedMerchants:    []string{"acme", "globex"},
		Currency:            "USD",
		MaxPerPaymentMinor:  100_000,
		AutoApproveMaxMinor: 5_000,
	}
}
