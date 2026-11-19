package paypal

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrDecalined = errors.New("paypal declined the payment")

type CaptureRequest struct {
	RequestID string
	AmountMinor int64
	Currency string
	Merchant string
	Recipient string
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