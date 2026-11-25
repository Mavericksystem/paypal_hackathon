package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/api"
	"github.com/Mavericksystem/warden/core/internal/intents"
	"github.com/Mavericksystem/warden/core/internal/state"
)

const validBody = `{"agent_id":"agent-1","budget_id":"b-1","idempotency_key":"k1","amount_minor":1000,"currency":"USD","merchant":"acme","recipient":"acme-payments","goal":"buy API credits"}`

type fakeSvc struct {
	create  func(context.Context, intents.CreateInput) (intents.Intent, error)
	get     func(context.Context, string) (intents.Intent, error)
	creates int
}

func (f *fakeSvc) Create(ctx context.Context, in intents.CreateInput) (intents.Intent, error) {
	f.creates++
	if f.create == nil {
		return intents.Intent{}, errors.New("unexpected Create call")
	}
	return f.create(ctx, in)
}

func (f *fakeSvc) Get(ctx context.Context, id string) (intents.Intent, error) {
	if f.get == nil {
		return intents.Intent{}, errors.New("unexpected Get call")
	}
	return f.get(ctx, id)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return b.Error.Code
}

type intentJSON struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	AmountMinor int64  `json:"amount_minor"`
	Version     int64  `json:"version"`
}

func decodeIntent(t *testing.T, rec *httptest.ResponseRecorder) intentJSON {
	t.Helper()
	var got intentJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode intent %q: %v", rec.Body.String(), err)
	}
	return got
}

func TestHealthz(t *testing.T) {
	h := api.New(&fakeSvc{}, quietLogger())
	if rec := do(t, h, "GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestCreateIntentRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"empty body":    "",
		"not json":      "not json",
		"array":         "[]",
		"unknown field": `{"agent_id":"a","admin":true}`,
		"trailing data": validBody + ` {"x":1}`,
		"wrong type":    `{"amount_minor":"1000"}`,
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			svc := &fakeSvc{}
			rec := do(t, api.New(svc, quietLogger()), "POST", "/intents", body)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "invalid_request" {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if svc.creates != 0 {
				t.Fatalf("service was called %d times for a bad body", svc.creates)
			}
		})
	}
}

func TestCreateIntentRejectsOversizedBody(t *testing.T) {
	svc := &fakeSvc{}
	body := `{"goal":"` + strings.Repeat("a", 70<<10) + `"}`
	rec := do(t, api.New(svc, quietLogger()), "POST", "/intents", body)
	if rec.Code != http.StatusRequestEntityTooLarge || errCode(t, rec) != "body_too_large" {
		t.Fatalf("status = %d body = %.200s", rec.Code, rec.Body.String())
	}
	if svc.creates != 0 {
		t.Fatalf("service was called for an oversized body")
	}
}

