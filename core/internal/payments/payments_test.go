package payments

import (
	"context"
	"errors"
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
func TestExecuteIsIdempotent(t *testing.T) {
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

func TestConcurrentExecuteSameIntentChargesOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.Execute(ctx, id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("execute: %v", err)
	}
	if got := e.stateOf(t, id); got != state.Captured {
		t.Fatalf("state %s, want captured", got)
	}
	if e.fake.Calls() != 1 || e.fake.Charges() != 1 {
		t.Fatalf("paypal calls=%d charges=%d, want 1 and 1", e.fake.Calls(), e.fake.Charges())
	}
	e.wantBudget(t, b, 0, 400)
	e.wantAuditOK(t)
}

func TestConcurrentExecutesNeverOverspendBudget(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 100)

	// Five intents of 30 against a limit of 100: exactly three can fit.
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = e.intentIn(t, b, 30, state.Approved)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := e.svc.Execute(ctx, id)
			if err != nil && !errors.Is(err, ErrReservationRefused) {
				t.Errorf("execute %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	captured, denied := 0, 0
	for _, id := range ids {
		switch e.stateOf(t, id) {
		case state.Captured:
			captured++
		case state.Denied:
			denied++
		default:
			t.Errorf("intent %s ended in %s", id, e.stateOf(t, id))
		}
	}
	if captured != 3 || denied != 2 {
		t.Fatalf("captured=%d denied=%d, want 3 and 2", captured, denied)
	}
	if e.fake.TotalMinor() != 90 {
		t.Fatalf("paypal moved %d, want 90", e.fake.TotalMinor())
	}
	e.wantBudget(t, b, 0, 90)
	e.wantAuditOK(t)
}

func TestDeclinedPaymentReleasesTheReservation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultDecline)

	p, err := e.svc.Execute(ctx, id)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.State != state.Failed {
		t.Fatalf("state %s, want failed", p.State)
	}
	e.wantBudget(t, b, 0, 0)
	if e.fake.Charges() != 0 {
		t.Fatalf("charges=%d, want 0", e.fake.Charges())
	}
	e.wantAuditOK(t)
}

func TestTimeoutAfterChargeIsUncertainThenReconciledAsCaptured(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutAfterCharge)

	p, err := e.svc.Execute(ctx, id)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.State != state.Uncertain {
		t.Fatalf("state %s, want uncertain", p.State)
	}
	// The money is held, not released and not committed.
	e.wantBudget(t, b, 400, 0)

	// Executing again must not touch PayPal.
	e.fake.SetFault(paypal.FaultNone)
	if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Uncertain {
		t.Fatalf("re-execute: %+v, %v", p, err)
	}
	if e.fake.Calls() != 1 {
		t.Fatalf("paypal called %d times, want 1", e.fake.Calls())
	}

	p, err = e.svc.Reconcile(ctx, id)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if p.State != state.Captured || p.PaypalOrderID == "" {
		t.Fatalf("got %+v, want captured with an order id", p)
	}
	e.wantBudget(t, b, 0, 400)
	if e.fake.Charges() != 1 {
		t.Fatalf("charges=%d, want 1", e.fake.Charges())
	}
	e.wantAuditOK(t)
}

func TestTimeoutBeforeChargeIsReconciledAsFailedOnlyAfterGrace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutBeforeCharge)

	if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Uncertain {
		t.Fatalf("execute: %+v, %v", p, err)
	}

	// PayPal has no record, but a late request could still land: do not decide yet.
	if _, err := e.svc.Reconcile(ctx, id); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("early reconcile err=%v, want ErrTooEarly", err)
	}
	e.wantBudget(t, b, 400, 0)

	e.clock.Advance(11 * time.Minute)
	p, err := e.svc.Reconcile(ctx, id)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if p.State != state.Failed {
		t.Fatalf("state %s, want failed", p.State)
	}
	e.wantBudget(t, b, 0, 0)
	if e.fake.Charges() != 0 {
		t.Fatalf("charges=%d, want 0", e.fake.Charges())
	}
	e.wantAuditOK(t)
}

