// Package intents is the entry point of the gateway. An agent creates a payment
// intent, the gateway checks it against policy and the AI check, and the intent
// lands in one of three states: approved, denied or pending_approval.
//
// Rules this package keeps:
//
//   - Create is idempotent on (agent_id, idempotency_key). A retry returns the same
//     intent. The same key with different parameters is refused.
//   - Create is resumable. If the process dies after the intent is stored but before
//     it is decided, the next Create with the same key finishes the job.
//   - The AI call (network, slow) never runs inside a database transaction.
//   - Every state change and its audit entry are written in one transaction.
//   - Fail closed: any AI failure escalates to a human, a policy lookup failure
//     decides nothing, and a rules deny is final (the AI is not even asked).
package intents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mavericksystem/warden/core/internal/ai"
	"github.com/Mavericksystem/warden/core/internal/audit"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policy"
	"github.com/Mavericksystem/warden/core/internal/state"
)

var (
	ErrInvalidInput        = errors.New("invalid intent input")
	ErrIdempotencyConflict = errors.New("idempotency key was already used with different parameters")
	ErrNotFound            = errors.New("intent not found")
	ErrNotPending          = errors.New("intent is not pending approval")
	ErrStateConflict       = errors.New("intent state changed concurrently")
)

// PolicySource returns the policy that applies to an agent and budget.
// A real implementation will read the policies table (roadmap step 2).
type PolicySource interface {
	For(ctx context.Context, agentID, budgetID string) (policy.Policy, error)
}

// StaticPolicy applies the same policy to everyone. Useful for tests and the first slice.
type StaticPolicy policy.Policy

func (p StaticPolicy) For(context.Context, string, string) (policy.Policy, error) {
	return policy.Policy(p), nil
}

// CheckInput is what the AI check is allowed to see.
type CheckInput struct {
	IntentID    string
	Goal        string
	Merchant    string
	Recipient   string
	AmountMinor int64
	Currency    string
}

// CheckResult carries the verdict plus what must be recorded with it in the audit log.
type CheckResult struct {
	Decision      ai.Decision
	Model         string
	PromptVersion string
}

// Checker is the AI check. The real client (roadmap step 3) and test fakes implement it.
type Checker interface {
	Check(ctx context.Context, in CheckInput) (CheckResult, error)
}

type Intent struct {
	ID             string
	AgentID        string
	BudgetID       string
	State          state.State
	AmountMinor    int64
	Currency       string
	Merchant       string
	Recipient      string
	Goal           string
	IdempotencyKey string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateInput struct {
	AgentID        string
	BudgetID       string
	IdempotencyKey string
	AmountMinor    int64
	Currency       string // upper case ISO code, for example "USD"
	Merchant       string
	Recipient      string
	Goal           string
}

func (in CreateInput) validate() error {
	switch {
	case strings.TrimSpace(in.AgentID) == "":
		return fmt.Errorf("%w: agent_id is required", ErrInvalidInput)
	case strings.TrimSpace(in.BudgetID) == "":
		return fmt.Errorf("%w: budget_id is required", ErrInvalidInput)
	case strings.TrimSpace(in.IdempotencyKey) == "":
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalidInput)
	case in.AmountMinor <= 0:
		return fmt.Errorf("%w: amount must be greater than zero", ErrInvalidInput)
	case len(in.Currency) != 3 || strings.ToUpper(in.Currency) != in.Currency:
		return fmt.Errorf("%w: currency must be a 3 letter upper case code", ErrInvalidInput)
	case strings.TrimSpace(in.Merchant) == "":
		return fmt.Errorf("%w: merchant is required", ErrInvalidInput)
	case strings.TrimSpace(in.Recipient) == "":
		return fmt.Errorf("%w: recipient is required", ErrInvalidInput)
	}
	return nil
}

type Service struct {
	pool     *pgxpool.Pool
	policies PolicySource
	checker  Checker

	// AITimeout bounds one AI check. Exceeding it counts as an AI failure.
	AITimeout time.Duration
	// Now is replaceable in tests.
	Now func() time.Time
}

// New builds a service. A nil checker is allowed and behaves like a permanently
// failing AI check, so everything that passes the rules escalates to a human.
func New(pool *pgxpool.Pool, policies PolicySource, checker Checker) *Service {
	return &Service{
		pool:      pool,
		policies:  policies,
		checker:   checker,
		AITimeout: 10 * time.Second,
		Now:       time.Now,
	}
}

