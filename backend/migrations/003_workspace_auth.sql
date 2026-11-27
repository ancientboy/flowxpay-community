CREATE TABLE IF NOT EXISTS merchant_invitations (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    merchant_id text NOT NULL REFERENCES merchants(id),
    email text NOT NULL,
    name text NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS merchant_members_merchant_id_id_idx ON merchant_members (merchant_id, id);

CREATE TABLE IF NOT EXISTS merchant_credentials (
    member_id text PRIMARY KEY,
    merchant_id text NOT NULL,
    email text NOT NULL,
    password_hash text NOT NULL,
    mfa_secret bytea,
    last_mfa_step bigint NOT NULL DEFAULT -1,
    enrollment_secret bytea,
    enrollment_expires_at timestamptz,
    UNIQUE (merchant_id, email),
    FOREIGN KEY (merchant_id, member_id) REFERENCES merchant_members (merchant_id, id)
);

CREATE TABLE IF NOT EXISTS merchant_login_challenges (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    member_id text NOT NULL REFERENCES merchant_credentials(member_id),
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    consumed_at timestamptz
);

ALTER TABLE merchant_sessions ADD COLUMN IF NOT EXISTS mfa_verified boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS merchant_auth_limits (
    bucket bytea PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL CHECK (attempts > 0)
);

CREATE INDEX IF NOT EXISTS merchant_auth_limits_expiration_idx ON merchant_auth_limits (expires_at);
