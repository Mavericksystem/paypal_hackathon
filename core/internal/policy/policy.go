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
