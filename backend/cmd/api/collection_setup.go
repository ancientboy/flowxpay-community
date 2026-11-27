package main

import (
	"context"
	"database/sql"
)

// An effective route is the account new orders will try to use, including
// the legacy fallback at sites without an approved change.
type effectiveCollectionRoute struct {
	SiteID    string `json:"site_id"`
	AccountID string `json:"account_id"`
	capability
}

func readEffectiveCollectionRoutes(ctx context.Context, tx *sql.Tx, merchant string) ([]effectiveCollectionRoute, error) {
	var markets, currencies, methods, providers []string
	for _, profile := range collectionProfiles {
		markets = append(markets, profile.capability.Market)
		currencies = append(currencies, profile.capability.Currency)
		methods = append(methods, profile.capability.Method)
		providers = append(providers, profile.provider)
	}
	return readPlatformRows(ctx, tx, `
		WITH profiles AS (
			SELECT market, currency, method, array_agg(provider ORDER BY priority) AS providers
			FROM unnest($2::text[], $3::text[], $4::text[], $5::text[]) WITH ORDINALITY
				AS p(market,currency,method,provider,priority)
			GROUP BY market,currency,method
		)
		SELECT s.id, target.id, p.market, p.currency, p.method
		FROM merchant_sites s CROSS JOIN profiles p
		LEFT JOIN collection_route_changes r ON r.merchant_id=s.merchant_id AND r.site_id=s.id
			AND (r.market,r.currency,r.method)=(p.market,p.currency,p.method) AND `+currentCollectionRoute+`
		CROSS JOIN LATERAL (
			SELECT id, platform_channel_id FROM sandbox_channel_accounts
			WHERE merchant_id=s.merchant_id AND site_id=s.id
			AND `+collectionAccountFilter("p.providers", "COALESCE(r.account_id,'')")+`
			ORDER BY array_position(p.providers,provider), id LIMIT 1
		) target
		LEFT JOIN sandbox_platform_channels c ON c.id=target.platform_channel_id
		WHERE ($1='' OR s.merchant_id=$1) AND COALESCE(c.enabled,true)
		ORDER BY s.is_default DESC, lower(s.name), s.id, p.currency, p.method`,
		func(rows *sql.Rows, route *effectiveCollectionRoute) error {
			return rows.Scan(&route.SiteID, &route.AccountID, &route.Market, &route.Currency, &route.Method)
		}, merchant, markets, currencies, methods, providers)
}
