package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/intents"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/paypal"
	"github.com/Mavericksystem/warden/core/internal/state"
)

var (
	ErrNotExecutable      = errors.New("intent is not in a state that can be executed")
	ErrReservationRefused = errors.New("budget reservation refused")

	ErrTooEarly = errors.New("too early to reconcile this payment")

	// ErrAmountMismatch means PayPal reports a capture whose amount or currency
	// differs from the intent. Nothing is committed, the intent stays unresolved
	// and an audit entry is written. A human must look at it.
	ErrAmountMismatch = errors.New("paypal amount or currency differs from the intent")

	// ErrPaymentConflict means the intent was already resolved the opposite way
	// to what PayPal now shows, for example failed here but captured at PayPal.
	// Money and ledger disagree. The state is left alone (failed and captured are
	// terminal), a payment_conflict audit entry is written, a human must resolve it.
	ErrPaymentConflict = errors.New("intent state conflicts with what paypal shows")
)

type Payment struct {
	IntentID      string
	State         state.State
	PaypalOrderID string
}

type Service struct {
	pool   *pgxpool.Pool
	client paypal.Client

	CallTimeout   time.Duration
	InFlightGrace time.Duration
	NotFoundGrace time.Duration
	// RecheckWindow is how long after being failed by "paypal has no record" an
	// intent keeps being looked up again, to catch a capture that lands late.
	RecheckWindow time.Duration
	Now           func() time.Time
}

func New(pool *pgxpool.Pool, client paypal.Client) *Service {
	return &Service{
		pool:          pool,
		client:        client,
		CallTimeout:   15 * time.Second,
		InFlightGrace: 2 * time.Minute,
		NotFoundGrace: 15 * time.Minute,
		RecheckWindow: 24 * time.Hour,
		Now:           time.Now,
	}
}

type rec struct {
	ID        string
	BudgetID  string
	State     string
	Amount    int64
	Currency  string
	Merchant  string
	Recipient string
	OrderID   *string
	UpdatedAt time.Time
}

func (r rec) payment() Payment {
	p := Payment{IntentID: r.ID, State: state.State(r.State)}
	if r.OrderID != nil {
		p.PaypalOrderID = *r.OrderID
	}
	return p
}

func isPgCode(err error, code string) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == code
}

func load(ctx context.Context, q ledger.Querier, id string, lock bool) (rec, error) {
	sql := `SELECT id::text, budget_id::text, state, amount_minor, currency, merchant, recipient, paypal_order_id, updated_at FROM intents WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	var r rec
	err := q.QueryRow(ctx, sql, id).Scan(&r.ID, &r.BudgetID, &r.State, &r.Amount, &r.Currency, &r.Merchant, &r.Recipient, &r.OrderID, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "22P02") {
		return rec{}, intents.ErrNotFound
	}
	if err != nil {
		return rec{}, fmt.Errorf("read intent: %w", err)
	}
	return r, nil
}

func transition(ctx context.Context, tx pgx.Tx, id string, from, to state.State, orderID *string,
	actor, event string, payload any, now time.Time) error {
	if err := state.Check(from, to); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE intents SET state = $3, version = version + 1, updated_at = $4,
			paypal_order_id = COALESCE($5, paypal_order_id)
			
			WHERE id = $1::uuid AND state = $2`, id, string(from), string(to), now, orderID)
	if err != nil {
		return fmt.Errorf("update intent state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return intents.ErrStateConflict
	}
	if _, err := audit.Append(ctx, tx, id, actor, event, payload, now); err != nil {
		return err
	}
	return nil
}

