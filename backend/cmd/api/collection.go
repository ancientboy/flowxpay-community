package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const capabilityVersion = "sandbox-collections-v1"

type capability struct {
	Market   string `json:"market"`
	Currency string `json:"currency"`
	Method   string `json:"method"`
}

type collectionProfile struct {
	provider   string
	capability capability
	action     string
}

// The order defines precedence when one site has multiple profiles for a method.
var collectionProfiles = []collectionProfile{
	{"mock_pix", capability{"BR", "BRL", "pix"}, "display_qr"},
	{"mock_card", capability{"US", "USD", "card"}, "redirect"},
	{"mock_in_redirect", capability{"IN", "INR", "gateway"}, "redirect"},
	{"mock_in_qr", capability{"IN", "INR", "gateway"}, "display_qr"},
	{"mock_in_unknown", capability{"IN", "INR", "gateway"}, "unsupported"},
}

func profileFor(provider string) (collectionProfile, bool) {
	for _, profile := range collectionProfiles {
		if profile.provider == provider {
			return profile, true
		}
	}
	return collectionProfile{}, false
}

type collectionRoute struct {
	Version     string     `json:"capability_version"`
	SiteID      string     `json:"site_id"`
	AccountID   string     `json:"account_id"`
	ChannelID   string     `json:"platform_channel_id,omitempty"`
	ChangeID    string     `json:"route_change_id,omitempty"`
	Provider    string     `json:"provider"`
	Environment string     `json:"environment"`
	LimitsRPS   int        `json:"requests_per_second"`
	Capability  capability `json:"capability"`
}

type accountCapability struct {
	standby    bool
	routed     bool
	AccountID  string `json:"account_id"`
	Provider   string `json:"provider"`
	Enabled    bool   `json:"enabled"`
	LimitsRPS  int    `json:"requests_per_second"`
	NextAction string `json:"next_action_type"`
	capability
}

type siteCapability struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Default  bool                `json:"default"`
	Accounts []accountCapability `json:"accounts"`
	Payments []capability        `json:"payments"`
}

