CREATE UNIQUE INDEX IF NOT EXISTS payments_merchant_identity ON payments (merchant_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS sandbox_channel_account_merchant_identity
    ON sandbox_channel_accounts (merchant_id, id);

CREATE TABLE IF NOT EXISTS sandbox_chargebacks (
    account_id text NOT NULL,
    id text NOT NULL,
    merchant_id text NOT NULL,
    payment_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('opened', 'won', 'lost')),
    currency text NOT NULL CHECK (currency IN ('USD', 'BRL', 'INR')),
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 999999999999),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (merchant_id, payment_id) REFERENCES payments (merchant_id, id),
    FOREIGN KEY (merchant_id, account_id) REFERENCES sandbox_channel_accounts (merchant_id, id)
);

CREATE INDEX IF NOT EXISTS sandbox_chargebacks_recent
    ON sandbox_chargebacks (merchant_id, updated_at DESC, account_id, id);

CREATE TABLE IF NOT EXISTS sandbox_chargeback_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id text NOT NULL,
    chargeback_id text NOT NULL,
    event_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('opened', 'won', 'lost')),
    UNIQUE (account_id, event_id),
    FOREIGN KEY (account_id, chargeback_id) REFERENCES sandbox_chargebacks (account_id, id),
    FOREIGN KEY (account_id, event_id) REFERENCES collection_inbox (account_id, event_id)
);

CREATE INDEX IF NOT EXISTS sandbox_chargeback_history
    ON sandbox_chargeback_events (account_id, chargeback_id, sequence);
