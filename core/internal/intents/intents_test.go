package intents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/ai"
	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policy"
	"github.com/Mavericksystem/warden/core/internal/state"
	"github.com/Mavericksystem/warden/core/internal/testutil"
)

// ---------- helpers ----------

type fakeChecker struct {
	mu    sync.Mutex
	calls int
	got   []CheckInput
	res   CheckResult
	err   error
	block bool // wait for the context to end, like a hung network call
}

func (f *fakeChecker) Check(ctx context.Context, in CheckInput) (CheckResult, error) {
	f.mu.Lock()
	f.calls++
	f.got = append(f.got, in)
	res, err, block := f.res, f.err, f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return CheckResult{}, ctx.Err()
	}
	return res, err
}

func (f *fakeChecker) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func verdictChecker(v ai.Verdict, reason string) *fakeChecker {
	return &fakeChecker{res: CheckResult{
		Decision:      ai.Decision{Verdict: v, Reason: reason},
		Model:         "fake-model",
		PromptVersion: "v0",
	}}
}

var testPolicy = StaticPolicy(policy.Policy{
	AllowedMerchants: []string{"acme"},
	Currency:         "USD",
	MaxPerPayment:    100_000,
	AutoApproveMax:   5_000,
})

func newBudget(t *testing.T, pool *pgxpool.Pool, limit int64) string {
	t.Helper()
	id, err := ledger.CreateBudget(context.Background(), pool, "test-budget", "USD", limit, "", nil)
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	return id
}

func input(budgetID, key string, amount int64) CreateInput {
	return CreateInput{
		AgentID:        "agent-1",
		BudgetID:       budgetID,
		IdempotencyKey: key,
		AmountMinor:    amount,
		Currency:       "USD",
		Merchant:       "acme",
		Recipient:      "acme-payments",
		Goal:           "buy API credits",
	}
}

