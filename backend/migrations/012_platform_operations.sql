CREATE TABLE IF NOT EXISTS sandbox_platform_channels (
    id text PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    provider text NOT NULL CHECK (provider IN ('mock_pix', 'mock_card', 'mock_in_redirect', 'mock_in_qr', 'mock_in_unknown')),
    enabled boolean NOT NULL
);

ALTER TABLE sandbox_channel_accounts
    ADD COLUMN IF NOT EXISTS platform_channel_id text REFERENCES sandbox_platform_channels(id);

CREATE UNIQUE INDEX IF NOT EXISTS sandbox_platform_channel_site
    ON sandbox_channel_accounts (merchant_id, site_id, platform_channel_id) WHERE platform_channel_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS sandbox_platform_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor text NOT NULL,
    action text NOT NULL,
    merchant_id text REFERENCES merchants(id),
    resource_id text NOT NULL,
    details jsonb NOT NULL
);

ALTER TABLE collection_route_changes
    ALTER COLUMN submitted_by DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS platform_submitted_by text,
    ADD COLUMN IF NOT EXISTS platform_approved_by text,
    ADD COLUMN IF NOT EXISTS cancelled_at timestamptz,
    DROP CONSTRAINT IF EXISTS collection_route_changes_check,
    DROP CONSTRAINT IF EXISTS route_actors;

ALTER TABLE collection_route_changes ADD CONSTRAINT route_actors CHECK (
    num_nonnulls(submitted_by, platform_submitted_by) = 1
    AND (platform_submitted_by IS NULL OR approved_by IS NULL)
    AND (submitted_by IS NULL OR platform_approved_by IS NULL)
    AND (approved_at IS NULL) = (num_nonnulls(approved_by, platform_approved_by) = 0)
    AND (cancelled_at IS NULL OR (platform_submitted_by IS NOT NULL AND approved_at IS NULL))
);

DROP INDEX IF EXISTS collection_one_pending_route_change;
CREATE UNIQUE INDEX IF NOT EXISTS collection_one_platform_pending_route
    ON collection_route_changes (merchant_id, site_id, market, currency, method)
    WHERE platform_submitted_by IS NOT NULL AND approved_at IS NULL AND cancelled_at IS NULL;

ALTER TABLE merchant_api_keys
    ADD COLUMN IF NOT EXISTS platform_created_by text,
    ADD COLUMN IF NOT EXISTS platform_revoked_by text,
    DROP CONSTRAINT IF EXISTS merchant_api_keys_check,
    DROP CONSTRAINT IF EXISTS merchant_api_keys_check1,
    DROP CONSTRAINT IF EXISTS api_key_actors;

ALTER TABLE merchant_api_keys ADD CONSTRAINT api_key_actors CHECK (
    ((key_hash IS NULL AND id = 'legacy_' || merchant_id AND created_at IS NULL
        AND num_nonnulls(created_by, platform_created_by) = 0 AND replaces_id IS NULL)
    OR (key_hash IS NOT NULL AND created_at IS NOT NULL AND num_nonnulls(created_by, platform_created_by) = 1))
    AND (revoked_at IS NULL) = (num_nonnulls(revoked_by, platform_revoked_by) = 0)
    AND num_nonnulls(revoked_by, platform_revoked_by) <= 1
    AND (platform_revoked_by IS NULL OR platform_created_by IS NOT NULL)
);
