CREATE TABLE IF NOT EXISTS sandbox_settlement_requests(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),merchant_id text NOT NULL, account_id text NOT NULL,currency text NOT NULL,request_key uuid NOT NULL,
 source_minor bigint NOT NULL CHECK(source_minor>0),requested_currency text NOT NULL,network text NOT NULL,recipient text NOT NULL,
 phase text NOT NULL CHECK(phase IN ('requested','quoted','approved','paying','confirmed','cancelled')),
 rate numeric(24,12),amount numeric(28,8),cancel_reason text,transfer_reference text,paid_on date,created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(merchant_id,request_key),FOREIGN KEY(merchant_id,account_id) REFERENCES sandbox_channel_accounts(merchant_id,id)
);
ALTER TABLE sandbox_settlement_requests ADD COLUMN IF NOT EXISTS payout_pool_reserved boolean NOT NULL DEFAULT true;
CREATE UNIQUE INDEX IF NOT EXISTS sandbox_request_proof ON sandbox_settlement_requests(requested_currency,transfer_reference) WHERE phase='confirmed';
CREATE OR REPLACE VIEW sandbox_request_balances AS
 SELECT a.merchant_id,a.account_id AS id,a.currency,2 AS scale,
 greatest(0,least(pa.available_minor,coalesce(e.net,0)-coalesce(disputed.loss,0)-coalesce(paid.unreported,0)-coalesce(b.used,0)))::bigint AS settle_available_minor,
 coalesce(b.held,0)::bigint AS settlement_held_minor
 FROM (SELECT merchant_id,account_id,currency FROM sandbox_payout_accounts UNION SELECT merchant_id,account_id,currency FROM sandbox_settlement_batches) a LEFT JOIN sandbox_payout_accounts pa USING(merchant_id,account_id,currency) LEFT JOIN LATERAL(
 SELECT sum(CASE WHEN e.kind='collection' AND e.net_minor>0 THEN
 least(e.net_minor,p.amount_minor-coalesce((p.pricing_snapshot->'quote'->>'fee_minor')::bigint,e.fee_minor)) ELSE least(0,e.net_minor) END) AS net FROM sandbox_settlement_entries e JOIN sandbox_settlement_batches s
 ON (s.merchant_id,s.account_id,s.currency,s.id)=(e.merchant_id,e.account_id,e.currency,e.batch_id)
 LEFT JOIN payments p ON p.merchant_id=e.merchant_id AND p.id=e.order_id
 WHERE (e.merchant_id,e.account_id,e.currency)=(a.merchant_id,a.account_id,a.currency)
 AND s.statement_date<=(now() AT TIME ZONE 'UTC')::date AND s.settlement_status='settled'
 AND ((e.match_status='matched' AND p.status='succeeded' AND p.amount_minor=e.gross_minor AND e.net_minor=e.gross_minor-e.fee_minor AND e.fee_minor>=0) OR e.net_minor<0) AND s.withdrawal_status NOT IN ('completed','pending')
 )e ON true LEFT JOIN LATERAL (SELECT sum(x.amount_minor) AS loss FROM sandbox_chargebacks x WHERE (x.merchant_id,x.account_id,x.currency)=(a.merchant_id,a.account_id,a.currency) AND x.status IN ('opened','lost')) disputed ON true LEFT JOIN LATERAL(
 SELECT sum(o.amount_minor+coalesce((o.pricing_snapshot->'quote'->>'fee_minor')::bigint,0)) AS unreported
 FROM sandbox_payouts o WHERE (o.merchant_id,o.account_id,o.currency)=(a.merchant_id,a.account_id,a.currency)
 AND o.status IN ('processing','pending_confirmation','succeeded') AND NOT EXISTS(
 SELECT FROM sandbox_settlement_entries z JOIN sandbox_settlement_batches b USING(merchant_id,account_id,currency)
 WHERE z.batch_id=b.id AND z.kind='payout' AND z.match_status='matched' AND z.order_id=o.id
 AND (z.merchant_id,z.account_id,z.currency)=(a.merchant_id,a.account_id,a.currency) AND b.settlement_status='settled'
 AND b.statement_date<=(now() AT TIME ZONE 'UTC')::date AND b.withdrawal_status NOT IN ('completed','pending')))
 paid ON true LEFT JOIN LATERAL(SELECT sum(source_minor) FILTER(WHERE phase<>'cancelled') AS used,
 sum(source_minor) FILTER(WHERE phase NOT IN ('confirmed','cancelled')) AS held FROM sandbox_settlement_requests b
 WHERE (b.merchant_id,b.account_id,b.currency)=(a.merchant_id,a.account_id,a.currency)) b ON true;
