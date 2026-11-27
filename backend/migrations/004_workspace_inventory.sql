ALTER TABLE merchant_sites ADD COLUMN IF NOT EXISTS request_key text;
ALTER TABLE sandbox_channel_accounts ADD COLUMN IF NOT EXISTS request_key text;

CREATE UNIQUE INDEX IF NOT EXISTS merchant_sites_request_key
    ON merchant_sites (merchant_id, request_key);
CREATE UNIQUE INDEX IF NOT EXISTS sandbox_channel_accounts_request_key
    ON sandbox_channel_accounts (merchant_id, request_key);
