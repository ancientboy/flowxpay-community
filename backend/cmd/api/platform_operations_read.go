package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type platformAuditEntry struct {
	ID          int64           `json:"id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Actor       string          `json:"actor"`
	Action      string          `json:"action"`
	Merchant    *string         `json:"merchant_id"`
	Resource    string          `json:"resource_id"`
	Details     json.RawMessage `json:"details"`
	ChannelID   *string         `json:"platform_channel_id,omitempty"`
	ChannelName *string         `json:"platform_channel_name,omitempty"`
	Provider    *string         `json:"provider,omitempty"`
	AccountID   *string         `json:"account_id,omitempty"`
}

type upstreamIntegration struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Market           string `json:"market"`
	Currency         string `json:"currency"`
	Collections      bool   `json:"collections"`
	Payouts          bool   `json:"payouts"`
	ValidationStatus string `json:"validation_status"`
}

func readPlatformOperations(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, _, merchant string) (int, any, error) {
	ctx := r.Context()
	channels, err := readPlatformRows(ctx, tx,
		`SELECT id,name,provider,enabled FROM sandbox_platform_channels ORDER BY name,id`,
		func(rows *sql.Rows, v *platformChannelConfig) error {
			var err error
			*v, err = scanPlatformChannel(rows)
			return err
		})
	if err != nil {
		return 0, nil, err
	}
	audit, err := readPlatformRows(ctx, tx,
		`SELECT e.id,e.occurred_at,e.actor,e.action,e.merchant_id,e.resource_id,e.details,
		  c.id,c.name,COALESCE(c.provider,a.provider),a.id
		 FROM sandbox_platform_audit e
		 LEFT JOIN sandbox_channel_accounts a ON a.merchant_id=e.merchant_id AND a.id=CASE
		  WHEN e.action='channel.assign' THEN e.resource_id
		  WHEN e.action IN ('route.submit','route.approve','route.cancel','route.switch') THEN e.details->>'account_id' END
		 LEFT JOIN sandbox_platform_channels c ON c.id=CASE
		  WHEN e.action IN ('channel.create','channel.status','price.publish') THEN e.resource_id ELSE a.platform_channel_id END
		 WHERE ($1='' OR e.merchant_id=$1) ORDER BY e.id DESC LIMIT 100`,
		func(rows *sql.Rows, v *platformAuditEntry) error {
			return rows.Scan(&v.ID, &v.OccurredAt, &v.Actor, &v.Action, &v.Merchant, &v.Resource, &v.Details, &v.ChannelID, &v.ChannelName, &v.Provider, &v.AccountID)
		}, merchant)
	if err != nil {
		return 0, nil, err
	}
	form := map[string]any{"channels": channels, "audit": audit}
	if merchant == "" {
		integrations, err := readPlatformRows(ctx, tx, `
			SELECT id,name,market,currency,collections,payouts,validation_status
			FROM platform_upstream_integrations ORDER BY market,name,id`,
			func(rows *sql.Rows, v *upstreamIntegration) error {
				return rows.Scan(&v.ID, &v.Name, &v.Market, &v.Currency, &v.Collections, &v.Payouts, &v.ValidationStatus)
			})
		if err != nil {
			return 0, nil, err
		}
		form["upstream_integrations"] = integrations
		return http.StatusOK, form, nil
	}
	onboarding, err := readOnboarding(tx.QueryRowContext(ctx, onboardingSelect+" WHERE o.merchant_id=$1", merchant))
	if err == nil {
		form["onboarding"] = onboarding
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	_, form["accounts"], err = listScopedRows(r, tx, merchant, channelAccountSelect+`
 WHERE a.merchant_id=$1 ORDER BY a.site_id,a.provider,a.id`, scanChannelAccount)
	if err != nil {
		return 0, nil, err
	}
	_, form["routes"], err = listScopedRows(r, tx, merchant, "SELECT "+routeChangeColumns+`
 FROM collection_route_changes WHERE merchant_id=$1 ORDER BY submitted_at DESC,id`, scanRouteChange)
	if err != nil {
		return 0, nil, err
	}
	effective, err := readEffectiveCollectionRoutes(ctx, tx, merchant)
	if err != nil {
		return 0, nil, err
	}
	form["effective_routes"] = effective
	form["merchant_channel_quotes"], err = readMerchantChannelQuotes(ctx, tx, merchant, effective)
	if err != nil {
		return 0, nil, err
	}
	form["sites"], err = readSiteCards(ctx, tx, merchant, effective)
	if err != nil {
		return 0, nil, err
	}
	_, form["keys"], err = listScopedRows(r, tx, merchant, "SELECT "+apiKeyColumns+`
 FROM merchant_api_keys WHERE merchant_id=$1 ORDER BY revoked_at IS NOT NULL,created_at DESC NULLS LAST,id`, scanAPIKey)
	return http.StatusOK, form, err
}
