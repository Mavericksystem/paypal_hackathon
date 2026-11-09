package policy

import (
	"encoding/json"
	"os"
	"testing"
)

type casesFile struct {
	Policy struct {
		Allowlist                   []string `json:"allowlist"`
		MaxPaymentMinor             int64    `json:"max_payment_minor"`
		Currency                    string   `json:"currency"`
		DefaultBudgetRemainingMinor int64    `json:"default_budget_remaining_minor"`
	} `json:"policy"`
	Cases []struct {
		ID       string `json:"id"`
		Category string `json:"category"`
		Request  struct {
			Merchant    string `json:"merchant"`
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"request"`
		BudgetRemainingMinor *int64 `json:"budget_remaining_minor"`
	} `json:"cases"`
}

func TestParityWithEvalCases(t *testing.T) {
	raw, err := os.ReadFile("../../../eval/cases.json")
	if err != nil {
		t.Fatalf("cannot read eval/cases.json: %v", err)
	}
	var f casesFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("cannot parse eval/cases.json: %v", err)
	}
	if len(f.Cases) != 30 {
		t.Fatalf("expected 30 cases, got %d", len(f.Cases))
	}

	pol := Policy{
		AllowedMerchants: f.Policy.Allowlist,
		Currency:         f.Policy.Currency,
		MaxPerPayment:    f.Policy.MaxPaymentMinor,
		AutoApproveMax:   0,
	}

	wantDenyCode := map[string]string{
		"R01": "over_payment_limit",
		"R02": "merchant_not_allowed",
		"R03": "currency_not_allowed",
		"R04": "over_budget",
	}

	denied := 0
	for _, c := range f.Cases {
		budget := f.Policy.DefaultBudgetRemainingMinor
		if c.BudgetRemainingMinor != nil {
			budget = *c.BudgetRemainingMinor
		}
		res := Evaluate(pol, Request{
			Merchant:        c.Request.Merchant,
			Amount:          c.Request.AmountMinor,
			Currency:        c.Request.Currency,
			BudgetRemaining: budget,
		})

		wantCode, shouldDeny := wantDenyCode[c.ID]
		if shouldDeny {
			denied++
			if res.Verdict != Deny || res.Code != wantCode {
				t.Errorf("%s: got %s/%s, want deny/%s", c.ID, res.Verdict, res.Code, wantCode)
			}
			continue
		}
		if res.Verdict != Pass {
			t.Errorf("%s (%s): rules denied (%s), but the evaluation sent it to the AI", c.ID, c.Category, res.Code)
		}
	}

	if denied != 4 {
		t.Errorf("rules denied %d cases, want exactly 4 (R01 to R04)", denied)
	}
}
