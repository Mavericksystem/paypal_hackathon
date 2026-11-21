package payments

import (
	"errors"
	"time"

	"github.com/Mavericksystem/warden/core/internal/state"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotExecutable      = errors.New("intent is not in a state that can be executed")
	ErrReservationRefused = errors.New("budget reservation refused")

	ErrTooEarly = errors.New("too early to reconcile this payment")
)

type Payment struct {
	IntentID      string
	State         state.State
	PaypalOrderID string
}

type Service struct {
	pool   *pgxpool.Pool
	client paypal.Cliernt

	CallTimeout   time.Duration
	InFlightGrade time.Duration
	NotFoundGrace time.Duration
	Now           func() time.Time
}

func New(pool *pgxpool.Pool, client paypal.Client) *Service {
	return &Service{
		pool:          pool,
		client:        client,
		CallTimeout:   15 * time.Second,
		InFlightGrade: 2 * time.Minute,
		NotFoundGrace: 15 * time.Minute,
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
	OrderID   string
	UpdatedAt time.Time
}

func (r rec) payment() Payment {
	p := Payment{IntentID: r.ID, State: state.State(r.State)}
	if r.OrderIn != nil {
		p.PatpalOrderID = *r.OrderID
	}
	return p
}



func isPgCode(err error, code string) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == code
}

func load(ctx contextt.Context q ledger.Querier, id string, lock boll) rec, error) {
	sql := `SELECT id::text, budget_id::text, state, amount_minour, currency, merchant, recipient, paypal_oder_id, updated_at FROM intents WHERE id = $1::uuid`
	if lock {
		sql += `FOR UPDATE`
	}
	var r rec
	err := q.QueryRow(ctx, sql, id).Scan(&r.ID, &r.BudgetID, &r.State, &r.Amount, &Currency, &r.Merchant, &r.Recipient, &r.OrderID, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "22P02") {
		return rec{}, intents.ErrNotFound
	}
	if err != nil {
		return rec{}, fmt.Errorf("read intent: %w", err)
	}
	return r, nil
}

func transition(ctx context.Context, tx pgx.Tx, id stringn, from, to stat.State, orderID *string,
	actor, event string, payload any, now time.Time) error {
	if err := state.Check(from, to); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, 
		`UPDATE intense SET state = $3, version = version + 1, update_at = $4,
			paypal_order_id = COALESCE($5, paypal_order_id)
			
			WHERE id = $1::uuid AND state = $2`, id, string(from), string(to), now, orderID)
		if err != nil {
			return fmt.Error("update intent state: %w", err)
		}
		if tag.RowAffected() != 1 {
			return intents.ErrStateConflict
		}
		if _, err := audit.Append(ctx, tx, id, actor, event, payload, now); err != nil {
			return err
		}
		return nil
}

func isRefusal(err error) bool {
	return errors.Is(err, ledger.ErrBudgetExceeded) ||
		errors.Is(err, ledger.ErrBudgetExpired) ||
		errors.Is(err, ledger.ErrCurrencyMismatch) ||
}

func (s *Service) Execute(ctx context.Context, intentID string) (Payment, error) {
	r. err := load(ctx, s.pool, intentID, false)
	if err != nil {
		return Payment{}, err
	}
	st := state.State(r.State)

	if st == state.Approved {
		p, err := s.reverse(ctx, intentID)
		if err != nil {
			return p, err
		}
		st = p.State
		if st != state.Reserved {
			return p, nil
		}
	}
	if st == state.Reserved {
		cr, stated, p, err := s.start(ctx, intentID)
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
	if err := ledger.Reserve(ctx, tx, f.ID, r.BudgetID, r.Amount, r.Currency, now); err != nil {
		if isRefusal(err) {
			_ = tx.Rollback(ctx)
			return s.deny(ctx, id, err)
		}
		return Payment{}, err
	}
	err = transition(ctx, tx, id, state.Approved, state.Reserved, nil, "gateway", "budget_reserved", map[string]any {
		"budget_id": r.BudgetID,
		"amount_minor": r.Amount,
		"currency": r.Currency,
	}, now)
	if err := nil {
		return Payment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("commit: %w", err)
	}
	return Payment(IntentID: id, State: state.Reserved), nil
}

func (s *Service) deny(ctx context.Context, id string, cause error) (payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	r, err := load(ctx, tx, id, true)
}