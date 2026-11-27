CREATE TABLE IF NOT EXISTS platform_upstream_integrations (
    id text PRIMARY KEY,
    name text NOT NULL,
    market text NOT NULL CHECK (market ~ '^[A-Z]{2}$'),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    collections boolean NOT NULL,
    payouts boolean NOT NULL,
    validation_status text NOT NULL CHECK (validation_status IN ('pending_verification', 'signature_rejected')),
    CHECK (collections OR payouts)
);

-- Onboarding records are intentionally separate from routable sandbox accounts.
INSERT INTO platform_upstream_integrations (id, name, market, currency, collections, payouts, validation_status)
VALUES ('thai-upstream', 'Thailand upstream', 'TH', 'THB', true, true, 'signature_rejected')
ON CONFLICT (id) DO NOTHING;
