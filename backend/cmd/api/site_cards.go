package main

import (
	"context"
	"database/sql"
)

type siteProposal struct {
	SiteID string `json:"-"`
	capability
	Available bool `json:"available"`
}

type siteCard struct {
	site
	MerchantID   string                     `json:"merchant_id"`
	MerchantName string                     `json:"merchant_name"`
	Assigned     int                        `json:"assigned_accounts"`
	Available    int                        `json:"available_accounts"`
	Methods      []effectiveCollectionRoute `json:"methods"`
	Proposals    []siteProposal             `json:"proposals"`
}

func readSiteCards(ctx context.Context, tx *sql.Tx, merchant string, effective []effectiveCollectionRoute) ([]siteCard, error) {
	cards, err := readPlatformRows(ctx, tx, `
		SELECT s.id,s.name,s.is_default,s.business_domain,s.merchant_id,m.name,
			a.assigned,a.available
		FROM merchant_sites s JOIN merchants m ON m.id=s.merchant_id
		CROSS JOIN LATERAL (
			SELECT count(*) AS assigned, count(*) FILTER (WHERE a.enabled AND COALESCE(c.enabled,true)) AS available
			FROM sandbox_channel_accounts a LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
			WHERE a.merchant_id=s.merchant_id AND a.site_id=s.id AND a.environment='sandbox'
		) a
		WHERE ($1='' OR s.merchant_id=$1)
		ORDER BY lower(m.name),m.id,s.is_default DESC,lower(s.name),s.id`,
		func(rows *sql.Rows, v *siteCard) error {
			v.Methods = []effectiveCollectionRoute{}
			v.Proposals = []siteProposal{}
			return rows.Scan(&v.ID, &v.Name, &v.Default, &v.BusinessDomain, &v.MerchantID, &v.MerchantName, &v.Assigned, &v.Available)
		}, merchant)
	if err != nil || len(cards) == 0 {
		return cards, err
	}
	proposals, err := readPlatformRows(ctx, tx, `
		SELECT r.site_id,r.market,r.currency,r.method,
			a.environment='sandbox' AND a.enabled AND COALESCE(c.enabled,true)
		FROM collection_route_changes r
		JOIN sandbox_channel_accounts a ON (a.id,a.merchant_id,a.site_id)=(r.account_id,r.merchant_id,r.site_id)
		LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
		WHERE ($1='' OR r.merchant_id=$1) AND r.approved_at IS NULL AND r.cancelled_at IS NULL
		ORDER BY r.currency,r.method,r.id`, func(rows *sql.Rows, v *siteProposal) error {
		return rows.Scan(&v.SiteID, &v.Market, &v.Currency, &v.Method, &v.Available)
	}, merchant)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*siteCard, len(cards))
	for i := range cards {
		byID[cards[i].ID] = &cards[i]
	}
	for _, route := range effective {
		if card := byID[route.SiteID]; card != nil {
			card.Methods = append(card.Methods, route)
		}
	}
	for _, proposal := range proposals {
		if card := byID[proposal.SiteID]; card != nil {
			card.Proposals = append(card.Proposals, proposal)
		}
	}
	return cards, nil
}
