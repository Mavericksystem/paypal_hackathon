package paypal

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrDeclined = errors.New("paypal declined the payment")

type CaptureRequest struct {
	RequestID   string
	AmountMinor int64
	Currency    string
	Merchant    string
	Recipient   string
}

// CaptureResult carries what PayPal says it captured. The caller compares it
// with the intent, so a real client must always fill both fields.
type CaptureResult struct {
	OrderID     string
	AmountMinor int64
	Currency    string
}

type LookupStatus string

const (
	StatusNotFound LookupStatus = "not_found"
	StatusCaptured LookupStatus = "captured"
	StatusDeclined LookupStatus = "declined"
)

// LookupResult carries the captured amount and currency when Status is
// StatusCaptured. They are compared with the intent before any money is committed.
type LookupResult struct {
	Status      LookupStatus
	OrderID     string
	AmountMinor int64
	Currency    string
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
	// FaultOverCharge captures one minor unit more than requested and reports
	// success, to test that the amount is verified.
	FaultOverCharge
)

type charge struct {
	amount   int64
	currency string
	orderID  string
}

type Fake struct {
	mu       sync.Mutex
	fault    Fault
	calls    int
	lookups  int
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

// Lookups is how many times Lookup was invoked.
func (f *Fake) Lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
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

// Seed records a USD charge that happened without going through Capture.
func (f *Fake) Seed(requestID string, amountMinor int64) {
	f.SeedWith(requestID, amountMinor, "USD")
}

func (f *Fake) SeedWith(requestID string, amountMinor int64, currency string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.charges[requestID] = charge{amount: amountMinor, currency: currency, orderID: "ORDER-" + requestID}
}

func (c charge) result() CaptureResult {
	return CaptureResult{OrderID: c.orderID, AmountMinor: c.amount, Currency: c.currency}
}

func (f *Fake) Capture(ctx context.Context, req CaptureRequest) (CaptureResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++

	if c, ok := f.charges[req.RequestID]; ok {
		return c.result(), nil
	}
	if f.declined[req.RequestID] {
		return CaptureResult{}, fmt.Errorf("%w: repeated request", ErrDeclined)
	}

	c := charge{amount: req.AmountMinor, currency: req.Currency, orderID: "ORDER-" + req.RequestID}
	switch f.fault {
	case FaultDecline:
		f.declined[req.RequestID] = true
		return CaptureResult{}, fmt.Errorf("%w: card refused", ErrDeclined)
	case FaultTimeoutBeforeCharge:
		return CaptureResult{}, context.DeadlineExceeded
	case FaultTimeoutAfterCharge:
		f.charges[req.RequestID] = c
		return CaptureResult{}, context.DeadlineExceeded
	case FaultOverCharge:
		c.amount++
	}
	f.charges[req.RequestID] = c
	return c.result(), nil
}

func (f *Fake) Lookup(ctx context.Context, requestID string) (LookupResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if c, ok := f.charges[requestID]; ok {
		return LookupResult{Status: StatusCaptured, OrderID: c.orderID, AmountMinor: c.amount, Currency: c.currency}, nil
	}
	if f.declined[requestID] {
		return LookupResult{Status: StatusDeclined}, nil
	}
	return LookupResult{Status: StatusNotFound}, nil
}