func (s *server) capabilities(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT site.id, site.name, site.is_default, a.id, a.provider, a.enabled AND COALESCE(c.enabled,true), a.requests_per_second, a.routing_standby, r.id
		FROM merchant_sites site LEFT JOIN sandbox_channel_accounts a
			ON a.merchant_id=site.merchant_id AND a.site_id=site.id AND a.environment='sandbox'
		LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
		LEFT JOIN collection_route_changes r ON r.merchant_id=a.merchant_id AND r.site_id=a.site_id
			AND r.account_id=a.id AND r.approved_at IS NOT NULL AND r.superseded_at IS NULL
		WHERE site.merchant_id=$1 ORDER BY site.is_default DESC, site.name, site.id, a.id`, merchant.ID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to read payment capabilities.")
		return
	}
	defer rows.Close()
	sites := make([]siteCapability, 0)
	options := make([]capability, 0)
	for rows.Next() {
		var site siteCapability
		var id, provider, change sql.NullString
		var enabled, standby sql.NullBool
		var limit sql.NullInt64
		if err := rows.Scan(&site.ID, &site.Name, &site.Default, &id, &provider, &enabled, &limit, &standby, &change); err != nil {
			fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to read payment capabilities.")
			return
		}
		if len(sites) == 0 || sites[len(sites)-1].ID != site.ID {
			site.Accounts = []accountCapability{}
			site.Payments = []capability{}
			sites = append(sites, site)
		}
		if profile, ok := profileFor(provider.String); ok && id.Valid {
			current := &sites[len(sites)-1]
			current.Accounts = append(current.Accounts, accountCapability{
				AccountID: id.String, Provider: provider.String, Enabled: enabled.Bool,
				standby: standby.Bool, routed: change.Valid,
				LimitsRPS: int(limit.Int64), NextAction: profile.action, capability: profile.capability,
			})
		}
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to read payment capabilities.")
		return
	}
	for i := range sites {
		site := &sites[i]
		configured := make(map[capability]bool)
		for _, account := range site.Accounts {
			if account.routed {
				configured[account.capability] = true
			}
		}
		for _, account := range site.Accounts {
			if account.Enabled && (account.routed || (!account.standby && !configured[account.capability])) {
				site.Payments = appendCapability(site.Payments, account.capability)
			}
		}
	}
	if len(sites) > 0 && sites[0].Default {
		options = sites[0].Payments
	}
	respond(w, http.StatusOK, map[string]any{
		"environment": "sandbox", "capability_version": capabilityVersion,
		"merchant": map[string]string{"id": merchant.ID, "name": merchant.Name},
		"payments": options, "sites": sites,
		"request_signing_required": len(s.requestKeys[merchant.ID]) != 0,
	})
}

func appendCapability(options []capability, value capability) []capability {
	for _, existing := range options {
		if value == existing {
			return options
		}
	}
	return append(options, value)
}

var (
	errSiteUnavailable    = errors.New("site unavailable")
	errMethodUnavailable  = errors.New("payment method unavailable for site")
	errChannelRejected    = errors.New("channel rejected payment")
	errChannelRateLimited = errors.New("channel rate limited")
)

// Keep the configured route authoritative, including when its account is disabled.
const currentCollectionRoute = `approved_at IS NOT NULL AND superseded_at IS NULL`
const collectionAccountEligibility = `environment='sandbox' AND enabled
    AND provider=ANY(%s) AND ((%s='' AND NOT routing_standby) OR id=%s)`

func collectionAccountFilter(providers, account string) string {
	return fmt.Sprintf(collectionAccountEligibility, providers, account, account)
}

func selectCollectionRoute(ctx context.Context, tx *sql.Tx, merchantID string, request paymentRequest) (collectionRoute, error) {
	route := collectionRoute{
		Version: capabilityVersion, Environment: "sandbox",
		Capability: capability{request.Market, request.Currency, request.Method},
	}
	providers := collectionProviders(route.Capability)
	if len(providers) == 0 {
		return route, errMethodUnavailable
	}
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM merchant_sites WHERE merchant_id=$1
		AND (($2='' AND is_default) OR id=$2) FOR SHARE`, merchantID, request.SiteID).Scan(&route.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return route, errSiteUnavailable
	}
	if err != nil {
		return route, err
	}
	// The site lock orders approval commits and payment reservations across API instances.
	var accountID string
	err = tx.QueryRowContext(ctx, `
		SELECT id, account_id FROM collection_route_changes
		WHERE merchant_id=$1 AND site_id=$2 AND market=$3 AND currency=$4 AND method=$5
		  AND `+currentCollectionRoute,
		merchantID, route.SiteID, request.Market, request.Currency, request.Method).Scan(&route.ChangeID, &accountID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return route, err
	}
	var channel sql.NullString
	err = tx.QueryRowContext(ctx, `
  SELECT id, provider, requests_per_second, platform_channel_id FROM sandbox_channel_accounts
  WHERE merchant_id=$1 AND site_id=$2 AND `+collectionAccountFilter("$3::text[]", "$4")+`
  ORDER BY array_position($3::text[],provider), id LIMIT 1 FOR SHARE`,
		merchantID, route.SiteID, providers, accountID).Scan(&route.AccountID, &route.Provider, &route.LimitsRPS, &channel)
	if err == nil {
		err = lockEnabledPlatformChannel(ctx, tx, channel)
		route.ChannelID = channel.String
	}
	if errors.Is(err, sql.ErrNoRows) {
		return route, errMethodUnavailable
	}
	return route, err
}

func collectionProviders(value capability) []string {
	providers := make([]string, 0, len(collectionProfiles))
	for _, profile := range collectionProfiles {
		if profile.capability == value {
			providers = append(providers, profile.provider)
		}
	}
	return providers
}

// Acquire a shared per-account budget after claiming the idempotency key.
func takeCollectionSlot(ctx context.Context, tx *sql.Tx, route collectionRoute) error {
	var attempts int
	err := tx.QueryRowContext(ctx, `
		INSERT INTO sandbox_channel_rate AS rate (account_id, window_start, attempts)
		VALUES ($1, date_trunc('second', clock_timestamp()), 1)
		ON CONFLICT (account_id) DO UPDATE SET
			window_start=EXCLUDED.window_start,
			attempts=CASE WHEN rate.window_start < EXCLUDED.window_start THEN 1 ELSE rate.attempts+1 END
		WHERE rate.window_start < EXCLUDED.window_start
		   OR (rate.window_start = EXCLUDED.window_start AND rate.attempts < $2)
		RETURNING attempts`, route.AccountID, route.LimitsRPS).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return errChannelRateLimited
	}
	return err
}

