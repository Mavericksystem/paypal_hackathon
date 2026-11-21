package payments

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/paypal"
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
