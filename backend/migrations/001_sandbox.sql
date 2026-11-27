CREATE TABLE IF NOT EXISTS merchants (
    id text PRIMARY KEY,
    name text NOT NULL
);

INSERT INTO merchants (id, name) VALUES
    ('M10028', 'Cedar Commerce'),
    ('M20029', 'Harbor Retail')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS payments (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    merchant_reference text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    market text NOT NULL,
    currency text NOT NULL,
    method text NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 999999999999),
    status text NOT NULL CHECK (status IN ('requires_action', 'succeeded')),
    route text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now() + interval '20 minutes',
    paid_at timestamptz,
    CONSTRAINT merchant_idempotency UNIQUE (merchant_id, idempotency_key),
    CONSTRAINT merchant_reference UNIQUE (merchant_id, merchant_reference)
);

CREATE INDEX IF NOT EXISTS payments_merchant_recent_idx
    ON payments (merchant_id, created_at DESC, id DESC);
