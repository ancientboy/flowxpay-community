CREATE UNIQUE INDEX IF NOT EXISTS merchant_member_owner
    ON merchant_members (merchant_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS sandbox_channel_account_owner
    ON sandbox_channel_accounts (merchant_id, site_id, id);

CREATE TABLE IF NOT EXISTS merchant_api_keys (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 100),
    key_hash bytea UNIQUE CHECK (octet_length(key_hash) = 32),
    created_at timestamptz,
    created_by text,
    replaces_id text,
    revoked_at timestamptz,
    revoked_by text,
    UNIQUE (merchant_id, id),
    UNIQUE (merchant_id, replaces_id),
    FOREIGN KEY (merchant_id, created_by) REFERENCES merchant_members (merchant_id, id),
    FOREIGN KEY (merchant_id, revoked_by) REFERENCES merchant_members (merchant_id, id),
    FOREIGN KEY (merchant_id, replaces_id) REFERENCES merchant_api_keys (merchant_id, id),
    CHECK ((key_hash IS NULL AND id = 'legacy_' || merchant_id AND created_at IS NULL AND created_by IS NULL AND replaces_id IS NULL)
        OR (key_hash IS NOT NULL AND created_at IS NOT NULL AND created_by IS NOT NULL)),
    CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS merchant_api_key_active_label
    ON merchant_api_keys (merchant_id, lower(label)) WHERE revoked_at IS NULL;

INSERT INTO merchant_api_keys (id, merchant_id, label)
    SELECT 'legacy_' || id, id, 'Legacy sandbox key' FROM merchants
    WHERE id IN ('M10028', 'M20029') ON CONFLICT (id) DO NOTHING;

ALTER TABLE sandbox_channel_accounts
    ADD COLUMN IF NOT EXISTS routing_standby boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS collection_route_changes (
    id text PRIMARY KEY,
    merchant_id text NOT NULL,
    site_id text NOT NULL,
    account_id text NOT NULL,
    market text NOT NULL,
    currency text NOT NULL,
    method text NOT NULL,
    submitted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    submitted_by text NOT NULL,
    approved_at timestamptz,
    approved_by text,
    superseded_at timestamptz,
    FOREIGN KEY (merchant_id, site_id, account_id) REFERENCES sandbox_channel_accounts (merchant_id, site_id, id),
    FOREIGN KEY (merchant_id, submitted_by) REFERENCES merchant_members (merchant_id, id),
    FOREIGN KEY (merchant_id, approved_by) REFERENCES merchant_members (merchant_id, id),
    CHECK ((approved_at IS NULL) = (approved_by IS NULL)),
    CHECK (submitted_by <> approved_by),
    CHECK (superseded_at IS NULL OR approved_at IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS collection_one_active_route_change
    ON collection_route_changes (merchant_id, site_id, market, currency, method)
    WHERE approved_at IS NOT NULL AND superseded_at IS NULL;
