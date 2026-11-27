CREATE UNIQUE INDEX IF NOT EXISTS sandbox_account_owner_key
    ON sandbox_channel_accounts (merchant_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS merchant_member_owner_key
    ON merchant_members (merchant_id, id);

CREATE TABLE IF NOT EXISTS sandbox_payout_accounts (
    account_id text PRIMARY KEY,
    merchant_id text NOT NULL,
    currency text NOT NULL CHECK (currency IN ('USD','BRL','INR')),
    recipient_type text NOT NULL CHECK (recipient_type IN ('bank_account','pix','upi')),
    available_minor bigint NOT NULL CHECK (available_minor >= 0),
    reserved_minor bigint NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
    max_payout_minor bigint NOT NULL CHECK (max_payout_minor BETWEEN 1 AND 999999999999),
    simulation text NOT NULL DEFAULT 'success' CHECK (simulation IN ('success','disconnect','failure','unresolved')),
    FOREIGN KEY (merchant_id, account_id) REFERENCES sandbox_channel_accounts (merchant_id, id),
    UNIQUE (merchant_id, account_id, currency, recipient_type)
);

-- Only explicitly provisioned accounts can originate a payout. Inventory creation grants no payout balance.
INSERT INTO sandbox_payout_accounts (account_id, merchant_id, currency, recipient_type, available_minor, max_payout_minor)
SELECT a.id, a.merchant_id, rule.currency, rule.recipient_type, 1000000, 250000
FROM sandbox_channel_accounts a
JOIN (VALUES ('mock_card','USD','bank_account'),('mock_pix','BRL','pix'),('mock_in_redirect','INR','upi'))
    AS rule(provider,currency,recipient_type) USING (provider)
WHERE a.merchant_id IN ('M10028','M20029') AND a.id='default_' || a.merchant_id || '_' || a.provider
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS sandbox_payout_recipients (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    request_key text NOT NULL,
    recipient_type text NOT NULL CHECK (recipient_type IN ('bank_account','pix','upi')),
    test_suffix text NOT NULL CHECK (test_suffix ~ '^[0-9]{4}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, request_key),
    UNIQUE (merchant_id, id, recipient_type)
);

CREATE TABLE IF NOT EXISTS sandbox_payouts (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    merchant_reference text NOT NULL,
    request_key text NOT NULL,
    account_id text NOT NULL,
    recipient_id text NOT NULL,
    recipient_type text NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 999999999999),
    currency text NOT NULL,
    status text NOT NULL CHECK (status IN ('draft','processing','pending_confirmation','succeeded','failed','rejected')),
    created_by text NOT NULL,
    reviewed_by text,
    reviewed_at timestamptz,
    channel_reference text NOT NULL,
    transfer_reference text NOT NULL DEFAULT '',
    failure_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    next_check_at timestamptz,
    check_attempts integer NOT NULL DEFAULT 0,
    UNIQUE (merchant_id, request_key),
    UNIQUE (merchant_id, merchant_reference),
    UNIQUE (merchant_id, id),
    FOREIGN KEY (merchant_id, account_id, currency, recipient_type)
        REFERENCES sandbox_payout_accounts (merchant_id, account_id, currency, recipient_type),
    FOREIGN KEY (merchant_id, recipient_id, recipient_type)
        REFERENCES sandbox_payout_recipients (merchant_id, id, recipient_type),
    FOREIGN KEY (merchant_id, created_by) REFERENCES merchant_members (merchant_id, id),
    FOREIGN KEY (merchant_id, reviewed_by) REFERENCES merchant_members (merchant_id, id),
    CHECK (reviewed_by IS NULL OR reviewed_by <> created_by),
    CHECK ((status = 'draft') = (reviewed_by IS NULL)),
    CHECK ((reviewed_by IS NULL) = (reviewed_at IS NULL)),
    CHECK ((status = 'succeeded') = (transfer_reference <> ''))
);
CREATE INDEX IF NOT EXISTS sandbox_payouts_by_merchant ON sandbox_payouts (merchant_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS sandbox_payouts_dispatch ON sandbox_payouts (updated_at, id) WHERE status='processing';
CREATE INDEX IF NOT EXISTS sandbox_payouts_check ON sandbox_payouts (next_check_at, id) WHERE next_check_at IS NOT NULL;

ALTER TABLE collection_inbox ALTER COLUMN payment_id DROP NOT NULL;
ALTER TABLE collection_inbox ADD COLUMN IF NOT EXISTS payout_id text;
ALTER TABLE collection_outbox ALTER COLUMN payment_id DROP NOT NULL;
ALTER TABLE collection_outbox ADD COLUMN IF NOT EXISTS payout_id text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='collection_inbox'::regclass AND conname='inbox_payout_owner') THEN
        ALTER TABLE collection_inbox
            ADD CONSTRAINT inbox_payout_owner FOREIGN KEY (merchant_id, payout_id) REFERENCES sandbox_payouts (merchant_id,id),
            ADD CONSTRAINT inbox_one_business_target CHECK (num_nonnulls(payment_id,payout_id)=1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='collection_outbox'::regclass AND conname='outbox_payout_owner') THEN
        ALTER TABLE collection_outbox
            ADD CONSTRAINT outbox_payout_owner FOREIGN KEY (merchant_id, payout_id) REFERENCES sandbox_payouts (merchant_id,id),
            ADD CONSTRAINT outbox_one_business_target CHECK (num_nonnulls(payment_id,payout_id)=1);
    END IF;
END $$;
