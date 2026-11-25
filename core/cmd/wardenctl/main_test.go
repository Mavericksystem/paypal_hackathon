package main

import (
	"reflect"
	"strings"
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

func TestParsePolicyRequiresEveryFlag(t *testing.T) {
	all := goodPolicyArgs()
	for i := 0; i < len(all); i += 2 {
		name := all[i]
		var args []string
		args = append(args, all[:i]...)
		args = append(args, all[i+2:]...)
		_, err := parsePolicy(args)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: err = %v, want a missing flag error naming it", name, err)
		}
	}
}
