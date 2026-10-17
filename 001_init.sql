CREATE TABLE budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    parent_id UUID REFERENCES budgets (id),
    name TEXT NOT NULL,
    currency CHAR(3) NOT NULL,
    limit_minor BIGINT NOT NULL CHECK (limit_minor >= 0),
    reserved_minor BIGINT NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
    committed_minor BIGINT NOT NULL DEFAULT 0 CHECK (committed_minor >= 0),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Last line of defence for invariant I1, enforced by the database itself.
    CONSTRAINT budget_never_exceeded CHECK (
        reserved_minor + committed_minor <= limit_minor
    )
);

CREATE TABLE intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    agent_id TEXT NOT NULL,
    budget_id UUID NOT NULL REFERENCES budgets (id),
    state TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL,
    merchant TEXT NOT NULL,
    recipient TEXT NOT NULL,
    goal TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    paypal_order_id TEXT,
    version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- An agent that retries the same request gets the same intent back.
    UNIQUE (agent_id, idempotency_key)
);

CREATE TABLE reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    -- UNIQUE: one intent can reserve money at most once.
    intent_id UUID NOT NULL UNIQUE REFERENCES intents (id),
    budget_id UUID NOT NULL REFERENCES budgets (id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status TEXT NOT NULL CHECK (
        status IN (
            'reserved',
            'committed',
            'released'
        )
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    seq BIGINT PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL,
    intent_id UUID,
    actor TEXT NOT NULL,
    event TEXT NOT NULL,
    payload JSONB NOT NULL,
    prev_hash BYTEA NOT NULL,
    hash BYTEA NOT NULL
);

CREATE INDEX audit_log_intent_idx ON audit_log (intent_id);