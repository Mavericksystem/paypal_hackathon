package api_test

import (
	"context"
	"errors"

	"github.com/Mavericksystem/warden/core/internal/intents"
)

const validBody = `{"agent_id":"agent-1","budget_id":"b-1","idempotency_key":"k1","amount_minor":1000,"currency":"USD","merchant":"acme","recipient":"acme-payments","goal":"buy API credits"}`

type fakeSvc struct {
	create  func(context.Context, intents.CreateInput) (intents.Intent, error)
	get     func(context.Context, string) (intents.Intent, error)
	creates int
}

func (f *fakeSvc) Create(ctx context.Context, in intents.CreateInput) (intents.Intent, error) {
	f.creates++
	if f.create == nil {
		return intents.Intent{}, errors.New("unexpected Create call")
	}
	return f.create(ctx, in)
}

func (f *fakeSvc) Get(ctx context.Context, id string) (intents.Intent, error) {
	if f.get == nil {
		return intents.Intent{}, errors.New("unexpected Get call")
	}
	return f.get(ctx, id)
}