func eventsFor(t *testing.T, pool *pgxpool.Pool, intentID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT event FROM audit_log WHERE intent_id = $1::uuid ORDER BY seq`, intentID)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	return out
}

func wantEvents(t *testing.T, pool *pgxpool.Pool, intentID string, want ...string) {
	t.Helper()
	got := eventsFor(t, pool, intentID)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit events = %v, want %v", got, want)
	}
}

func verifyChain(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	res, err := audit.Verify(context.Background(), pool)
	if err != nil {
		t.Fatalf("verify audit: %v", err)
	}
	if !res.OK {
		t.Fatalf("audit chain broken at seq %d: %s", res.BrokenAtSeq, res.Reason)
	}
}

func wantState(t *testing.T, got Intent, want state.State) {
	t.Helper()
	if got.State != want {
		t.Fatalf("state = %s, want %s", got.State, want)
	}
}

// ---------- Create: decisions ----------

func TestCreateApproved(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "looks fine")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	got, err := svc.Create(context.Background(), input(budget, "k1", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, got, state.Approved)
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2 (created -> policy_checked -> approved)", got.Version)
	}
	if chk.Calls() != 1 {
		t.Fatalf("AI calls = %d, want 1", chk.Calls())
	}
	wantEvents(t, pool, got.ID, "intent_created", "policy_checked", "ai_checked", "intent_approved")
	verifyChain(t, pool)
}

func TestCreateAIReceivesOnlyAllowedFields(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "ok")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	got, err := svc.Create(context.Background(), input(budget, "k-fields", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(chk.got) != 1 {
		t.Fatalf("checker saw %d inputs, want 1", len(chk.got))
	}
	in := chk.got[0]
	if in.IntentID != got.ID || in.Goal != "buy API credits" || in.Merchant != "acme" ||
		in.Recipient != "acme-payments" || in.AmountMinor != 1_000 || in.Currency != "USD" {
		t.Fatalf("unexpected checker input: %+v", in)
	}
}

func TestCreateRulesDenyNeverCallsAI(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CreateInput)
		limit  int64
	}{
		{"merchant not allowed", func(in *CreateInput) { in.Merchant = "evil-corp" }, 1_000_000},
		{"currency not allowed", func(in *CreateInput) {
			in.Currency = "EUR"
		}, 1_000_000},
		{"over per-payment limit", func(in *CreateInput) { in.AmountMinor = 200_000 }, 10_000_000},
		{"over budget", func(in *CreateInput) { in.AmountMinor = 1_000 }, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testutil.NewPool(t)
			// An AI that would happily allow everything: it must never be asked.
			chk := verdictChecker(ai.Allow, "fine")
			svc := New(pool, testPolicy, chk)
			budget := newBudget(t, pool, tc.limit)

			in := input(budget, "k-deny", 1_000)
			tc.mutate(&in)
			got, err := svc.Create(context.Background(), in)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			wantState(t, got, state.Denied)
			if chk.Calls() != 0 {
				t.Fatalf("AI was called %d times for a rules deny", chk.Calls())
			}
			wantEvents(t, pool, got.ID, "intent_created", "policy_checked", "intent_denied")
			verifyChain(t, pool)
		})
	}
}

func TestCreateAboveAutoApproveNeedsHuman(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	budget := newBudget(t, pool, 1_000_000)

	got, err := svc.Create(context.Background(), input(budget, "k-big", 9_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, got, state.PendingApproval)
	wantEvents(t, pool, got.ID, "intent_created", "policy_checked", "ai_checked", "intent_pending_approval")
}

func TestCreateAIVerdicts(t *testing.T) {
	cases := []struct {
		verdict ai.Verdict
		want    state.State
	}{
		{ai.Allow, state.Approved},
		{ai.Escalate, state.PendingApproval},
		{ai.Deny, state.Denied},
	}
	for _, tc := range cases {
		t.Run(string(tc.verdict), func(t *testing.T) {
			pool := testutil.NewPool(t)
			svc := New(pool, testPolicy, verdictChecker(tc.verdict, "because"))
			budget := newBudget(t, pool, 1_000_000)

			got, err := svc.Create(context.Background(), input(budget, "k-ai", 1_000))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			wantState(t, got, tc.want)
			verifyChain(t, pool)
		})
	}
}

// ---------- Create: fail closed ----------

func TestCreateAIErrorFailsClosed(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := &fakeChecker{err: errors.New("upstream 503")}
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	got, err := svc.Create(context.Background(), input(budget, "k-err", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, got, state.PendingApproval)
	wantEvents(t, pool, got.ID, "intent_created", "policy_checked", "ai_checked", "intent_pending_approval")

	var errMsg string
	err = pool.QueryRow(context.Background(),
		`SELECT payload->>'error' FROM audit_log WHERE intent_id = $1::uuid AND event = 'ai_checked'`,
		got.ID).Scan(&errMsg)
	if err != nil {
		t.Fatalf("read ai_checked payload: %v", err)
	}
	if !strings.Contains(errMsg, "upstream 503") {
		t.Fatalf("ai_checked payload error = %q, want it to record the failure", errMsg)
	}
}

func TestCreateNilCheckerFailsClosed(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, nil)
	budget := newBudget(t, pool, 1_000_000)

	got, err := svc.Create(context.Background(), input(budget, "k-nil", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, got, state.PendingApproval)
}

func TestCreateAITimeoutFailsClosed(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := &fakeChecker{block: true}
	svc := New(pool, testPolicy, chk)
	svc.AITimeout = 50 * time.Millisecond
	budget := newBudget(t, pool, 1_000_000)

	start := time.Now()
	got, err := svc.Create(context.Background(), input(budget, "k-slow", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("create took %s, the AI timeout did not bound it", elapsed)
	}
	wantState(t, got, state.PendingApproval)
}

type brokenPolicy struct{}

func (brokenPolicy) For(context.Context, string, string) (policy.Policy, error) {
	return policy.Policy{}, errors.New("policy store down")
}

func TestCreatePolicyFailureDecidesNothing(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "fine")
	svc := New(pool, brokenPolicy{}, chk)
	budget := newBudget(t, pool, 1_000_000)

	_, err := svc.Create(context.Background(), input(budget, "k-pol", 1_000))
	if err == nil {
		t.Fatal("expected an error when the policy cannot be loaded")
	}
	if chk.Calls() != 0 {
		t.Fatalf("AI was called %d times without a policy", chk.Calls())
	}

	// The intent is stored but undecided, so a retry can finish the job.
	var st string
	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM intents WHERE idempotency_key = 'k-pol'`).Scan(&st); err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if st != string(state.Created) {
		t.Fatalf("state = %s, want created", st)
	}

	svc2 := New(pool, testPolicy, chk)
	got, err := svc2.Create(context.Background(), input(budget, "k-pol", 1_000))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	wantState(t, got, state.Approved)
}

// ---------- Create: idempotency and validation ----------

