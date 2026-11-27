-- Synthetic sandbox policy, in the payout account's own currency.
ALTER TABLE sandbox_payout_accounts
    ADD COLUMN IF NOT EXISTS dispatches_per_second integer NOT NULL DEFAULT 10
        CHECK (dispatches_per_second BETWEEN 1 AND 1000),
    ADD COLUMN IF NOT EXISTS daily_payout_minor bigint NOT NULL DEFAULT 500000
        CHECK (daily_payout_minor > 0);

ALTER TABLE sandbox_payouts ADD COLUMN IF NOT EXISTS submitted_at timestamptz;

-- Legacy receipts have no timestamp. Use the last recorded transition as a
-- conservative estimate and include only orders with durable mock receipts.
UPDATE sandbox_payouts AS p SET submitted_at=p.updated_at
FROM sandbox_channel_receipts AS r
WHERE p.submitted_at IS NULL AND r.account_id=p.account_id
    AND r.subject_id=p.id AND r.reference=p.channel_reference;

CREATE INDEX IF NOT EXISTS sandbox_payouts_account_submission
    ON sandbox_payouts (merchant_id, account_id, submitted_at)
    INCLUDE (status, amount_minor) WHERE submitted_at IS NOT NULL;
