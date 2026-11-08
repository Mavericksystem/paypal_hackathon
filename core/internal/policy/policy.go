package policy

type Verdict string

const (
	Pass Verdict = "pass"
	Deny Verdict = "deny"
)

type Policy struct {
	AllowedMerchants []string // exact, case-sensitive match
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
	if r.Currency != p.Currency {
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

	res := Result{Verdict: Pass, Code: "ok", Reason: "all rules passed"}
	if p.AutoApproveMax > 0 && r.Amount > p.AutoApproveMax {
		res.NeedsHuman = true
		res.Reason = "rules passed, amount above auto-approve limit"
	}
	return res
}

func deny(code, reason string) Result {
	return Result{Verdict: Deny, Code: code, Reason: reason}
}

func merchantAllowed(allowed []string, merchant string) bool {
	if merchant == "" {
		return false
	}
	for _, a := range allowed {
		if a == merchant {
			return true
		}
	}
	return false
}
