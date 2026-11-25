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
