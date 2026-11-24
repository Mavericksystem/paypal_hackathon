package policystore_test

import (
	"context"
	"encoding/json"
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

func mustPut(t *testing.T, s *policystore.Store, changedBy string, in policystore.Input) {
	t.Helper()
	if err := s.Put(context.Background(), changedBy, in); err != nil {
		t.Fatalf("put: %v", err)
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func policyVersion(t *testing.T, pool *pgxpool.Pool, agentID, budgetID string) int64 {
	t.Helper()
	return countRows(t, pool,
		`SELECT COALESCE(MAX(version), 0) FROM policies WHERE agent_id = $1 AND budget_id = $2::uuid`,
		agentID, budgetID)
}

type policySetPayload struct {
	AgentID             string   `json:"agent_id"`
	BudgetID            string   `json:"budget_id"`
	AllowedMerchants    []string `json:"allowed_merchants"`
	Currency            string   `json:"currency"`
	MaxPerPaymentMinor  int64    `json:"max_per_payment_minor"`
	AutoApproveMaxMinor int64    `json:"auto_approve_max_minor"`
	Version             int64    `json:"version"`
}

type policySetEntry struct {
	Actor   string
	Payload policySetPayload
}

func policySetEntries(t *testing.T, pool *pgxpool.Pool) []policySetEntry {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor, payload FROM audit_log WHERE event = 'policy_set' ORDER BY seq`)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	var out []policySetEntry
	for rows.Next() {
		var actor string
		var raw []byte
		if err := rows.Scan(&actor, &raw); err != nil {
			t.Fatalf("scan audit entry: %v", err)
		}
		var p policySetPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("decode audit payload %s: %v", raw, err)
		}
		out = append(out, policySetEntry{Actor: actor, Payload: p})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit: %v", err)
	}
	return out
}
