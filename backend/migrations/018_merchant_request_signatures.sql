CREATE TABLE IF NOT EXISTS merchant_request_nonces (
    merchant_id text NOT NULL REFERENCES merchants(id),
    nonce text NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (merchant_id, nonce)
);

CREATE INDEX IF NOT EXISTS merchant_request_nonces_expiry ON merchant_request_nonces (expires_at);
