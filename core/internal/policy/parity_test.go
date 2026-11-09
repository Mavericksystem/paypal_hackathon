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