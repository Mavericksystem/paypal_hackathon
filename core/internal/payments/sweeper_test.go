package payments

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mavericksystem/warden/core/internal/paypal"
	"github.com/Mavericksystem/warden/core/internal/state"
)

type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// startSweeper runs the sweeper and returns a stop function that cancels it and
// fails the test if it does not return promptly.
func startSweeper(t *testing.T, e env, cfg SweeperConfig) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.svc.RunSweeper(ctx, cfg)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("sweeper did not stop after cancel")
		}
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (e env) uncertainIntent(t *testing.T, budgetID string, amount int64) string {
	t.Helper()
	id := e.intentIn(t, budgetID, amount, state.Approved)
	e.fake.SetFault(paypal.FaultTimeoutAfterCharge)
	p, err := e.svc.Execute(context.Background(), id)
	if err != nil || p.State != state.Uncertain {
		t.Fatalf("execute: %+v, %v", p, err)
	}
	e.fake.SetFault(paypal.FaultNone)
	// The test clock is frozen, so move it on to make the intent older than the sweep cutoff.
	e.clock.Advance(time.Second)
	return id
}

func TestSweeperResolvesLeftoversImmediatelyOnStart(t *testing.T) {
	e := newEnv(t)
	b := e.budget(t, 1000)
	id := e.uncertainIntent(t, b, 400)

	// An hour between sweeps, so only the startup sweep can resolve it in time.
	stop := startSweeper(t, e, SweeperConfig{Interval: time.Hour, OlderThan: time.Nanosecond, Logger: discard()})
	defer stop()

	waitFor(t, "startup sweep to capture the intent", func() bool { return e.stateOf(t, id) == state.Captured })
	e.wantBudget(t, b, 0, 400)
}

func TestSweeperKeepsSweepingOnItsInterval(t *testing.T) {
	e := newEnv(t)
	b := e.budget(t, 1000)

	stop := startSweeper(t, e, SweeperConfig{Interval: 10 * time.Millisecond, OlderThan: time.Nanosecond, Logger: discard()})
	defer stop()

	// Created after the sweeper started, so only a later tick can resolve it.
	id := e.uncertainIntent(t, b, 400)
	waitFor(t, "a later sweep to capture the intent", func() bool { return e.stateOf(t, id) == state.Captured })
	e.wantBudget(t, b, 0, 400)
	e.wantAuditOK(t)
}

func TestSweeperStopsWhenContextIsCancelled(t *testing.T) {
	e := newEnv(t)
	stop := startSweeper(t, e, SweeperConfig{Interval: 10 * time.Millisecond, Logger: discard()})
	time.Sleep(50 * time.Millisecond)
	stop() // fails the test if RunSweeper does not return
}

func TestSweeperLogsConflictsAtErrorLevel(t *testing.T) {
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
	e.fake.Seed(id, 400) // the late capture

	out := &safeBuf{}
	logger := slog.New(slog.NewTextHandler(out, nil))
	stop := startSweeper(t, e, SweeperConfig{Interval: time.Hour, OlderThan: time.Minute, Logger: logger})
	defer stop()

	waitFor(t, "an error log line about the conflict", func() bool {
		s := out.String()
		return strings.Contains(s, "level=ERROR") && strings.Contains(s, "conflicts=1")
	})
	if n := e.countEvents(t, id, "payment_conflict"); n != 1 {
		t.Fatalf("conflict entries = %d, want 1", n)
	}
}
