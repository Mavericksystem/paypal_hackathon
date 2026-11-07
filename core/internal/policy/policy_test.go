package policy

import "testing"

func basePolicy() Policy {
	return Policy{
		AllowedMerchants: []string{"Acme Books", "cloud-host.example"},
		Currency:         "USD",
		MaxPerPayment:    5000,
		AutoApproveMax:   2000,
	}
}

func baseRequest() Request {
	return Request{
		Merchant:        "Acme Books",
		Amount:          1500,
		Currency:        "USD",
		BudgetRemaining: 10000,
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name      string
		mutatePol func(*Policy)
		mutateReq func(*Request)
		wantVerd  Verdict
		wantCode  string
		wantHuman bool
	}{
		{
			name:     "legit payment passes",
			wantVerd: Pass, wantCode: "ok",
		},
		{
			name:      "merchant match ignores case and spaces",
			mutateReq: func(r *Request) { r.Merchant = "  acme books " },
			wantVerd:  Pass, wantCode: "ok",
		},
		{
			name:      "currency match ignores case",
			mutateReq: func(r *Request) { r.Currency = "usd" },
			wantVerd:  Pass, wantCode: "ok",
		},
		{
			name:      "unknown merchant denied",
			mutateReq: func(r *Request) { r.Merchant = "Shady Store" },
			wantVerd:  Deny, wantCode: "merchant_not_allowed",
		},
		{
			name:      "empty merchant denied",
			mutateReq: func(r *Request) { r.Merchant = "   " },
			wantVerd:  Deny, wantCode: "merchant_not_allowed",
		},
		{
			name:      "wrong currency denied",
			mutateReq: func(r *Request) { r.Currency = "EUR" },
			wantVerd:  Deny, wantCode: "currency_not_allowed",
		},
		{
			name:      "zero amount denied",
			mutateReq: func(r *Request) { r.Amount = 0 },
			wantVerd:  Deny, wantCode: "invalid_amount",
		},
		{
			name:      "negative amount denied",
			mutateReq: func(r *Request) { r.Amount = -100 },
			wantVerd:  Deny, wantCode: "invalid_amount",
		},
		{
			name:      "over per-payment max denied",
			mutateReq: func(r *Request) { r.Amount = 5001 },
			wantVerd:  Deny, wantCode: "over_payment_limit",
		},
		{
			name:      "exactly at per-payment max passes, needs human",
			mutateReq: func(r *Request) { r.Amount = 5000 },
			wantVerd:  Pass, wantCode: "ok", wantHuman: true,
		},
		{
			name:      "over remaining budget denied",
			mutateReq: func(r *Request) { r.Amount = 1500; r.BudgetRemaining = 1499 },
			wantVerd:  Deny, wantCode: "over_budget",
		},
		{
			name:      "exactly the remaining budget passes",
			mutateReq: func(r *Request) { r.Amount = 1500; r.BudgetRemaining = 1500 },
			wantVerd:  Pass, wantCode: "ok",
		},
		{
			name:      "exactly at auto-approve limit needs no human",
			mutateReq: func(r *Request) { r.Amount = 2000 },
			wantVerd:  Pass, wantCode: "ok", wantHuman: false,
		},
		{
			name:      "above auto-approve limit needs human",
			mutateReq: func(r *Request) { r.Amount = 2001 },
			wantVerd:  Pass, wantCode: "ok", wantHuman: true,
		},
		{
			name:      "auto-approve not configured means no human gate",
			mutatePol: func(p *Policy) { p.AutoApproveMax = 0 },
			mutateReq: func(r *Request) { r.Amount = 4000 },
			wantVerd:  Pass, wantCode: "ok", wantHuman: false,
		},
		{
			name:      "max per payment of zero means no cap",
			mutatePol: func(p *Policy) { p.MaxPerPayment = 0 },
			mutateReq: func(r *Request) { r.Amount = 9000 },
			wantVerd:  Pass, wantCode: "ok", wantHuman: true,
		},
		{
			name:      "empty allowlist denies everything",
			mutatePol: func(p *Policy) { p.AllowedMerchants = nil },
			wantVerd:  Deny, wantCode: "merchant_not_allowed",
		},
		{
			name:      "invalid amount wins over bad merchant",
			mutateReq: func(r *Request) { r.Amount = 0; r.Merchant = "Shady Store" },
			wantVerd:  Deny, wantCode: "invalid_amount",
		},
		{
			name:      "currency wins over merchant",
			mutateReq: func(r *Request) { r.Currency = "EUR"; r.Merchant = "Shady Store" },
			wantVerd:  Deny, wantCode: "currency_not_allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, r := basePolicy(), baseRequest()
			if tc.mutatePol != nil {
				tc.mutatePol(&p)
			}
			if tc.mutateReq != nil {
				tc.mutateReq(&r)
			}
			got := Evaluate(p, r)
			if got.Verdict != tc.wantVerd {
				t.Fatalf("verdict = %q, want %q (code %q)", got.Verdict, tc.wantVerd, got.Code)
			}
			if got.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.NeedsHuman != tc.wantHuman {
				t.Errorf("NeedsHuman = %v, want %v", got.NeedsHuman, tc.wantHuman)
			}
			if got.Reason == "" {
				t.Error("reason must never be empty")
			}
			if got.Verdict == Deny && got.NeedsHuman {
				t.Error("a denied result must not ask for human confirmation")
			}
		})
	}
}
