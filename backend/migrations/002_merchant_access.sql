CREATE TABLE IF NOT EXISTS merchant_members (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    name text NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    disabled_at timestamptz
);

CREATE TABLE IF NOT EXISTS merchant_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    member_id text NOT NULL REFERENCES merchant_members(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);

CREATE TABLE IF NOT EXISTS merchant_sites (
    id text PRIMARY KEY,
    merchant_id text NOT NULL REFERENCES merchants(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    UNIQUE (merchant_id, id)
);

CREATE TABLE IF NOT EXISTS sandbox_channel_accounts (
    id text PRIMARY KEY,
    merchant_id text NOT NULL,
    site_id text NOT NULL,
    environment text NOT NULL DEFAULT 'sandbox' CHECK (environment = 'sandbox'),
    provider text NOT NULL CHECK (provider IN ('mock_pix', 'mock_card')),
    CONSTRAINT sandbox_account_site_owner
        FOREIGN KEY (merchant_id, site_id) REFERENCES merchant_sites(merchant_id, id)
);
