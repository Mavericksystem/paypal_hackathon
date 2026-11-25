package main

import (
	"reflect"
	"testing"
)

func goodPolicyArgs() []string {
	return []string{
		"-agent", "agent-1", "-budget", "b-1", "-merchants", "acme, globex", "-currency", "USD",
		"-max-per-payment", "100000", "-auto-approve-max", "5000", "-by", "edward",
	}
}

func TestParsePolicyOK(t *testing.T) {
	got, err := parsePolicy(goodPolicyArgs())
	if err != nil {
		t.Fatal(err)
	}
	if got.changedBy != "edward" || got.in.AgentID != "agent-1" || got.in.BudgetID != "b-1" ||
		got.in.Currency != "USD" || got.in.MaxPerPaymentMinor != 100000 || got.in.AutoApproveMaxMinor != 5000 {
		t.Fatalf("parsed = %+v", got)
	}
	if !reflect.DeepEqual(got.in.AllowedMerchants, []string{"acme", "globex"}) {
		t.Fatalf("merchants = %v", got.in.AllowedMerchants)
	}
}
