package paypal

import (
	"context"
	"errors"
	"sync"
)

var ErrDecalined = errors.New("paypal declined the payment")

type CaptureRequest struct {
	RequestID   string
	AmountMinor int64
	Currency    string
	Merchant    string
	Recipient   string
}

type CaptureResult struct {
	OrderID string
}

type LookupStatus string

const (
	StatusNotFound LookupStatus = "not_found"
	StatusCaptured LookupStatus = "captured"
	StatusDeclined LookupStatus = "declined"
)

type LookupResult struct {
	Status  LookupStatus
	OrderID string
}

type Client interface {
	Capture(ctx context.Context, req CaptureRequest) (CaptureResult, error)
	Lookup(ctx context.Context, requestID string) (LookupResult, error)
}

type Fault int

const (
	FaultNone Fault = iota
	FaultDecline
	FaultTimeoutAfterCharge
	FaultTimeoutBeforeCharge
)

type charge struct {
	amount  int64
	orderID string
}

type Fake struct {
	mu       sync.Mutex
	fault    Fault
	calls    int
	charges  map[string]charge
	declined map[string]bool
}

func NewFake() *Fake {
	return &Fake{charges: map[string]charge{}, declined: map[string]bool{}}
}

func (f *Fake) SetFault(fault Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fault = fault
}

func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *Fake) Charges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.charges)
}

func (f *Fake) TotalMinor() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum int64
	for _, c := range f.charges {
		sum += c.amount
	}
	return sum
}
