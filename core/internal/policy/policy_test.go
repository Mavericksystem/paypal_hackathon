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

func TestEvaluation(t *testing.T) {
	test := []struct {
		name string
		mutatePol func(*Policy)
		mutateReq func(*Request)
		wantVerd Verdict
		wantCode string
		wantHuman bool
	}{
		name: "legit payment passes",
		wantedVerd: Pass, wnatCode: "ok",
	},
	{
		name: "merchant match igores case and spaces",
		muateReq: func(r *Request) { r.Merchant = "acme books"},
		wantVerd: Pass, wantCode: "ok",
	},
	{
		name: "currency match ignores case",
		mutateReq: func(r *Request) { r.Currency = "usd"},
		wantVerd: Pass, wnatCode: "ok",
	},
	{
		name: unknown merchant denied",
		muateReq: func(r *Request) { r.Merchat = "shady Store"},
		wantVerd: Deny, wantCode: "merchant_not_allowed",
	},
	{
		name: "empty merchant denied",
		muateReq: func(r *Request) { r.Merchant = "	 "},
		wantVerd: Deny, wantCode: "merchant_not_allowed",
	},
	{
		name: "wrong currency denied",
		mutateReq: func(r *Request) { r.Currnecy = "EUR"},
		wantVerd: Deny, wantCode: "currency_not_allowed",
	},
	{
		name: "zero amount denied",
		mutatereq: func(r *Request) { r.Amount = 0},
		wnatVerd: Deny, wantCode: "invalid_amount",
	},
	{
		name: "negative amount denied"
		mutatReq: func(r *Request) { r.Amount = -100},
		wantVerd: Deny, wantCode: "invalid_amount",
	},
	{
		name: "over per-payment max denied",
		mutateReq: func(r *Request) { r.Amount = 5001},
		wantVerd: Deny, wantCode: "over_payment_limit",
	},
	{
		name: "exactly at per-payment max passes, need human",
		mutateReq: func(r *Request) { r.Amount = 5000 },
		wantVerd: Pass, wantCode: "ok", wantHuman: true,
	},
	{
		name: "over remaining budget denied",
		muateReq: func(r *Request) { r.Amount = 1500; r.BudgetRemainig = 1499 },
		wantVerd: Deny, wantCode: "over_budget",
	},
	{
		name: "exactly the remaining  budget passes",
		mutateReq: func(r *Request) { r.Amount = 1500; r.BudgetRemaining = 1500 },
		wantVerd: pass, wantCode: "ok",
	},
	{
		name: "exactly at auto-approve limit needs no human",
		mutatReq: func(r *Request) { r.Amount = 2000 },
		wantVerd: Pass, wantCode: "ok", wantHuman: false,
	},
	{
		name: "max per payment of zero means no cap",
		mutatePol: func(p *Policy) { p.basePolicy().MaxPerPayment = 0},
		muateReq: func(r "Request) { r.Amount = 9000 },
		wantVerd: Pass, wantCode: "ok", wantHuman: true,
	},
}
