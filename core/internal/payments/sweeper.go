package payments

import (
	"context"
	"log/slog"
	"time"
)

type SweeperConfig struct {
	// Interval between sweeps. Default 30 seconds.
	Interval time.Duration
	// OlderThan is the minimum age of an executing or uncertain intent before it
	// is looked at. Default is the service's InFlightGrace.
	OlderThan time.Duration
	// Logger receives the results. Default is slog.Default().
	Logger *slog.Logger
}

// RunSweeper runs Sweep once immediately, so a restart picks up what a crash left
// behind, and then every Interval until ctx is cancelled. It blocks, so call it in
// a goroutine. Sweeps never overlap. Several instances may run it at once, because
// Reconcile takes the intent row lock and is safe to repeat.
//
// Logging contract, so an alert can key on it:
//   - ERROR "payment sweep found money that needs a human": conflicts or mismatches.
//     A conflict is reported once, when it is first flagged, and stays in the audit
//     log as payment_conflict. A mismatch is reported on every sweep until resolved.
//   - WARN "payment sweep could not reconcile an intent": a lookup or database error,
//     retried on the next sweep.
//   - ERROR "payment sweep failed": the sweep could not even list intents.
func (s *Service) RunSweeper(ctx context.Context, cfg SweeperConfig) {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.OlderThan <= 0 {
		cfg.OlderThan = s.InFlightGrace
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s.sweepOnce(ctx, cfg)
	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepOnce(ctx, cfg)
		}
	}
}

func (s *Service) sweepOnce(ctx context.Context, cfg SweeperConfig) {
	log := cfg.Logger
	res, err := s.Sweep(ctx, cfg.OlderThan)
	if err != nil {
		if ctx.Err() == nil { // a shutdown in the middle of a sweep is not an error
			log.Error("payment sweep failed", "error", err)
		}
		return
	}
	if res.Conflicts > 0 || res.Mismatches > 0 {
		log.Error("payment sweep found money that needs a human",
			"conflicts", res.Conflicts, "mismatches", res.Mismatches)
	}
	for _, e := range res.Errors {
		log.Warn("payment sweep could not reconcile an intent", "error", e)
	}
	if res.Captured > 0 || res.Failed > 0 {
		log.Info("payment sweep resolved intents",
			"captured", res.Captured, "failed", res.Failed, "pending", res.Pending)
	}
}
