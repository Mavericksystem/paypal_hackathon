package payments

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/paypal"
	"github.com/Mavericksystem/warden/core/internal/state"
	"github.com/Mavericksystem/warden/core/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	pool  *pgxpool.Pool
	fake  *paypal.Fake
	svc   *Service
	clock *clock
}

func newEnv(t *testing.T) env {
	t.Helper()
	pool := testutil.NewPool(t)
	fake := paypal.NewFake()
	clk := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	svc := New(pool, fake)
	svc.Now = clk.Now
	svc.InFlightGrace = time.Minute
	svc.NotFoundGrace = 10 * time.Minute
	return env{pool: pool, fake: fake, svc: svc, clock: clk}
}

func (e env) budget(t *testing.T, limit int64) string {
	t.Helper()
	id, err := ledger.CreateBudget(context.Background(), e.pool, "test", "USD", limit, "", nil)
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	return id
}

func (e env) intentIn(t *testing.T, budgetID string, amount int64, st state.State) string {
	t.Helper()
	id := testutil.InsertIntent(t, e.pool, budgetID, amount, "USD")
	_, err := e.pool.Exec(context.Background(),
		`UPDATE intents SET state = $2, updated_at = $3 WHERE id = $1::uuid`, id, string(st), e.clock.Now())
	if err != nil {
		t.Fatalf("set intent state: %v", err)
	}
	return id
}

func (e env) stateOf(t *testing.T, id string) state.State {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT state FROM intents WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return state.State(s)
}

func (e env) snap(t *testing.T, budgetID string) ledger.Snapshot {
	t.Helper()
	s, err := ledger.GetSnapshot(context.Background(), e.pool, budgetID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return s
}

func (e env) wantBudget(t *testing.T, budgetID string, reserved, committed int64) {
	t.Helper()
	s := e.snap(t, budgetID)
	if s.Reserved != reserved || s.Committed != committed {
		t.Fatalf("budget reserved=%d committed=%d, want reserved=%d committed=%d",
			s.Reserved, s.Committed, reserved, committed)
	}
}

func (e env) wantAuditOK(t *testing.T) {
	t.Helper()
	res, err := audit.Verify(context.Background(), e.pool)
	if err != nil {
		t.Fatalf("verify audit: %v", err)
	}
	if !res.OK {
		t.Fatalf("audit chain broken at seq %d: %s", res.BrokenAtSeq, res.Reason)
	}
}

func TestExecuteHappyPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)

	p, err := e.svc.Execute(ctx, id)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.State != state.Captured || p.PaypalOrderID == "" {
		t.Fatalf("got %+v, want captured with an order id", p)
	}
	e.wantBudget(t, b, 0, 400)
	if e.fake.Charges() != 1 || e.fake.TotalMinor() != 400 {
		t.Fatalf("charges=%d total=%d, want 1 and 400", e.fake.Charges(), e.fake.TotalMinor())
	}
	e.wantAuditOK(t)
}
unc TestExecuteIsIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)

	for i := 0; i < 3; i++ {
		p, err := e.svc.Execute(ctx, id)
		if err != nil || p.State != state.Captured {
			t.Fatalf("execute #%d: %+v, %v", i, p, err)
		}
	}
	if e.fake.Calls() != 1 {
		t.Fatalf("paypal called %d times, want 1", e.fake.Calls())
	}
	e.wantBudget(t, b, 0, 400)
}

func TestExecuteRefusesIntentsThatAreNotApproved(t *testing.T) {
	e := newEnv(t)
	b := e.budget(t, 1000)
	for _, st := range []state.State{state.Created, state.PolicyChecked, state.PendingApproval, state.Rejected} {
		id := e.intentIn(t, b, 100, st)
		_, err := e.svc.Execute(context.Background(), id)
		if !errors.Is(err, ErrNotExecutable) {
			t.Fatalf("%s: err=%v, want ErrNotExecutable", st, err)
		}
	}
	if e.fake.Calls() != 0 {
		t.Fatalf("paypal was called %d times for non-approved intents", e.fake.Calls())
	}
	e.wantBudget(t, b, 0, 0)
}