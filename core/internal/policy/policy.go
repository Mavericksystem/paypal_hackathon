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