func TestCreateIdempotentRetry(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "fine")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	first, err := svc.Create(context.Background(), input(budget, "k-same", 1_000))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := svc.Create(context.Background(), input(budget, "k-same", 1_000))
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry produced a new intent: %s vs %s", first.ID, second.ID)
	}
	wantState(t, second, state.Approved)
	if chk.Calls() != 1 {
		t.Fatalf("AI calls = %d, want 1 (a retry must not re-check)", chk.Calls())
	}
	wantEvents(t, pool, first.ID, "intent_created", "policy_checked", "ai_checked", "intent_approved")
}

func TestCreateIdempotencyConflict(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	budget := newBudget(t, pool, 1_000_000)

	if _, err := svc.Create(context.Background(), input(budget, "k-conflict", 1_000)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(context.Background(), input(budget, "k-conflict", 2_000))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateSameKeyDifferentAgentsAreIndependent(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	budget := newBudget(t, pool, 1_000_000)

	a := input(budget, "shared-key", 1_000)
	b := input(budget, "shared-key", 1_000)
	b.AgentID = "agent-2"

	ia, err := svc.Create(context.Background(), a)
	if err != nil {
		t.Fatalf("agent 1: %v", err)
	}
	ib, err := svc.Create(context.Background(), b)
	if err != nil {
		t.Fatalf("agent 2: %v", err)
	}
	if ia.ID == ib.ID {
		t.Fatal("two agents with the same key must get different intents")
	}
}

func TestCreateInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{"missing agent", func(in *CreateInput) { in.AgentID = " " }},
		{"missing budget", func(in *CreateInput) { in.BudgetID = "" }},
		{"missing key", func(in *CreateInput) { in.IdempotencyKey = "" }},
		{"zero amount", func(in *CreateInput) { in.AmountMinor = 0 }},
		{"negative amount", func(in *CreateInput) { in.AmountMinor = -5 }},
		{"lower case currency", func(in *CreateInput) { in.Currency = "usd" }},
		{"short currency", func(in *CreateInput) { in.Currency = "US" }},
		{"missing merchant", func(in *CreateInput) { in.Merchant = "" }},
		{"missing recipient", func(in *CreateInput) { in.Recipient = "" }},
	}
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "fine")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := input(budget, "k-invalid", 1_000)
			tc.mutate(&in)
			_, err := svc.Create(context.Background(), in)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
	if chk.Calls() != 0 {
		t.Fatalf("AI was called %d times for invalid input", chk.Calls())
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM intents`).Scan(&n); err != nil {
		t.Fatalf("count intents: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d intents stored for invalid input", n)
	}
}

func TestCreateBudgetErrors(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))

	t.Run("unknown budget", func(t *testing.T) {
		_, err := svc.Create(context.Background(),
			input("00000000-0000-0000-0000-000000000000", "k-nobudget", 1_000))
		if !errors.Is(err, ledger.ErrBudgetNotFound) {
			t.Fatalf("err = %v, want ErrBudgetNotFound", err)
		}
	})
	t.Run("malformed budget id", func(t *testing.T) {
		_, err := svc.Create(context.Background(), input("not-a-uuid", "k-baduuid", 1_000))
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}

// ---------- Create: resume and concurrency ----------

func TestCreateResumesUndecidedIntent(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "fine")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	// Simulate a crash between the insert and the decision.
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO intents (agent_id, budget_id, state, amount_minor, currency, merchant, recipient, goal, idempotency_key)
		 VALUES ('agent-1', $1::uuid, 'created', 1000, 'USD', 'acme', 'acme-payments', 'buy API credits', 'k-resume')
		 RETURNING id::text`, budget).Scan(&id)
	if err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	got, err := svc.Create(context.Background(), input(budget, "k-resume", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID != id {
		t.Fatalf("resume created a new intent: %s vs %s", got.ID, id)
	}
	wantState(t, got, state.Approved)
}

func TestCreateConcurrentSameKey(t *testing.T) {
	pool := testutil.NewPool(t)
	chk := verdictChecker(ai.Allow, "fine")
	svc := New(pool, testPolicy, chk)
	budget := newBudget(t, pool, 1_000_000)

	const workers = 8
	ids := make([]string, workers)
	states := make([]state.State, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, err := svc.Create(context.Background(), input(budget, "k-race", 1_000))
			ids[i], states[i], errs[i] = got.ID, got.State, err
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("worker %d got intent %s, worker 0 got %s", i, ids[i], ids[0])
		}
	}

	// Several callers may have run the AI check, but exactly one decision was recorded.
	final, err := svc.Get(context.Background(), ids[0])
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wantState(t, final, state.Approved)
	wantEvents(t, pool, ids[0], "intent_created", "policy_checked", "ai_checked", "intent_approved")

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM intents`).Scan(&n); err != nil {
		t.Fatalf("count intents: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d intents stored, want 1", n)
	}
	verifyChain(t, pool)
}

// ---------- Get ----------

func TestGetNotFound(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, nil)

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "garbage"} {
		if _, err := svc.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) err = %v, want ErrNotFound", id, err)
		}
	}
}

// ---------- Decide ----------

func pendingIntent(t *testing.T, svc *Service, pool *pgxpool.Pool, key string) Intent {
	t.Helper()
	budget := newBudget(t, pool, 1_000_000)
	got, err := svc.Create(context.Background(), input(budget, key, 9_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, got, state.PendingApproval)
	return got
}

func TestDecideApprove(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	in := pendingIntent(t, svc, pool, "k-approve")

	got, err := svc.Decide(context.Background(), in.ID, "alice", true, "checked the invoice")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	wantState(t, got, state.Approved)
	wantEvents(t, pool, in.ID,
		"intent_created", "policy_checked", "ai_checked", "intent_pending_approval", "human_approved")

	var actor string
	if err := pool.QueryRow(context.Background(),
		`SELECT actor FROM audit_log WHERE intent_id = $1::uuid AND event = 'human_approved'`,
		in.ID).Scan(&actor); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	if actor != "human:alice" {
		t.Fatalf("actor = %q, want human:alice", actor)
	}
	verifyChain(t, pool)
}

func TestDecideReject(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	in := pendingIntent(t, svc, pool, "k-reject")

	got, err := svc.Decide(context.Background(), in.ID, "bob", false, "not expected")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	wantState(t, got, state.Rejected)
}

func TestDecideRepeatIsNoOpOppositeIsRefused(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	in := pendingIntent(t, svc, pool, "k-repeat")

	first, err := svc.Decide(context.Background(), in.ID, "alice", true, "")
	if err != nil {
		t.Fatalf("first decide: %v", err)
	}
	again, err := svc.Decide(context.Background(), in.ID, "alice", true, "")
	if err != nil {
		t.Fatalf("repeat decide: %v", err)
	}
	if again.Version != first.Version {
		t.Fatalf("repeat changed version %d -> %d", first.Version, again.Version)
	}
	if n := len(eventsFor(t, pool, in.ID)); n != 5 {
		t.Fatalf("%d audit events after a repeat, want 5", n)
	}

	_, err = svc.Decide(context.Background(), in.ID, "mallory", false, "changed my mind")
	if !errors.Is(err, ErrNotPending) {
		t.Fatalf("opposite decision err = %v, want ErrNotPending", err)
	}
}

func TestDecideOnlyWorksOnPendingIntents(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	budget := newBudget(t, pool, 1_000_000)

	approved, err := svc.Create(context.Background(), input(budget, "k-auto", 1_000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantState(t, approved, state.Approved)
	if _, err := svc.Decide(context.Background(), approved.ID, "alice", false, ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("reject of auto-approved intent err = %v, want ErrNotPending", err)
	}

	bad := input(budget, "k-denied", 1_000)
	bad.Merchant = "evil-corp"
	denied, err := svc.Create(context.Background(), bad)
	if err != nil {
		t.Fatalf("create denied: %v", err)
	}
	wantState(t, denied, state.Denied)
	if _, err := svc.Decide(context.Background(), denied.ID, "alice", true, ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve of denied intent err = %v, want ErrNotPending", err)
	}
}

func TestDecideValidation(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	in := pendingIntent(t, svc, pool, "k-validate")

	if _, err := svc.Decide(context.Background(), in.ID, "  ", true, ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank approver err = %v, want ErrInvalidInput", err)
	}
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "garbage"} {
		if _, err := svc.Decide(context.Background(), id, "alice", true, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Decide(%q) err = %v, want ErrNotFound", id, err)
		}
	}
}

func TestDecideConcurrentOppositeDecisions(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := New(pool, testPolicy, verdictChecker(ai.Allow, "fine"))
	in := pendingIntent(t, svc, pool, "k-decide-race")

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, approve := range []bool{true, false} {
		wg.Add(1)
		go func(i int, approve bool) {
			defer wg.Done()
			<-start
			_, results[i] = svc.Decide(context.Background(), in.ID, fmt.Sprintf("human-%d", i), approve, "")
		}(i, approve)
	}
	close(start)
	wg.Wait()

	wins, refused := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrNotPending):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || refused != 1 {
		t.Fatalf("wins=%d refused=%d, want exactly one of each", wins, refused)
	}
	verifyChain(t, pool)
}
