package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

type merchantChannelQuote struct {
	AccountID string      `json:"account_id"`
	SiteID    string      `json:"site_id"`
	ChannelID string      `json:"channel_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Provider  string      `json:"provider"`
	Currency  string      `json:"currency"`
	Direction string      `json:"direction"`
	Quote     *feeVersion `json:"quote"`
}

func listMerchantChannelQuotes(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	ctx := r.Context()
	routes, err := readEffectiveCollectionRoutes(ctx, tx, m.MerchantID)
	if err != nil {
		return 0, nil, err
	}
	quotes, err := readMerchantChannelQuotes(ctx, tx, m.MerchantID, routes)
	return http.StatusOK, quotes, err
}

func readMerchantChannelQuotes(ctx context.Context, tx *sql.Tx, merchant string, routes []effectiveCollectionRoute) ([]merchantChannelQuote, error) {
	collection := make([]string, 0, len(routes))
	for _, route := range routes {
		collection = append(collection, route.AccountID)
	}
	quotes, err := readPlatformRows(ctx, tx, `
		WITH eligible AS (
			SELECT id AS account_id, 'collection' AS direction, '' AS currency
			FROM sandbox_channel_accounts WHERE merchant_id=$1 AND id=ANY($2::text[])
			UNION ALL SELECT account_id,'payout',currency FROM sandbox_payout_accounts WHERE merchant_id=$1
		)
		SELECT a.id,a.site_id,COALESCE(c.id,''),COALESCE(c.name,''),a.provider,e.currency,e.direction
		FROM eligible e JOIN sandbox_channel_accounts a ON a.merchant_id=$1 AND a.id=e.account_id
		LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
		WHERE a.enabled AND COALESCE(c.enabled,true) ORDER BY a.site_id,e.direction,a.id`,
		func(rows *sql.Rows, q *merchantChannelQuote) error {
			return rows.Scan(&q.AccountID, &q.SiteID, &q.ChannelID, &q.Name, &q.Provider, &q.Currency, &q.Direction)
		}, merchant, collection)
	if err != nil {
		return nil, err
	}
	prices, err := readChannelPrices(ctx, tx, merchant, "", "", "", false)
	if err != nil {
		return nil, err
	}
	for i := range quotes {
		q := &quotes[i]
		if profile, ok := profileFor(q.Provider); q.Currency == "" && ok {
			q.Currency = profile.capability.Currency
		}
		for _, price := range prices {
			if price.ChannelID == q.ChannelID && price.Direction == q.Direction && price.Currency == q.Currency {
				q.Quote = price.quote()
				break
			}
		}
	}
	return quotes, nil
}

type platformOrderFee struct {
	ID               string    `json:"id"`
	Status           string    `json:"status"`
	SiteID           string    `json:"site_id"`
	AccountID        string    `json:"account_id"`
	Direction        string    `json:"direction"`
	Currency         string    `json:"currency"`
	AmountMinor      int64     `json:"amount_minor"`
	CreatedAt        string    `json:"created_at"`
	ChannelID        string    `json:"channel_id,omitempty"`
	Quote            *orderFee `json:"quote"`
	Cost             *orderFee `json:"cost,omitempty"`
	EstimatedMargin  *int64    `json:"estimated_margin_minor,omitempty"`
	StatementFee     *int64    `json:"statement_fee_minor"`
	StatementNet     *int64    `json:"statement_net_minor"`
	StatementBatchID string    `json:"statement_batch_id,omitempty"`
	StatementStatus  string    `json:"statement_status"`
}

func listPlatformOrderFees(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _, merchant string) (int, any, error) {
	ctx := r.Context()
	site, reference := r.URL.Query().Get("site_id"), r.URL.Query().Get("reference")
	if (site != "" && !clientID.MatchString(site)) || (reference != "" && !clientID.MatchString(reference)) {
		fail(w, 400, "invalid_order_fee_filter", "Supply a valid site or order reference.")
		return 0, nil, nil
	}
	if site != "" {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM merchant_sites WHERE merchant_id=$1 AND id=$2", merchant, site).Scan(&id); err != nil {
			return 0, nil, err
		}
	}
	operator := r.Context().Value(platformRoleKey{}) == "operator"
	rows, err := readPlatformRows(ctx, tx, `
		WITH orders AS (
			SELECT id,merchant_reference,status,currency,amount_minor,created_at,channel_reference,
				COALESCE(route_snapshot->>'account_id','') AS account_id,COALESCE(route_snapshot->>'site_id','') AS site_id,
				'collection' AS direction,pricing_snapshot
			FROM payments WHERE merchant_id=$1
			UNION ALL
			SELECT p.id,p.merchant_reference,p.status,p.currency,p.amount_minor,p.created_at,p.channel_reference,p.account_id,a.site_id,
				'payout',p.pricing_snapshot
			FROM sandbox_payouts p JOIN sandbox_channel_accounts a ON (a.merchant_id,a.id)=(p.merchant_id,p.account_id)
			WHERE p.merchant_id=$1
		), scoped AS (
			SELECT * FROM orders WHERE ($2='' OR site_id=$2) AND ($3='' OR id=$3 OR merchant_reference=$3)
			ORDER BY created_at DESC,id DESC LIMIT 100
		)
		SELECT o.id,o.status,o.site_id,o.account_id,o.direction,o.currency,o.amount_minor,
			to_char(o.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),o.pricing_snapshot,
			COALESCE(e.batch_id,''),e.fee_minor,e.net_minor,
			CASE WHEN e.id IS NULL THEN 'missing'
				WHEN rev.id IS NOT NULL THEN 'reversal_reported'
				WHEN o.status<>'succeeded' OR e.match_status<>'matched' OR e.order_id IS DISTINCT FROM o.id
					OR e.gross_minor<>CASE WHEN o.direction='payout' THEN -o.amount_minor ELSE o.amount_minor END
					OR e.gross_minor-e.fee_minor<>e.net_minor THEN 'unmatched'
				WHEN b.settlement_status<>'settled' THEN 'pending'
				ELSE 'matched_settled' END
		FROM scoped o
		LEFT JOIN sandbox_settlement_entries e ON e.merchant_id=$1 AND e.account_id=o.account_id AND e.currency=o.currency
			AND e.kind=o.direction AND e.channel_reference=o.channel_reference AND e.reverses_entry IS NULL
		LEFT JOIN sandbox_settlement_batches b ON (b.merchant_id,b.account_id,b.currency,b.id)=(e.merchant_id,e.account_id,e.currency,e.batch_id)
		LEFT JOIN sandbox_settlement_entries rev ON (rev.merchant_id,rev.account_id,rev.currency,rev.reverses_entry)=(e.merchant_id,e.account_id,e.currency,e.id)
		ORDER BY o.created_at DESC,o.id DESC`,
		func(row *sql.Rows, v *platformOrderFee) error {
			var snapshot []byte
			var fee, net *int64
			if err := row.Scan(&v.ID, &v.Status, &v.SiteID, &v.AccountID, &v.Direction, &v.Currency, &v.AmountMinor, &v.CreatedAt, &snapshot,
				&v.StatementBatchID, &fee, &net, &v.StatementStatus); err != nil {
				return err
			}
			if v.StatementStatus == "matched_settled" {
				v.StatementFee, v.StatementNet = fee, net
			}
			if snapshot == nil {
				return nil
			}
			var pricing orderPricing
			if err := json.Unmarshal(snapshot, &pricing); err != nil {
				return err
			}
			v.Quote, v.ChannelID = pricing.Quote, pricing.ChannelID
			if operator {
				v.Cost = pricing.Cost
				if v.Status == "succeeded" && v.Cost != nil && v.Quote != nil {
					margin := v.Quote.FeeMinor - v.Cost.FeeMinor
					v.EstimatedMargin = &margin
				}
			}
			return nil
		}, merchant, site, reference)
	return http.StatusOK, rows, err
}