func TestSweepResolvesIntentsLeftExecutingByACrash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)

	// Intent A: crashed after PayPal took the money.
	a := e.intentIn(t, b, 300, state.Executing)
	// Intent B: crashed before PayPal saw anything.
	c := e.intentIn(t, b, 200, state.Executing)
	for _, id := range []struct {
		id  string
		amt int64
	}{{a, 300}, {c, 200}} {
		tx, err := e.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := ledger.Reserve(ctx, tx, id.id, b, id.amt, "USD", e.clock.Now()); err != nil {
			t.Fatalf("reserve: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	e.fake.Seed(a, 300)
	e.wantBudget(t, b, 500, 0)

	res, err := e.svc.Sweep(ctx, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Captured != 0 || res.Failed != 0 || res.Pending != 2 {
		t.Fatalf("fresh sweep %+v, want everything pending", res)
	}

	e.clock.Advance(11 * time.Minute)
	res, err = e.svc.Sweep(ctx, time.Minute)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Errors) != 0 || res.Captured != 1 || res.Failed != 1 {
		t.Fatalf("sweep %+v, want 1 captured and 1 failed", res)
	}
	if e.stateOf(t, a) != state.Captured || e.stateOf(t, c) != state.Failed {
		t.Fatalf("states a=%s c=%s", e.stateOf(t, a), e.stateOf(t, c))
	}
	e.wantBudget(t, b, 0, 300)
	e.wantAuditOK(t)
}

func TestConcurrentReconcileResolvesOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutAfterCharge)
	if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Uncertain {
		t.Fatalf("execute: %+v, %v", p, err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := e.svc.Reconcile(ctx, id); err != nil || p.State != state.Captured {
				t.Errorf("reconcile: %+v, %v", p, err)
			}
		}()
	}
	wg.Wait()
	e.wantBudget(t, b, 0, 400) // committed once, not ten times
	e.wantAuditOK(t)
}

