ALTER TABLE merchant_sites ADD COLUMN IF NOT EXISTS is_default boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS merchant_one_default_site
    ON merchant_sites (merchant_id) WHERE is_default;

ALTER TABLE sandbox_channel_accounts
    DROP CONSTRAINT IF EXISTS sandbox_channel_accounts_provider_check,
    ADD CONSTRAINT sandbox_channel_accounts_provider_check
        CHECK (provider IN ('mock_pix', 'mock_card', 'mock_in_redirect', 'mock_in_qr', 'mock_in_unknown')),
    ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS requests_per_second integer NOT NULL DEFAULT 60
        CHECK (requests_per_second BETWEEN 1 AND 1000);

-- Preserve site-less requests for the two built-in sandbox merchants.
INSERT INTO merchant_sites (id, merchant_id, name, is_default)
    SELECT 'default_' || id, id, 'Default sandbox site', true FROM merchants
    WHERE id IN ('M10028', 'M20029')
    ON CONFLICT DO NOTHING;

INSERT INTO sandbox_channel_accounts (id, merchant_id, site_id, provider)
    SELECT 'default_' || merchant_id || '_' || provider, merchant_id, id, provider
    FROM merchant_sites CROSS JOIN (VALUES ('mock_pix'), ('mock_card'), ('mock_in_redirect')) AS providers(provider)
    WHERE is_default AND id = 'default_' || merchant_id AND merchant_id IN ('M10028', 'M20029')
    ON CONFLICT DO NOTHING;

CREATE INDEX IF NOT EXISTS sandbox_account_routing
    ON sandbox_channel_accounts (merchant_id, site_id, provider, id) WHERE enabled;

-- Existing orders retain their original route and behavior. New routes are persisted before dispatch.
ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_status_check,
    ADD CONSTRAINT payments_status_check
        CHECK (status IN ('requires_action', 'succeeded', 'pending_confirmation', 'failed')),
    ADD COLUMN IF NOT EXISTS route_snapshot jsonb,
    ADD COLUMN IF NOT EXISTS channel_action text,
    ADD COLUMN IF NOT EXISTS channel_reference text,
    ADD COLUMN IF NOT EXISTS failure_code text;

CREATE TABLE IF NOT EXISTS sandbox_channel_rate (
    account_id text PRIMARY KEY REFERENCES sandbox_channel_accounts(id),
    window_start timestamptz NOT NULL,
    attempts integer NOT NULL CHECK (attempts > 0)
);
