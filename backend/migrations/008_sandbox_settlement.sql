CREATE TABLE IF NOT EXISTS sandbox_settlement_batches (
    merchant_id text NOT NULL,
    account_id text NOT NULL,
    currency text NOT NULL CHECK (currency IN ('USD','BRL','INR')),
    id text NOT NULL,
    statement_date date NOT NULL,
    settlement_status text NOT NULL CHECK (settlement_status IN ('pending','settled')),
    withdrawal_status text NOT NULL CHECK (withdrawal_status IN ('not_reported','pending','completed','failed')),
    withdrawal_reference text NOT NULL,
    body_hash bytea NOT NULL,
    imported_by text NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    row_count integer NOT NULL CHECK (row_count BETWEEN 1 AND 500),
    gross_minor bigint NOT NULL,
    fee_minor bigint NOT NULL,
    net_minor bigint NOT NULL,
    PRIMARY KEY (merchant_id, account_id, currency, id),
    FOREIGN KEY (merchant_id, account_id) REFERENCES sandbox_channel_accounts (merchant_id, id),
    FOREIGN KEY (merchant_id, imported_by) REFERENCES merchant_members (merchant_id, id),
    CHECK ((withdrawal_status = 'not_reported') = (withdrawal_reference = ''))
);

CREATE TABLE IF NOT EXISTS sandbox_settlement_entries (
    merchant_id text NOT NULL,
    account_id text NOT NULL,
    currency text NOT NULL,
    batch_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('collection','refund','payout','reversal')),
    channel_reference text NOT NULL,
    reverses_entry text,
    gross_minor bigint NOT NULL,
    fee_minor bigint NOT NULL,
    net_minor bigint NOT NULL,
    order_id text,
    order_status text,
    expected_gross_minor bigint,
    match_status text NOT NULL CHECK (match_status IN ('matched','missing','ambiguous','amount_mismatch','status_mismatch')),
    acknowledged boolean NOT NULL DEFAULT false,
    revision integer NOT NULL DEFAULT 0 CHECK (revision >= 0),
    PRIMARY KEY (merchant_id, account_id, currency, id),
    FOREIGN KEY (merchant_id, account_id, currency, batch_id)
        REFERENCES sandbox_settlement_batches (merchant_id, account_id, currency, id),
    FOREIGN KEY (merchant_id, account_id, currency, reverses_entry)
        REFERENCES sandbox_settlement_entries (merchant_id, account_id, currency, id),
    CHECK ((kind = 'reversal') = (reverses_entry IS NOT NULL)),
    CHECK (reverses_entry IS NULL OR reverses_entry <> id)
);

CREATE UNIQUE INDEX IF NOT EXISTS settlement_one_base_reference ON sandbox_settlement_entries
    (merchant_id, account_id, currency, kind, channel_reference) WHERE reverses_entry IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS settlement_one_reversal ON sandbox_settlement_entries
    (merchant_id, account_id, currency, reverses_entry) WHERE reverses_entry IS NOT NULL;
CREATE INDEX IF NOT EXISTS settlement_entries_by_batch ON sandbox_settlement_entries
    (merchant_id, account_id, currency, batch_id, id);

CREATE INDEX IF NOT EXISTS settlement_entries_by_order ON sandbox_settlement_entries
    (merchant_id, account_id, currency, order_id, kind) WHERE order_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS sandbox_settlement_daily (
    merchant_id text NOT NULL,
    account_id text NOT NULL,
    currency text NOT NULL CHECK (currency IN ('USD','BRL','INR')),
    statement_date date NOT NULL,
    batch_count bigint NOT NULL,
    row_count bigint NOT NULL,
    gross_minor numeric(38,0) NOT NULL,
    fee_minor numeric(38,0) NOT NULL,
    net_minor numeric(38,0) NOT NULL,
    PRIMARY KEY (merchant_id, account_id, currency, statement_date),
    FOREIGN KEY (merchant_id, account_id) REFERENCES sandbox_channel_accounts (merchant_id, id)
);

CREATE TABLE IF NOT EXISTS sandbox_settlement_actions (
    merchant_id text NOT NULL,
    account_id text NOT NULL,
    currency text NOT NULL,
    entry_id text NOT NULL,
    revision integer NOT NULL,
    member_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('acknowledge','reopen','recheck')),
    note text NOT NULL,
    before_check jsonb NOT NULL,
    after_check jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (merchant_id, account_id, currency, entry_id, revision),
    FOREIGN KEY (merchant_id, account_id, currency, entry_id)
        REFERENCES sandbox_settlement_entries (merchant_id, account_id, currency, id),
    FOREIGN KEY (merchant_id, member_id) REFERENCES merchant_members (merchant_id, id)
);

CREATE INDEX IF NOT EXISTS settlement_payment_reference ON payments
    (merchant_id, (route_snapshot->>'account_id'), currency, channel_reference, id);
CREATE INDEX IF NOT EXISTS settlement_refund_reference ON sandbox_refunds
    (merchant_id, (route_snapshot->>'account_id'), currency, channel_reference, id);
CREATE INDEX IF NOT EXISTS settlement_payout_reference ON sandbox_payouts
    (merchant_id, account_id, currency, channel_reference, id);
CREATE INDEX IF NOT EXISTS settlement_payments_by_order ON payments
    (merchant_id, (route_snapshot->>'account_id'), currency, id);
CREATE INDEX IF NOT EXISTS settlement_refunds_by_order ON sandbox_refunds
    (merchant_id, (route_snapshot->>'account_id'), currency, id);
CREATE INDEX IF NOT EXISTS settlement_payouts_by_order ON sandbox_payouts
    (merchant_id, account_id, currency, id);