func (e env) countEvents(t *testing.T, id, event string) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE intent_id = $1::uuid AND event = $2`, id, event).Scan(&n)
	if err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	return n
}

func TestReconcileRefusesCaptureThatDiffersFromTheIntent(t *testing.T) {
	cases := []struct {
		name     string
		amount   int64
		currency string
	}{
		{"amount", 500, "USD"},
		{"currency", 400, "EUR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			b := e.budget(t, 1000)
			id := e.intentIn(t, b, 400, state.Approved)
			e.fake.SetFault(paypal.FaultTimeoutBeforeCharge)
			if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Uncertain {
				t.Fatalf("execute: %+v, %v", p, err)
			}
			// PayPal turns out to have captured something other than the intent.
			e.fake.SeedWith(id, tc.amount, tc.currency)

			for i := 0; i < 3; i++ {
				p, err := e.svc.Reconcile(ctx, id)
				if !errors.Is(err, ErrAmountMismatch) {
					t.Fatalf("reconcile #%d err=%v, want ErrAmountMismatch", i, err)
				}
				if p.State != state.Uncertain {
					t.Fatalf("state %s, want uncertain", p.State)
				}
			}
			e.wantBudget(t, b, 400, 0) // still held, nothing committed
			if n := e.countEvents(t, id, "reconcile_amount_mismatch"); n != 1 {
				t.Fatalf("mismatch audit entries = %d, want exactly 1 across repeated reconciles", n)
			}
			e.wantAuditOK(t)
		})
	}
}

func TestSweepCountsMismatchesInsteadOfCommitting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutBeforeCharge)
	if _, err := e.svc.Execute(ctx, id); err != nil {
		t.Fatalf("execute: %v", err)
	}
	e.fake.Seed(id, 999)

	res, err := e.svc.Sweep(ctx, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Mismatches != 1 || res.Captured != 0 || len(res.Errors) != 0 {
		t.Fatalf("sweep %+v, want 1 mismatch and nothing else", res)
	}
	e.wantBudget(t, b, 400, 0)
}

func TestCaptureReportedWithWrongAmountIsNotCommitted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultOverCharge)

	p, err := e.svc.Execute(ctx, id)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.State != state.Uncertain {
		t.Fatalf("state %s, want uncertain", p.State)
	}
	e.wantBudget(t, b, 400, 0)

	if _, err := e.svc.Reconcile(ctx, id); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("reconcile err=%v, want ErrAmountMismatch", err)
	}
	e.wantBudget(t, b, 400, 0)
	e.wantAuditOK(t)
}

func TestLateCaptureAfterNotFoundIsFlaggedOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutBeforeCharge)

	if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Uncertain {
		t.Fatalf("execute: %+v, %v", p, err)
	}
	e.clock.Advance(11 * time.Minute)
	if p, err := e.svc.Reconcile(ctx, id); err != nil || p.State != state.Failed {
		t.Fatalf("reconcile: %+v, %v", p, err)
	}
	e.wantBudget(t, b, 0, 0)

	// The delayed request lands at PayPal after the money was released here.
	e.fake.Seed(id, 400)

	res, err := e.svc.Sweep(ctx, time.Minute)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Conflicts != 1 || len(res.Errors) != 0 {
		t.Fatalf("sweep %+v, want exactly 1 conflict", res)
	}
	if got := e.stateOf(t, id); got != state.Failed {
		t.Fatalf("state %s, a terminal state must not be rewritten", got)
	}
	if n := e.countEvents(t, id, "payment_conflict"); n != 1 {
		t.Fatalf("conflict entries = %d, want 1", n)
	}

	// Already flagged, so later sweeps leave it alone.
	lookups := e.fake.Lookups()
	res, err = e.svc.Sweep(ctx, time.Minute)
	if err != nil || res.Conflicts != 0 {
		t.Fatalf("second sweep %+v, %v", res, err)
	}
	if e.fake.Lookups() != lookups {
		t.Fatalf("flagged intent was looked up again")
	}
	if n := e.countEvents(t, id, "payment_conflict"); n != 1 {
		t.Fatalf("conflict entries = %d after second sweep, want 1", n)
	}
	e.wantAuditOK(t)
}

func TestDeclinedAndNeverRecheckedIntentsAreNotLookedUp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultDecline)
	if p, err := e.svc.Execute(ctx, id); err != nil || p.State != state.Failed {
		t.Fatalf("execute: %+v, %v", p, err)
	}
	if _, err := e.svc.Sweep(ctx, 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if p, err := e.svc.Reconcile(ctx, id); err != nil || p.State != state.Failed {
		t.Fatalf("reconcile: %+v, %v", p, err)
	}
	if e.fake.Lookups() != 0 {
		t.Fatalf("a declined intent was looked up %d times, want 0", e.fake.Lookups())
	}
}

func TestRecheckStopsAfterTheWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)
	id := e.intentIn(t, b, 400, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutBeforeCharge)
	if _, err := e.svc.Execute(ctx, id); err != nil {
		t.Fatalf("execute: %v", err)
	}
	e.clock.Advance(11 * time.Minute)
	if p, err := e.svc.Reconcile(ctx, id); err != nil || p.State != state.Failed {
		t.Fatalf("reconcile: %+v, %v", p, err)
	}

	e.clock.Advance(e.svc.RecheckWindow + time.Minute)
	e.fake.Seed(id, 400)
	res, err := e.svc.Sweep(ctx, time.Minute)
	if err != nil || res.Conflicts != 0 {
		t.Fatalf("sweep %+v, %v: window is over, nothing should be looked up", res, err)
	}
}

// finish must refuse to flip a terminal intent and must say so in the audit log.
func TestFinishRecordsConflictInBothDirections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.budget(t, 1000)

	declined := e.intentIn(t, b, 100, state.Approved)
	e.fake.SetFault(paypal.FaultDecline)
	if p, err := e.svc.Execute(ctx, declined); err != nil || p.State != state.Failed {
		t.Fatalf("execute declined: %+v, %v", p, err)
	}
	e.fake.SetFault(paypal.FaultNone)
	captured := e.intentIn(t, b, 200, state.Approved)
	if p, err := e.svc.Execute(ctx, captured); err != nil || p.State != state.Captured {
		t.Fatalf("execute captured: %+v, %v", p, err)
	}

	order := "ORDER-LATE"
	for i := 0; i < 2; i++ { // repeating must not add entries
		_, err := e.svc.finish(ctx, declined, state.Captured, &order, "test", "payment_captured", nil)
		if !errors.Is(err, ErrPaymentConflict) {
			t.Fatalf("failed then captured: err=%v, want ErrPaymentConflict", err)
		}
		_, err = e.svc.finish(ctx, captured, state.Failed, nil, "test", "payment_declined", nil)
		if !errors.Is(err, ErrPaymentConflict) {
			t.Fatalf("captured then failed: err=%v, want ErrPaymentConflict", err)
		}
	}
	for _, id := range []string{declined, captured} {
		if n := e.countEvents(t, id, "payment_conflict"); n != 1 {
			t.Fatalf("intent %s has %d conflict entries, want 1", id, n)
		}
	}
	if e.stateOf(t, declined) != state.Failed || e.stateOf(t, captured) != state.Captured {
		t.Fatalf("terminal states were rewritten")
	}
	e.wantBudget(t, b, 0, 200)
	e.wantAuditOK(t)
}
