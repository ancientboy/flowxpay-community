ALTER TABLE sandbox_platform_channels
    ADD COLUMN IF NOT EXISTS creation_request_key text,
    ADD COLUMN IF NOT EXISTS creation_request_hash bytea;

CREATE UNIQUE INDEX IF NOT EXISTS sandbox_platform_channel_creation_key
    ON sandbox_platform_channels (creation_request_key) WHERE creation_request_key IS NOT NULL;
