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