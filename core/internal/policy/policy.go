package policy

type Verdict string

const (
	Pass Verdict = "pass"
	Deny Verdict = "deny"
)

type Policy struct {
	AllowedMerchants []string // exact match, case-insensitive
	Currency         string   // e.g. "USD"
	MaxPerPayment    int64
	AutoApproveMax   int64
}

type Request struct {
	Merchant        string
	Amount          int64
	Currency        string
	BudgetRemaining int64 // remaining budget for this agent, from the ledger
}

type Result struct {
	Verdict    Verdict
	Code       string
	Reason     string
	NeedsHuman bool // true when Pass but amount is above AutoApproveMax
}

func Evaluate(p Policy, r Request) Result {
	if r.Amount <= 0 {
		return deny("invalid_amount", "amount must be greater than zero")
	}
	if !strings.EqualFold(strings.TrimSpace(r.Currency), strings.TrimSpace(p.Currency)) {
		return deny("currency_not_allowed", "currency does not match the policy")
	}
	if !merchantAllowed(p.AllowedMerchants, r.Merchant) {
		return deny("merchant_not_allowed", "merchant is not on the allowlist")
	}
	if p.MaxPerPayment > 0 && r.Amount > p.MaxPerPayment {
		return deny("over_payment_limit", "amount exceeds the per-payment maximum")
	}
	if r.Amount > r.BudgetRemaining {
		return deny("over_budget", "amount exceeds the remaining budget")
	}