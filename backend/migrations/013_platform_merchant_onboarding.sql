CREATE UNIQUE INDEX IF NOT EXISTS merchant_invitations_owner_hash_idx
    ON merchant_invitations (merchant_id, token_hash);

CREATE TABLE IF NOT EXISTS sandbox_merchant_onboardings (
    merchant_id text PRIMARY KEY REFERENCES merchants(id),
    request_key text NOT NULL UNIQUE,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    site_id text NOT NULL,
    invite_hash bytea NOT NULL,
    invite_version integer NOT NULL DEFAULT 1 CHECK (invite_version >= 1),
    FOREIGN KEY (merchant_id, invite_hash) REFERENCES merchant_invitations(merchant_id, token_hash),
    FOREIGN KEY (merchant_id, site_id) REFERENCES merchant_sites(merchant_id, id)
);

CREATE TABLE IF NOT EXISTS sandbox_merchant_invitation_reissues (
    merchant_id text NOT NULL REFERENCES sandbox_merchant_onboardings(merchant_id),
    request_key text NOT NULL,
    replaced_version integer NOT NULL CHECK (replaced_version >= 1),
    PRIMARY KEY (merchant_id, request_key)
);
