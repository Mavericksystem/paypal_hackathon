package policy

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
