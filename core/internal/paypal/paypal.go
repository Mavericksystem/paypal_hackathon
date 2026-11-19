package paypal

import (
	"context"
	"errors"
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