type collectionInput struct {
	PaymentID string // Stable channel idempotency reference.
	Request   paymentRequest
	Route     collectionRoute
}

type collectionResult struct {
	Reference string
	Action    string
}

// Create returns a known rejection only via the typed errors above. Any other
// error leaves the outcome unknown. The route and ID are available for later reconciliation.
type collectionAdapter interface {
	Create(context.Context, collectionInput) (collectionResult, error)
}

type sandboxCollection struct{ db *sql.DB }

func (mock sandboxCollection) Create(ctx context.Context, input collectionInput) (collectionResult, error) {
	if err := ctx.Err(); err != nil {
		return collectionResult{}, err
	}
	profile, ok := profileFor(input.Route.Provider)
	if !ok || input.Route.Environment != "sandbox" || input.Route.Version != capabilityVersion ||
		input.Route.Capability != profile.capability {
		return collectionResult{}, errChannelRejected
	}
	result := collectionResult{Reference: "sim_" + input.PaymentID, Action: profile.action}
	if mock.db != nil {
		err := storeMockReceipt(ctx, mock.db, input.Route.AccountID, input.PaymentID, result.Reference, result.Action)
		return result, err
	}
	return result, nil
}

func (s *server) dispatchCollection(ctx context.Context, p payment, route collectionRoute) (payment, error) {
	adapter := s.collection
	if adapter == nil {
		adapter = sandboxCollection{db: s.db}
	}
	timeout := s.collectionTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx = context.WithoutCancel(ctx)
	attempt, cancel := context.WithTimeout(ctx, timeout)
	result, err := adapter.Create(attempt, collectionInput{PaymentID: p.ID, Request: p.paymentRequest, Route: route})
	if attempt.Err() != nil {
		err = attempt.Err()
	}
	cancel()

	write, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	tx, storeErr := s.db.BeginTx(write, nil)
	if storeErr != nil {
		return p, storeErr
	}
	defer tx.Rollback()
	current, storeErr := readPayment(tx.QueryRowContext(write, "SELECT "+paymentColumns+" FROM payments WHERE id=$1 FOR NO KEY UPDATE", p.ID))
	if storeErr != nil {
		return p, storeErr
	}
	if current.Status == "pending_confirmation" && current.FailureCode.String == "channel_confirmation_pending" {
		current, storeErr = s.applyCollectionResult(write, tx, current, route, result, err)
		if storeErr != nil {
			return p, storeErr
		}
	}
	return current, tx.Commit()
}

func (s *server) applyCollectionResult(ctx context.Context, tx *sql.Tx, p payment, route collectionRoute, result collectionResult, channelErr error) (payment, error) {
	switch {
	case errors.Is(channelErr, errChannelRejected):
		return s.finishPayment(ctx, tx, p, "failed", "", "channel_rejected")
	case errors.Is(channelErr, errChannelRateLimited):
		return s.finishPayment(ctx, tx, p, "failed", "", "channel_rate_limited")
	case channelErr != nil || !clientID.MatchString(result.Reference):
		return p, nil
	}
	status, failure, action := "pending_confirmation", "channel_action_unsupported", "unsupported"
	profile, _ := profileFor(route.Provider)
	if result.Action == profile.action && (result.Action == "redirect" || result.Action == "display_qr") {
		status, failure, action = "requires_action", "", result.Action
	}
	return readPayment(tx.QueryRowContext(ctx, `
		UPDATE payments SET status=$2, failure_code=NULLIF($3,''), channel_action=$4,
			channel_reference=$5, next_check_at=CASE WHEN $2='requires_action' THEN NULL ELSE next_check_at END
		WHERE id=$1 RETURNING `+paymentColumns, p.ID, status, failure, action, result.Reference))
}
