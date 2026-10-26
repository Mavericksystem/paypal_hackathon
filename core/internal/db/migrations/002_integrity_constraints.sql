ALTER TABLE intents
    ADD CONSTRAINT intents_id_budget_key UNIQUE (id, budget_id);

ALTER TABLE intents
    ADD CONSTRAINT intents_state_valid CHECK (
        state IN (
            'created',
            'policy_checked',
            'denied',
            'pending_approval',
            'approved',
            'rejected',
            'reserved',
            'executing',
            'captured',
            'settled',
            'failed',
            'uncertain'
        )
    );

ALTER TABLE reservations
    ADD CONSTRAINT reservations_intent_budget_match
    FOREIGN KEY (intent_id, budget_id)
    REFERENCES intents (id, budget_id);
