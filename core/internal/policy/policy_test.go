package policy

func basePolicy() Policy {
	return Policy{
		AllowedMerchants: []string{"Acme Books", "cloud-host.example"},
		Currency:         "USD",
		MaxPerPayment:    5000,
		AutoApproveMax:   2000,
	}
}
