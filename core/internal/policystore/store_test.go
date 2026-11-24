package policystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/intents"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policy"
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

func verifyChain(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	res, err := audit.Verify(context.Background(), pool)
	if err != nil {
		t.Fatalf("verify audit: %v", err)
	}
	if !res.OK {
		t.Fatalf("audit chain broken at seq %d: %s", res.BrokenAtSeq, res.Reason)
	}
	return res.Entries
}

func TestForMissingPolicyReturnsErrNoPolicy(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	budget := newBudget(t, pool)
	otherBudget := newBudget(t, pool)

	if _, err := s.For(ctx, "agent-1", budget); !errors.Is(err, policystore.ErrNoPolicy) {
		t.Fatalf("empty table: want ErrNoPolicy, got %v", err)
	}

	mustPut(t, s, "alice", validInput(budget))

	// A policy belongs to one agent on one budget. It must not leak to another agent or budget.
	if _, err := s.For(ctx, "agent-2", budget); !errors.Is(err, policystore.ErrNoPolicy) {
		t.Fatalf("other agent: want ErrNoPolicy, got %v", err)
	}
	if _, err := s.For(ctx, "agent-1", otherBudget); !errors.Is(err, policystore.ErrNoPolicy) {
		t.Fatalf("other budget: want ErrNoPolicy, got %v", err)
	}
}

func TestForMalformedBudgetIDReturnsErrNoPolicy(t *testing.T) {
	s, _ := newStore(t)
	for _, id := range []string{"", "not-a-uuid", "12345", "00000000-0000-4000-8000-00000000000z"} {
		id := id
		t.Run(id, func(t *testing.T) {
			if _, err := s.For(context.Background(), "agent-1", id); !errors.Is(err, policystore.ErrNoPolicy) {
				t.Fatalf("want ErrNoPolicy, got %v", err)
			}
		})
	}
}

