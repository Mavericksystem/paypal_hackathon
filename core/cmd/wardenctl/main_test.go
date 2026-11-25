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

func TestParsePolicyAcceptsExplicitZerosAndEmptyMerchants(t *testing.T) {
	args := []string{
		"-agent", "a", "-budget", "b", "-merchants", "", "-currency", "USD",
		"-max-per-payment", "0", "-auto-approve-max", "0", "-by", "edward",
	}
	got, err := parsePolicy(args)
	if err != nil {
		t.Fatal(err)
	}
	if got.in.AllowedMerchants != nil || got.in.MaxPerPaymentMinor != 0 || got.in.AutoApproveMaxMinor != 0 {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestParsePolicyRejectsUnknownFlagAndExtraArgs(t *testing.T) {
	if _, err := parsePolicy(append(goodPolicyArgs(), "-nope", "x")); err == nil {
		t.Error("unknown flag: want an error")
	}
	if _, err := parsePolicy(append(goodPolicyArgs(), "stray")); err == nil {
		t.Error("stray argument: want an error")
	}
}

func TestParseBudget(t *testing.T) {
	got, err := parseBudget([]string{"-name", "demo", "-currency", "USD", "-limit", "1000000"})
	if err != nil {
		t.Fatal(err)
	}
	if got.name != "demo" || got.currency != "USD" || got.limit != 1000000 || got.parent != "" {
		t.Fatalf("parsed = %+v", got)
	}

	bad := map[string][]string{
		"missing limit":    {"-name", "demo", "-currency", "USD"},
		"lower currency":   {"-name", "demo", "-currency", "usd", "-limit", "1"},
		"digit currency":   {"-name", "demo", "-currency", "123", "-limit", "1"},
		"blank name":       {"-name", " ", "-currency", "USD", "-limit", "1"},
		"negative limit":   {"-name", "demo", "-currency", "USD", "-limit", "-5"},
		"unexpected extra": {"-name", "demo", "-currency", "USD", "-limit", "1", "extra"},
	}
	for name, args := range bad {
		if _, err := parseBudget(args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
