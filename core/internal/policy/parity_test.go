package policy

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
