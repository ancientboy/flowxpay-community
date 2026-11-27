ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS next_check_at timestamptz,
    ADD COLUMN IF NOT EXISTS check_attempts integer NOT NULL DEFAULT 0;
UPDATE payments SET next_check_at=now()+interval '5 seconds'
    WHERE status='pending_confirmation' AND route_snapshot IS NOT NULL
        AND next_check_at IS NULL AND check_attempts=0;
CREATE INDEX IF NOT EXISTS payments_due_check ON payments (next_check_at) WHERE next_check_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS sandbox_refunds (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    payment_id text NOT NULL REFERENCES payments(id),
    idempotency_key text NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 999999999999),
    currency text NOT NULL,
    route_snapshot jsonb NOT NULL,
    payment_reference text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending_confirmation', 'succeeded', 'failed')),
    channel_reference text NOT NULL,
    failure_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    next_check_at timestamptz,
    check_attempts integer NOT NULL DEFAULT 0,
    UNIQUE (merchant_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS refunds_by_payment ON sandbox_refunds (payment_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS refunds_due_check ON sandbox_refunds (next_check_at) WHERE next_check_at IS NOT NULL;

-- The mock channel's response store is written separately from the platform state.
CREATE TABLE IF NOT EXISTS sandbox_channel_receipts (
    account_id text NOT NULL,
    subject_id text NOT NULL,
    reference text NOT NULL,
    action text NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, subject_id)
);

CREATE TABLE IF NOT EXISTS collection_inbox (
    account_id text NOT NULL,
    event_id text NOT NULL,
    merchant_id text NOT NULL REFERENCES merchants(id),
    payment_id text NOT NULL REFERENCES payments(id),
    subject_id text NOT NULL,
    body_hash bytea NOT NULL,
    outcome text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    replay_conflict_at timestamptz,
    PRIMARY KEY (account_id, event_id)
);
ALTER TABLE collection_inbox ADD COLUMN IF NOT EXISTS replay_conflict_at timestamptz;
CREATE INDEX IF NOT EXISTS collection_inbox_terminal_conflicts ON collection_inbox (merchant_id, received_at DESC)
    WHERE outcome='terminal_conflict';
CREATE INDEX IF NOT EXISTS collection_inbox_replay_conflicts ON collection_inbox (merchant_id, replay_conflict_at DESC)
    WHERE replay_conflict_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS collection_outbox (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    payment_id text NOT NULL REFERENCES payments(id),
    subject_id text NOT NULL,
    payload bytea NOT NULL,
    destination text NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'delivered', 'failed', 'unconfigured')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS collection_outbox_due ON collection_outbox (next_attempt_at) WHERE state='pending';
CREATE INDEX IF NOT EXISTS collection_outbox_anomalies ON collection_outbox (merchant_id, created_at DESC)
    WHERE state='failed';
