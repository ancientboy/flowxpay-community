CREATE TABLE IF NOT EXISTS sandbox_channel_price_versions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id text NOT NULL REFERENCES sandbox_platform_channels(id),
    merchant_id text REFERENCES merchants(id),
    direction text NOT NULL CHECK (direction IN ('collection', 'payout')),
    currency text NOT NULL CHECK (currency IN ('BRL', 'INR', 'USD')),
    kind text NOT NULL CHECK (kind IN ('cost', 'default', 'override')),
    bps integer CHECK (bps BETWEEN 0 AND 10000),
    fixed_minor bigint CHECK (fixed_minor BETWEEN 0 AND 999999999999),
    effective_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    changed_by text NOT NULL,
    CHECK ((kind = 'override') = (merchant_id IS NOT NULL)),
    CHECK ((bps IS NULL) = (fixed_minor IS NULL)),
    CHECK (bps IS NOT NULL OR kind = 'override')
);

CREATE INDEX IF NOT EXISTS sandbox_channel_prices_latest ON sandbox_channel_price_versions
    (channel_id, direction, currency, kind, merchant_id, id DESC);

ALTER TABLE payments ADD COLUMN IF NOT EXISTS pricing_snapshot jsonb;
ALTER TABLE sandbox_payouts ADD COLUMN IF NOT EXISTS pricing_snapshot jsonb;

-- Give the built-in mock accounts a shared catalog identity for each existing provider.
-- Assignment, account IDs and sandbox balances are preserved.
WITH builtin(id, name, provider) AS (VALUES
    ('channel_builtin_pix', 'Mock Pix', 'mock_pix'),
    ('channel_builtin_card', 'Mock Card', 'mock_card'),
    ('channel_builtin_india', 'Mock India redirect', 'mock_in_redirect')
)
INSERT INTO sandbox_platform_channels (id, name, provider, enabled)
SELECT id, name, provider, true FROM builtin
ON CONFLICT DO NOTHING;

UPDATE sandbox_channel_accounts a SET platform_channel_id=c.id
FROM sandbox_platform_channels c
WHERE c.provider=a.provider AND c.id IN ('channel_builtin_pix','channel_builtin_card','channel_builtin_india')
    AND a.merchant_id IN ('M10028','M20029')
    AND a.id='default_' || a.merchant_id || '_' || a.provider
    AND a.platform_channel_id IS NULL
    AND NOT EXISTS (SELECT 1 FROM sandbox_channel_accounts other
        WHERE (other.merchant_id, other.site_id, other.platform_channel_id)=(a.merchant_id, a.site_id, c.id));