func TestPutThenForRoundTrip(t *testing.T) {
	s, pool := newStore(t)
	budget := newBudget(t, pool)
	in := validInput(budget)
	mustPut(t, s, "alice", in)

	got, err := s.For(context.Background(), in.AgentID, budget)
	if err != nil {
		t.Fatalf("for: %v", err)
	}
	want := policy.Policy{
		AllowedMerchants: []string{"acme", "globex"},
		Currency:         "USD",
		MaxPerPayment:    100_000,
		AutoApproveMax:   5_000,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
	if v := policyVersion(t, pool, in.AgentID, budget); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
}

func TestSecondPutReplacesPolicyAndBumpsVersion(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	budget := newBudget(t, pool)

	first := validInput(budget)
	mustPut(t, s, "alice", first)

	second := validInput(budget)
	second.AllowedMerchants = []string{"initech"}
	second.MaxPerPaymentMinor = 20_000
	second.AutoApproveMaxMinor = 1_000
	mustPut(t, s, "bob", second)

	got, err := s.For(ctx, first.AgentID, budget)
	if err != nil {
		t.Fatalf("for: %v", err)
	}
	want := policy.Policy{
		AllowedMerchants: []string{"initech"},
		Currency:         "USD",
		MaxPerPayment:    20_000,
		AutoApproveMax:   1_000,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy after replace = %+v, want %+v", got, want)
	}
	if v := policyVersion(t, pool, first.AgentID, budget); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM policies`); n != 1 {
		t.Fatalf("policies rows = %d, want 1 (replace, not insert)", n)
	}

	mustPut(t, s, "bob", second)
	if v := policyVersion(t, pool, first.AgentID, budget); v != 3 {
		t.Fatalf("version after identical put = %d, want 3", v)
	}
}

func TestPutUnknownBudgetReturnsBudgetNotFound(t *testing.T) {
	s, pool := newStore(t)
	in := validInput(unknownBudget)

	err := s.Put(context.Background(), "alice", in)
	if !errors.Is(err, ledger.ErrBudgetNotFound) {
		t.Fatalf("want ledger.ErrBudgetNotFound, got %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM policies`); n != 0 {
		t.Fatalf("policies rows = %d, want 0", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

func TestPutMalformedBudgetIDIsInvalid(t *testing.T) {
	s, pool := newStore(t)
	in := validInput("not-a-uuid")

	err := s.Put(context.Background(), "alice", in)
	if !errors.Is(err, policystore.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

func TestPutValidation(t *testing.T) {
	s, pool := newStore(t)
	budget := newBudget(t, pool)

	cases := []struct {
		name      string
		changedBy string
		mutate    func(*policystore.Input)
	}{
		{"missing changed_by", "", func(*policystore.Input) {}},
		{"blank changed_by", "  ", func(*policystore.Input) {}},
		{"missing agent", "alice", func(in *policystore.Input) { in.AgentID = "" }},
		{"blank agent", "alice", func(in *policystore.Input) { in.AgentID = "  " }},
		{"missing budget", "alice", func(in *policystore.Input) { in.BudgetID = "" }},
		{"lower case currency", "alice", func(in *policystore.Input) { in.Currency = "usd" }},
		{"two letter currency", "alice", func(in *policystore.Input) { in.Currency = "US" }},
		{"four letter currency", "alice", func(in *policystore.Input) { in.Currency = "USDX" }},
		{"empty currency", "alice", func(in *policystore.Input) { in.Currency = "" }},
		{"symbol in currency", "alice", func(in *policystore.Input) { in.Currency = "U$D" }},
		{"digit currency", "alice", func(in *policystore.Input) { in.Currency = "123" }},
		{"negative per payment max", "alice", func(in *policystore.Input) { in.MaxPerPaymentMinor = -1 }},
		{"negative auto approve max", "alice", func(in *policystore.Input) { in.AutoApproveMaxMinor = -1 }},
		{"empty merchant name", "alice", func(in *policystore.Input) { in.AllowedMerchants = []string{"acme", ""} }},
		{"blank merchant name", "alice", func(in *policystore.Input) { in.AllowedMerchants = []string{"  "} }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			in := validInput(budget)
			tc.mutate(&in)
			err := s.Put(context.Background(), tc.changedBy, in)
			if !errors.Is(err, policystore.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}

	if n := countRows(t, pool, `SELECT count(*) FROM policies`); n != 0 {
		t.Fatalf("policies rows = %d, want 0", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

func TestPutAcceptsZeroLimitsAndEmptyMerchantList(t *testing.T) {
	s, pool := newStore(t)
	budget := newBudget(t, pool)
	in := validInput(budget)
	in.AllowedMerchants = nil
	in.MaxPerPaymentMinor = 0
	in.AutoApproveMaxMinor = 0
	mustPut(t, s, "alice", in)

	got, err := s.For(context.Background(), in.AgentID, budget)
	if err != nil {
		t.Fatalf("for: %v", err)
	}
	if len(got.AllowedMerchants) != 0 || got.MaxPerPayment != 0 || got.AutoApproveMax != 0 {
		t.Fatalf("policy = %+v, want empty merchants and zero limits", got)
	}

	res := policy.Evaluate(got, policy.Request{
		Merchant:        "acme",
		Amount:          100,
		Currency:        "USD",
		BudgetRemaining: 1_000,
	})
	if res.Verdict != policy.Deny || res.Code != "merchant_not_allowed" {
		t.Fatalf("empty merchant list must deny every merchant, got %+v", res)
	}
}

func TestEachPutWritesOneAuditEntryAndChainVerifies(t *testing.T) {
	s, pool := newStore(t)
	budget := newBudget(t, pool)

	a1 := validInput(budget)
	mustPut(t, s, "alice", a1)

	a1v2 := validInput(budget)
	a1v2.MaxPerPaymentMinor = 70_000
	mustPut(t, s, "bob", a1v2)

	a2 := validInput(budget)
	a2.AgentID = "agent-2"
	mustPut(t, s, "alice", a2)

	entries := policySetEntries(t, pool)
	if len(entries) != 3 {
		t.Fatalf("policy_set entries = %d, want 3", len(entries))
	}
	if n := countRows(t, pool, `SELECT count(*) FROM audit_log WHERE event = 'policy_set' AND intent_id IS NULL`); n != 3 {
		t.Fatalf("policy_set entries without intent_id = %d, want 3", n)
	}

	wantActors := []string{"admin:alice", "admin:bob", "admin:alice"}
	wantVersions := []int64{1, 2, 1}
	for i, e := range entries {
		if e.Actor != wantActors[i] {
			t.Errorf("entry %d actor = %q, want %q", i, e.Actor, wantActors[i])
		}
		if e.Payload.Version != wantVersions[i] {
			t.Errorf("entry %d version = %d, want %d", i, e.Payload.Version, wantVersions[i])
		}
		if e.Payload.BudgetID != budget || e.Payload.Currency != "USD" {
			t.Errorf("entry %d payload = %+v", i, e.Payload)
		}
	}
	// The second entry carries the full new policy, not a diff.
	if got := entries[1].Payload; got.MaxPerPaymentMinor != 70_000 ||
		got.AutoApproveMaxMinor != 5_000 ||
		!reflect.DeepEqual(got.AllowedMerchants, []string{"acme", "globex"}) {
		t.Errorf("entry 1 payload = %+v", got)
	}

	if n := verifyChain(t, pool); n != 3 {
		t.Fatalf("chain entries = %d, want 3", n)
	}
}


func TestFailedAuditAppendLeavesPolicyUnchanged(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	budget := newBudget(t, pool)

	original := validInput(budget)
	mustPut(t, s, "alice", original)
	wantPolicy, err := s.For(ctx, original.AgentID, budget)
	if err != nil {
		t.Fatalf("for: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`ALTER TABLE audit_log ADD CONSTRAINT block_policy_set CHECK (event <> 'policy_set') NOT VALID`); err != nil {
		t.Fatalf("install audit failure: %v", err)
	}

	wantCheckViolation := func(t *testing.T, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("want check violation 23514 from the audit insert, got %v", err)
		}
	}

	// Replace of an existing policy.
	changed := validInput(budget)
	changed.AllowedMerchants = []string{"initech"}
	changed.MaxPerPaymentMinor = 1
	wantCheckViolation(t, s.Put(ctx, "alice", changed))

	got, err := s.For(ctx, original.AgentID, budget)
	if err != nil {
		t.Fatalf("for after failed replace: %v", err)
	}
	if !reflect.DeepEqual(got, wantPolicy) {
		t.Fatalf("policy changed despite failed audit: %+v, want %+v", got, wantPolicy)
	}
	if v := policyVersion(t, pool, original.AgentID, budget); v != 1 {
		t.Fatalf("version = %d after failed replace, want 1", v)
	}