// noteOnce appends an audit entry that does not change state, unless the latest
// audit entry of the intent is already the same event. Repeated sweeps over the
// same problem therefore do not flood the chain. It takes the intent row lock
// first, so the lock order stays intent, budgets, audit.
func (s *Service) noteOnce(ctx context.Context, id, actor, event string, payload any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := load(ctx, tx, id, true); err != nil {
		return err
	}
	var last string
	err = tx.QueryRow(ctx,
		`SELECT event FROM audit_log WHERE intent_id = $1::uuid ORDER BY seq DESC LIMIT 1`, id).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read latest audit event: %w", err)
	}
	if last == event {
		return nil
	}
	if _, err := audit.Append(ctx, tx, id, actor, event, payload, s.Now()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func isRefusal(err error) bool {
	return errors.Is(err, ledger.ErrBudgetExceeded) ||
		errors.Is(err, ledger.ErrBudgetExpired) ||
		errors.Is(err, ledger.ErrCurrencyMismatch)
}

func (s *Service) Execute(ctx context.Context, intentID string) (Payment, error) {
	r, err := load(ctx, s.pool, intentID, false)
	if err != nil {
		return Payment{}, err
	}
	st := state.State(r.State)

	if st == state.Approved {
		p, err := s.reserve(ctx, intentID)
		if err != nil {
			return p, err
		}
		st = p.State
		if st != state.Reserved {
			return p, nil
		}
	}
	if st == state.Reserved {
		cr, started, p, err := s.start(ctx, intentID)
		if err != nil {
			return p, err
		}
		if !started {
			return p, nil
		}
		return s.call(ctx, cr)
	}

	switch st {
	case state.Executing, state.Uncertain, state.Captured, state.Settled, state.Failed, state.Denied:
		cur, err := load(ctx, s.pool, intentID, false)
		if err != nil {
			return Payment{}, err
		}
		return cur.payment(), nil
	}
	return r.payment(), fmt.Errorf("%w: current state is %s", ErrNotExecutable, st)
}

func (s *Service) reserve(ctx context.Context, id string) (Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	r, err := load(ctx, tx, id, true)
	if err != nil {
		return Payment{}, err
	}
	if state.State(r.State) != state.Approved {
		return r.payment(), nil
	}
	now := s.Now()
	if err := ledger.Reserve(ctx, tx, id, r.BudgetID, r.Amount, r.Currency, now); err != nil {
		if isRefusal(err) {
			_ = tx.Rollback(ctx)
			return s.deny(ctx, id, err)
		}
		return Payment{}, err
	}
	err = transition(ctx, tx, id, state.Approved, state.Reserved, nil, "gateway", "budget_reserved", map[string]any{
		"budget_id":    r.BudgetID,
		"amount_minor": r.Amount,
		"currency":     r.Currency,
	}, now)
	if err != nil {
		return Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit: %w", err)
	}
	return Payment{IntentID: id, State: state.Reserved}, nil
}

func (s *Service) deny(ctx context.Context, id string, cause error) (Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	r, err := load(ctx, tx, id, true)
	if err != nil {
		return Payment{}, err
	}
	if state.State(r.State) != state.Approved {
		return r.payment(), nil
	}
	err = transition(ctx, tx, id, state.Approved, state.Denied, nil, "gateway", "reservation_refused",
		map[string]any{"reason": cause.Error()}, s.Now())
	if err != nil {
		return Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit: %w", err)
	}
	return Payment{IntentID: id, State: state.Denied}, fmt.Errorf("%w: %w", ErrReservationRefused, cause)
}

func (s *Service) start(ctx context.Context, id string) (paypal.CaptureRequest, bool, Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return paypal.CaptureRequest{}, false, Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	r, err := load(ctx, tx, id, true)
	if err != nil {
		return paypal.CaptureRequest{}, false, Payment{}, err
	}
	if state.State(r.State) != state.Reserved {
		return paypal.CaptureRequest{}, false, r.payment(), nil
	}
	err = transition(ctx, tx, id, state.Reserved, state.Executing, nil, "gateway", "payment_executing", map[string]any{
		"request_id":   id,
		"amount_minor": r.Amount,
		"currency":     r.Currency,
		"merchant":     r.Merchant,
		"recipient":    r.Recipient,
	}, s.Now())
	if err != nil {
		return paypal.CaptureRequest{}, false, Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return paypal.CaptureRequest{}, false, Payment{}, fmt.Errorf("commit: %w", err)
	}
	return paypal.CaptureRequest{
		RequestID:   id,
		AmountMinor: r.Amount,
		Currency:    r.Currency,
		Merchant:    r.Merchant,
		Recipient:   r.Recipient,
	}, true, Payment{IntentID: id, State: state.Executing}, nil
}

func (s *Service) call(ctx context.Context, req paypal.CaptureRequest) (Payment, error) {
	timeout := s.CallTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	res, callErr := s.client.Capture(cctx, req)
	cancel()

	wctx := context.WithoutCancel(ctx)

	switch {
	case callErr == nil:
		// A success that does not match the intent is not trusted: the money
		// stays reserved and the intent goes uncertain, so reconciliation and a
		// human look at it instead of committing the wrong amount.
		if res.AmountMinor != req.AmountMinor || res.Currency != req.Currency {
			mismatch := fmt.Errorf("%w: capture reported %d %s, intent is %d %s",
				ErrAmountMismatch, res.AmountMinor, res.Currency, req.AmountMinor, req.Currency)
			return s.markUncertain(wctx, req.RequestID, mismatch)
		}
		order := res.OrderID
		return s.finish(wctx, req.RequestID, state.Captured, &order, "paypal", "payment_captured",
			map[string]any{"order_id": res.OrderID})
	case errors.Is(callErr, paypal.ErrDeclined):
		return s.finish(wctx, req.RequestID, state.Failed, nil, "paypal", "payment_declined",
			map[string]any{"error": callErr.Error()})

	default:
		return s.markUncertain(wctx, req.RequestID, callErr)
	}
}

func (s *Service) markUncertain(ctx context.Context, id string, callErr error) (Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	r, err := load(ctx, tx, id, true)
	if err != nil {
		return Payment{}, err
	}
	if state.State(r.State) != state.Executing {
		return r.payment(), nil
	}
	err = transition(ctx, tx, id, state.Executing, state.Uncertain, nil, "gateway", "payment_uncertain",
		map[string]any{"error": callErr.Error()}, s.Now())
	if err != nil {
		return Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit: %w", err)
	}
	return Payment{IntentID: id, State: state.Uncertain}, nil
}

func (s *Service) finish(ctx context.Context, id string, to state.State, orderID *string,
	actor, event string, payload any) (Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	r, err := load(ctx, tx, id, true)
	if err != nil {
		return Payment{}, err
	}
	cur := state.State(r.State)
	if cur == to {
		return r.payment(), nil // already resolved the same way
	}
	if cur != state.Executing && cur != state.Uncertain {
		if cur == state.Captured || cur == state.Failed {
			// Resolved the opposite way already. Both states are terminal, so the
			// intent is left alone and the disagreement is recorded for a human.
			pay := r.payment()
			_ = tx.Rollback(ctx)
			conflict := map[string]any{
				"intent_state":    string(cur),
				"paypal_outcome":  string(to),
				"budget_id":       r.BudgetID,
				"amount_minor":    r.Amount,
				"currency":        r.Currency,
				"attempted_event": event,
			}
			if orderID != nil {
				conflict["order_id"] = *orderID
			}
			if err := s.noteOnce(ctx, id, actor, "payment_conflict", conflict); err != nil {
				return pay, err
			}
			return pay, fmt.Errorf("%w: intent is %s but outcome was %s", ErrPaymentConflict, cur, to)
		}
		return r.payment(), fmt.Errorf("%w: wanted %s but intent is %s", intents.ErrStateConflict, to, cur)
	}
	if to == state.Captured {
		err = ledger.Commit(ctx, tx, id)
	} else {
		err = ledger.Release(ctx, tx, id)
	}
	if err != nil {
		return Payment{}, err
	}
	if err := transition(ctx, tx, id, cur, to, orderID, actor, event, payload, s.Now()); err != nil {
		return Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit: %w", err)
	}
	p := Payment{IntentID: id, State: to}
	if orderID != nil {
		p.PaypalOrderID = *orderID
	}
	return p, nil
}

func (s *Service) lookup(ctx context.Context, id string) (paypal.LookupResult, error) {
	timeout := s.CallTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	lctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lk, err := s.client.Lookup(lctx, id)
	if err != nil {
		return paypal.LookupResult{}, fmt.Errorf("lookup at paypal: %w", err)
	}
	return lk, nil
}

func (s *Service) Reconcile(ctx context.Context, intentID string) (Payment, error) {
	r, err := load(ctx, s.pool, intentID, false)
	if err != nil {
		return Payment{}, err
	}
	st := state.State(r.State)
	if st == state.Failed {
		return s.recheckFailed(ctx, r)
	}
	if st != state.Executing && st != state.Uncertain {
		return r.payment(), nil
	}
	age := s.Now().Sub(r.UpdatedAt)
	if st == state.Executing && age < s.InFlightGrace {
		return r.payment(), ErrTooEarly
	}

	lk, err := s.lookup(ctx, intentID)
	if err != nil {
		return r.payment(), err
	}

	switch lk.Status {
	case paypal.StatusCaptured:
		if lk.AmountMinor != r.Amount || lk.Currency != r.Currency {
			note := map[string]any{
				"was":               string(st),
				"order_id":          lk.OrderID,
				"expected_minor":    r.Amount,
				"expected_currency": r.Currency,
				"paypal_minor":      lk.AmountMinor,
				"paypal_currency":   lk.Currency,
			}
			if err := s.noteOnce(ctx, intentID, "reconciler", "reconcile_amount_mismatch", note); err != nil {
				return r.payment(), err
			}
			return r.payment(), fmt.Errorf("%w: paypal captured %d %s, intent is %d %s",
				ErrAmountMismatch, lk.AmountMinor, lk.Currency, r.Amount, r.Currency)
		}
		order := lk.OrderID
		return s.finish(ctx, intentID, state.Captured, &order, "reconciler", "reconciled_captured",
			map[string]any{"order_id": lk.OrderID, "was": string(st)})
	case paypal.StatusDeclined:
		return s.finish(ctx, intentID, state.Failed, nil, "reconciler", "reconciled_declined",
			map[string]any{"was": string(st)})
	case paypal.StatusNotFound:
		if age < s.NotFoundGrace {
			return r.payment(), ErrTooEarly
		}
		return s.finish(ctx, intentID, state.Failed, nil, "reconciler", "reconciled_not_found",
			map[string]any{"was": string(st), "age_seconds": int64(age.Seconds())})
	}
	return r.payment(), fmt.Errorf("unknown lookup status %q", lk.Status)
}

// recheckFailed catches the dangerous case: an intent was failed because PayPal
// had no record, and the request then landed anyway. Only intents failed that way
// are looked up again, and only inside RecheckWindow. A declined intent is final
// because PayPal answers a repeated request id with the same decline.
func (s *Service) recheckFailed(ctx context.Context, r rec) (Payment, error) {
	if s.Now().Sub(r.UpdatedAt) > s.RecheckWindow {
		return r.payment(), nil
	}
	var byNotFound bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM audit_log WHERE intent_id = $1::uuid AND event = 'reconciled_not_found')`,
		r.ID).Scan(&byNotFound)
	if err != nil {
		return r.payment(), fmt.Errorf("read failure reason: %w", err)
	}
	if !byNotFound {
		return r.payment(), nil
	}
	lk, err := s.lookup(ctx, r.ID)
	if err != nil {
		return r.payment(), err
	}
	if lk.Status != paypal.StatusCaptured {
		return r.payment(), nil
	}
	conflict := map[string]any{
		"intent_state":   string(state.Failed),
		"paypal_outcome": string(state.Captured),
		"budget_id":      r.BudgetID,
		"amount_minor":   r.Amount,
		"currency":       r.Currency,
		"order_id":       lk.OrderID,
		"paypal_minor":   lk.AmountMinor,
		"paypal_curr":    lk.Currency,
		"detected_by":    "recheck_failed",
	}
	if err := s.noteOnce(ctx, r.ID, "reconciler", "payment_conflict", conflict); err != nil {
		return r.payment(), err
	}
	return r.payment(), fmt.Errorf("%w: intent is failed but paypal captured it", ErrPaymentConflict)
}

type SweepResult struct {
	Captured   int
	Failed     int
	Pending    int
	Conflicts  int // intents whose state disagrees with PayPal, need a human
	Mismatches int // captures whose amount or currency differ, need a human
	Errors     []error
}

func (s *Service) ids(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list intents: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan intent id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate intents: %w", err)
	}
	return ids, nil
}

func (s *Service) Sweep(ctx context.Context, olderThan time.Duration) (SweepResult, error) {
	now := s.Now()
	unresolved, err := s.ids(ctx,
		`SELECT id::text FROM intents
		 WHERE state IN ('executing', 'uncertain') AND updated_at <= $1
		 ORDER BY updated_at`, now.Add(-olderThan))
	if err != nil {
		return SweepResult{}, err
	}
	// Failed by "no record" and not yet flagged: a late capture may still show up.
	recheck, err := s.ids(ctx,
		`SELECT i.id::text FROM intents i
		 WHERE i.state = 'failed' AND i.updated_at > $1
		   AND EXISTS (SELECT 1 FROM audit_log a WHERE a.intent_id = i.id AND a.event = 'reconciled_not_found')
		   AND NOT EXISTS (SELECT 1 FROM audit_log a WHERE a.intent_id = i.id AND a.event = 'payment_conflict')
		 ORDER BY i.updated_at`, now.Add(-s.RecheckWindow))
	if err != nil {
		return SweepResult{}, err
	}

	var res SweepResult
	for _, id := range append(unresolved, recheck...) {
		p, err := s.Reconcile(ctx, id)
		switch {
		case errors.Is(err, ErrTooEarly):
			res.Pending++
		case errors.Is(err, ErrPaymentConflict):
			res.Conflicts++
		case errors.Is(err, ErrAmountMismatch):
			res.Mismatches++
		case err != nil:
			res.Errors = append(res.Errors, fmt.Errorf("intent %s: %w", id, err))
		case p.State == state.Captured:
			res.Captured++
		case p.State == state.Failed:
			// Only a state change counts as a failure resolved by this sweep.
			if containsID(unresolved, id) {
				res.Failed++
			}
		default:
			res.Pending++
		}
	}
	return res, nil
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