func TestCreateIntentPassesFieldsAndReturnsIntent(t *testing.T) {
	var got intents.CreateInput
	svc := &fakeSvc{create: func(_ context.Context, in intents.CreateInput) (intents.Intent, error) {
		got = in
		return intents.Intent{
			ID: "i-1", AgentID: in.AgentID, BudgetID: in.BudgetID, State: state.PendingApproval,
			AmountMinor: in.AmountMinor, Currency: in.Currency, Merchant: in.Merchant,
			Recipient: in.Recipient, Goal: in.Goal, IdempotencyKey: in.IdempotencyKey, Version: 3,
		}, nil
	}}
	rec := do(t, api.New(svc, quietLogger()), "POST", "/intents", validBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	want := intents.CreateInput{
		AgentID: "agent-1", BudgetID: "b-1", IdempotencyKey: "k1", AmountMinor: 1000,
		Currency: "USD", Merchant: "acme", Recipient: "acme-payments", Goal: "buy API credits",
	}
	if got != want {
		t.Fatalf("service got %+v, want %+v", got, want)
	}
	out := decodeIntent(t, rec)
	if out.ID != "i-1" || out.State != "pending_approval" || out.AmountMinor != 1000 || out.Version != 3 {
		t.Fatalf("response = %+v", out)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestServiceErrorsMapToStatuses(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid input", fmt.Errorf("%w: amount must be greater than zero", intents.ErrInvalidInput), 400, "invalid_request"},
		{"budget not found", ledger.ErrBudgetNotFound, 404, "budget_not_found"},
		{"idempotency conflict", intents.ErrIdempotencyConflict, 409, "idempotency_conflict"},
		{"state conflict", intents.ErrStateConflict, 409, "state_conflict"},
		{"no policy", fmt.Errorf("load policy: %w", policystore.ErrNoPolicy), 403, "no_policy"},
		{"unexpected", errors.New("dial tcp: password=hunter2 refused"), 500, "internal_error"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSvc{create: func(context.Context, intents.CreateInput) (intents.Intent, error) {
				return intents.Intent{}, tc.err
			}}
			rec := do(t, api.New(svc, quietLogger()), "POST", "/intents", validBody)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("status = %d code = %q, want %d %q", rec.Code, errCode(t, rec), tc.status, tc.code)
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Fatalf("internal error detail leaked to the caller: %s", rec.Body.String())
			}
		})
	}

	func TestGetIntent(t *testing.T) {
	var gotID string
	svc := &fakeSvc{get: func(_ context.Context, id string) (intents.Intent, error) {
		gotID = id
		if id == "missing" {
			return intents.Intent{}, intents.ErrNotFound
		}
		return intents.Intent{ID: id, State: state.Approved}, nil
	}}
	h := api.New(svc, quietLogger())

	rec := do(t, h, "GET", "/intents/abc-123", "")
	if rec.Code != http.StatusOK || gotID != "abc-123" || decodeIntent(t, rec).State != "approved" {
		t.Fatalf("status = %d id = %q body = %s", rec.Code, gotID, rec.Body.String())
	}
	rec = do(t, h, "GET", "/intents/missing", "")
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestWrongMethodAndUnknownPath(t *testing.T) {
	h := api.New(&fakeSvc{}, quietLogger())
	if rec := do(t, h, "PUT", "/intents", validBody); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /intents = %d, want 405", rec.Code)
	}
	if rec := do(t, h, "POST", "/intents/abc", validBody); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /intents/abc = %d, want 405", rec.Code)
	}
	if rec := do(t, h, "GET", "/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
	
	for _, p := range []string{"/policies", "/budgets", "/intents/abc/approve"} {
		for _, m := range []string{"GET", "POST", "PUT"} {
			if rec := do(t, h, m, p, validBody); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 404 or 405", m, p, rec.Code)
			}
		}
	}
}

func TestPanicIsRecovered(t *testing.T) {
	svc := &fakeSvc{get: func(context.Context, string) (intents.Intent, error) { panic("boom") }}
	h := api.New(svc, quietLogger())
	rec := do(t, h, "GET", "/intents/x", "")
	if rec.Code != http.StatusInternalServerError || errCode(t, rec) != "internal_error" {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("panic detail leaked: %s", rec.Body.String())
	}
	if rec := do(t, h, "GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("server unusable after a panic: %d", rec.Code)
	}
}

func createBody(budget, key string, amount int64) string {
	return fmt.Sprintf(`{"agent_id":"agent-1","budget_id":%q,"idempotency_key":%q,"amount_minor":%d,"currency":"USD","merchant":"acme","recipient":"acme-payments","goal":"buy API credits"}`,
		budget, key, amount)
}

func TestEndToEndWithRealStore(t *testing.T) {
	pool := testutil.NewPool(t) 
	ctx := context.Background()
	budget, err := ledger.CreateBudget(ctx, pool, "e2e", "USD", 1_000_000, "", nil)
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	store := policystore.New(pool)
	h := api.New(intents.New(pool, store, nil), quietLogger())

	
	rec := do(t, h, "POST", "/intents", createBody(budget, "k1", 1000))
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "no_policy" {
		t.Fatalf("no policy: status = %d body = %s", rec.Code, rec.Body.String())
	}

	err = store.Put(ctx, "tester", policystore.Input{
		AgentID: "agent-1", BudgetID: budget, AllowedMerchants: []string{"acme"}, Currency: "USD",
		MaxPerPaymentMinor: 100_000, AutoApproveMaxMinor: 5_000,
	})
	if err != nil {
		t.Fatalf("put policy: %v", err)
	}
	rec = do(t, h, "POST", "/intents", createBody(budget, "k1", 1000))
	if rec.Code != http.StatusOK {
		t.Fatalf("with policy: status = %d body = %s", rec.Code, rec.Body.String())
	}
	first := decodeIntent(t, rec)
	if first.State != "pending_approval" {
		t.Fatalf("state = %q, want pending_approval", first.State)
	}
