package nim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/intents"
)

// Paste meta.prompt_sha256 from eval/results/*_test_v4_final.json here.
const wantPromptSHA = "PASTE_FROM_RESULTS_JSON"

func TestSystemPromptMatchesEvaluatedPrompt(t *testing.T) {
	if wantPromptSHA == "PASTE_FROM_RESULTS_JSON" {
		t.Skip("set wantPromptSHA from the v4 results file")
	}
	sum := sha256.Sum256([]byte(systemPrompt))
	if got := hex.EncodeToString(sum[:])[:16]; got != wantPromptSHA {
		t.Fatalf("system prompt differs from the evaluated v4 prompt: %s", got)
	}
}

func TestUserMessageGolden(t *testing.T) {
	got := userMessage(intents.CheckInput{
		Goal: "buy one USB-C cable under 20 dollars", Item: "USB-C cable", Quantity: 1,
		AmountMinor: 1299, Currency: "USD", Merchant: "AudioShop", SellerText: "In stock.",
	})
	want := "User goal: buy one USB-C cable under 20 dollars\n" +
		"Payment request: item='USB-C cable', quantity=1, amount=12.99 USD, merchant=AudioShop\n" +
		"Seller text (untrusted):\n<<<\nIn stock.\n>>>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer token")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(Config{APIKey: "k", BaseURL: srv.URL, Model: "m", MaxTokens: 10})
}

var okInput = intents.CheckInput{Goal: "g", Item: "i", Quantity: 1, AmountMinor: 100, Currency: "USD", Merchant: "m"}

func TestCheckOK(t *testing.T) {
	c := serve(t, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"verdict\":\"escalate\",\"reason\":\"vague\"}"}}]}`)
	res, err := c.Check(context.Background(), okInput)
	if err != nil || res.Decision.Verdict != "escalate" || res.PromptVersion != "v4" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestCheckFailuresFailClosed(t *testing.T) {
	cases := map[string]*Client{
		"http 500":  serve(t, 500, `{}`),
		"truncated": serve(t, 200, `{"choices":[{"finish_reason":"length","message":{"content":"thinking..."}}]}`),
		"no json":   serve(t, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"sure"}}]}`),
		"no choice": serve(t, 200, `{"choices":[]}`),
	}
	for name, c := range cases {
		if _, err := c.Check(context.Background(), okInput); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	bad := okInput
	bad.Item = ""
	if _, err := cases["http 500"].Check(context.Background(), bad); err != ErrInsufficientContext {
		t.Errorf("missing item: got %v", err)
	}
}