const selectSQL = `SELECT id::text, agent_id, budget_id::text, state, amount_minor, currency,
	merchant, recipient, goal, idempotency_key, version, created_at, updated_at
	FROM intents`

func scanIntent(row pgx.Row) (Intent, error) {
	var in Intent
	var st string
	err := row.Scan(&in.ID, &in.AgentID, &in.BudgetID, &st, &in.AmountMinor, &in.Currency,
		&in.Merchant, &in.Recipient, &in.Goal, &in.IdempotencyKey, &in.Version, &in.CreatedAt, &in.UpdatedAt)
	in.State = state.State(st)
	return in, err
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// Get returns one intent.
func (s *Service) Get(ctx context.Context, id string) (Intent, error) {
	in, err := scanIntent(s.pool.QueryRow(ctx, selectSQL+` WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "22P02") {
		return Intent{}, ErrNotFound
	}
	if err != nil {
		return Intent{}, fmt.Errorf("read intent: %w", err)
	}
	return in, nil
}

// Create stores the intent (or finds the existing one for the same idempotency
// key) and, if it is still undecided, runs the policy and AI checks.
func (s *Service) Create(ctx context.Context, in CreateInput) (Intent, error) {
	if err := in.validate(); err != nil {
		return Intent{}, err
	}
	id, err := s.insert(ctx, in)
	if err != nil {
		return Intent{}, err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Intent{}, err
	}
	if cur.State != state.Created {
		return cur, nil // already decided, this is a retry
	}
	if err := s.process(ctx, cur); err != nil {
		return Intent{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) insert(ctx context.Context, in CreateInput) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	now := s.Now()
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO intents (agent_id, budget_id, state, amount_minor, currency, merchant, recipient, goal, idempotency_key)
		 VALUES ($1, $2::uuid, 'created', $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (agent_id, idempotency_key) DO NOTHING
		 RETURNING id::text`,
		in.AgentID, in.BudgetID, in.AmountMinor, in.Currency, in.Merchant, in.Recipient, in.Goal, in.IdempotencyKey,
	).Scan(&id)

	switch {
	case err == nil:
		payload := map[string]any{
			"agent_id":        in.AgentID,
			"budget_id":       in.BudgetID,
			"amount_minor":    in.AmountMinor,
			"currency":        in.Currency,
			"merchant":        in.Merchant,
			"recipient":       in.Recipient,
			"goal":            in.Goal,
			"idempotency_key": in.IdempotencyKey,
		}
		if _, err := audit.Append(ctx, tx, id, "agent:"+in.AgentID, "intent_created", payload, now); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit: %w", err)
		}
		return id, nil

	case errors.Is(err, pgx.ErrNoRows):
		// The key already exists. Same parameters means a retry, anything else is a bug or an attack.
		ex, err := scanIntent(tx.QueryRow(ctx, selectSQL+` WHERE agent_id = $1 AND idempotency_key = $2`,
			in.AgentID, in.IdempotencyKey))
		if err != nil {
			return "", fmt.Errorf("read existing intent: %w", err)
		}
		if ex.BudgetID != in.BudgetID || ex.AmountMinor != in.AmountMinor || ex.Currency != in.Currency ||
			ex.Merchant != in.Merchant || ex.Recipient != in.Recipient || ex.Goal != in.Goal {
			return "", ErrIdempotencyConflict
		}
		return ex.ID, nil

	case isPgCode(err, "23503"):
		return "", ledger.ErrBudgetNotFound
	case isPgCode(err, "22P02"):
		return "", fmt.Errorf("%w: budget_id is not a valid UUID", ErrInvalidInput)
	default:
		return "", fmt.Errorf("insert intent: %w", err)
	}
}

// process runs the checks for an intent in state created and records the result.
// Safe to call from several goroutines or processes at once: only one of them
// performs the transition, the others find the intent already decided.
func (s *Service) process(ctx context.Context, in Intent) error {
	// Fail closed: if the policy cannot be read nothing is decided and the intent stays created.
	pol, err := s.policies.For(ctx, in.AgentID, in.BudgetID)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	snap, err := ledger.GetSnapshot(ctx, s.pool, in.BudgetID)
	if err != nil {
		return fmt.Errorf("read budget: %w", err)
	}
	// This read is advisory. The authoritative check is ledger.Reserve under row locks.
	rules := policy.Evaluate(pol, policy.Request{
		Merchant:        in.Merchant,
		Amount:          in.AmountMinor,
		Currency:        in.Currency,
		BudgetRemaining: snap.Available(),
	})

	var res CheckResult
	var aiErr error
	aiRan := false
	if rules.Verdict == policy.Pass { // a rules deny is final, do not spend an AI call on it
		aiRan = true
		res, aiErr = s.runAI(ctx, in)
	}
	final := ai.Combine(rules, res.Decision, aiErr)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var cur string
	if err := tx.QueryRow(ctx, `SELECT state FROM intents WHERE id = $1::uuid FOR UPDATE`, in.ID).Scan(&cur); err != nil {
		return fmt.Errorf("lock intent: %w", err)
	}
	if state.State(cur) != state.Created {
		return nil // another caller already decided this intent
	}

	now := s.Now()
	err = transition(ctx, tx, in.ID, state.Created, state.PolicyChecked, "gateway", "policy_checked", map[string]any{
		"verdict":          string(rules.Verdict),
		"code":             rules.Code,
		"reason":           rules.Reason,
		"needs_human":      rules.NeedsHuman,
		"budget_remaining": snap.Available(),
	}, now)
	if err != nil {
		return err
	}
	if aiRan {
		payload := map[string]any{
			"model":          res.Model,
			"prompt_version": res.PromptVersion,
			"verdict":        string(res.Decision.Verdict),
			"reason":         res.Decision.Reason,
		}
		if aiErr != nil {
			payload["error"] = aiErr.Error()
		}
		if _, err := audit.Append(ctx, tx, in.ID, "ai", "ai_checked", payload, now); err != nil {
			return err
		}
	}
	target := targetState(final.Verdict)
	err = transition(ctx, tx, in.ID, state.PolicyChecked, target, "gateway", "intent_"+string(target), map[string]any{
		"verdict": string(final.Verdict),
		"source":  string(final.Source),
		"reason":  final.Reason,
	}, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *Service) runAI(ctx context.Context, in Intent) (CheckResult, error) {
	if s.checker == nil {
		return CheckResult{}, errors.New("no AI checker configured")
	}
	timeout := s.AITimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.checker.Check(cctx, CheckInput{
		IntentID:    in.ID,
		Goal:        in.Goal,
		Merchant:    in.Merchant,
		Recipient:   in.Recipient,
		AmountMinor: in.AmountMinor,
		Currency:    in.Currency,
	})
}

// targetState maps the final verdict to a state. Anything that is not an
// explicit allow or deny becomes a human review, so unknown values fail closed.
func targetState(v ai.Verdict) state.State {
	switch v {
	case ai.Allow:
		return state.Approved
	case ai.Deny:
		return state.Denied
	default:
		return state.PendingApproval
	}
}

// Decide records a human approving or rejecting an intent that is pending approval.
// Repeating the same decision is a no-op. The opposite decision is refused.
func (s *Service) Decide(ctx context.Context, intentID, approver string, approve bool, note string) (Intent, error) {
	if strings.TrimSpace(approver) == "" {
		return Intent{}, fmt.Errorf("%w: approver is required", ErrInvalidInput)
	}
	to, event := state.Rejected, "human_rejected"
	if approve {
		to, event = state.Approved, "human_approved"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Intent{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var cur string
	err = tx.QueryRow(ctx, `SELECT state FROM intents WHERE id = $1::uuid FOR UPDATE`, intentID).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "22P02") {
		return Intent{}, ErrNotFound
	}
	if err != nil {
		return Intent{}, fmt.Errorf("lock intent: %w", err)
	}
	switch state.State(cur) {
	case to:
		_ = tx.Rollback(ctx)
		return s.Get(ctx, intentID)
	case state.PendingApproval:
	default:
		return Intent{}, fmt.Errorf("%w: current state is %s", ErrNotPending, cur)
	}

	err = transition(ctx, tx, intentID, state.PendingApproval, to, "human:"+approver, event,
		map[string]any{"note": note}, s.Now())
	if err != nil {
		return Intent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Intent{}, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, intentID)
}

// transition moves an intent one step along the state machine and appends the
// audit entry in the same transaction. The caller must hold the intent row lock.
func transition(ctx context.Context, tx pgx.Tx, id string, from, to state.State, actor, event string, payload any, now time.Time) error {
	if err := state.Check(from, to); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE intents SET state = $3, version = version + 1, updated_at = $4
		 WHERE id = $1::uuid AND state = $2`, id, string(from), string(to), now)
	if err != nil {
		return fmt.Errorf("update intent state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStateConflict
	}
	if _, err := audit.Append(ctx, tx, id, actor, event, payload, now); err != nil {
		return err
	}
	return nil
}
