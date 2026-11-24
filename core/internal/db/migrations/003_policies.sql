CREATE TABLE policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    agent_id TEXT NOT NULL,
    budget_id UUID NOT NULL REFERENCES budgets (id),
    allowed_merchants TEXT[] NOT NULL,
    currency CHAR(3) NOT NULL,
    -- 0 means no per-payment cap. This mirrors policy.Policy.MaxPerPayment.
    max_per_payment_minor BIGINT NOT NULL CHECK (max_per_payment_minor >= 0),
    -- 0 means no human approval threshold. This mirrors policy.Policy.AutoApproveMax.
    auto_approve_max_minor BIGINT NOT NULL CHECK (auto_approve_max_minor >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One policy per agent and budget. A missing row means nothing is decided.
    UNIQUE (agent_id, budget_id)
);
