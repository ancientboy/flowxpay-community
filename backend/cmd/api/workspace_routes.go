package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type routeChange struct {
	ID        string `json:"id"`
	SiteID    string `json:"site_id"`
	AccountID string `json:"account_id"`
	capability
	SubmittedAt  time.Time  `json:"submitted_at"`
	SubmittedBy  string     `json:"submitted_by"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	ApprovedBy   *string    `json:"approved_by,omitempty"`
	Platform     bool       `json:"platform"`
	CancelledAt  *time.Time `json:"cancelled_at,omitempty"`
	SupersededAt *time.Time `json:"superseded_at,omitempty"`
}

const routeChangeColumns = `id, site_id, account_id, market, currency, method,
	submitted_at, COALESCE(platform_submitted_by, submitted_by), approved_at, COALESCE(platform_approved_by, approved_by), superseded_at, platform_submitted_by IS NOT NULL, cancelled_at`

func scanRouteChange(row scanner) (routeChange, error) {
	var v routeChange
	err := row.Scan(&v.ID, &v.SiteID, &v.AccountID, &v.Market, &v.Currency, &v.Method,
		&v.SubmittedAt, &v.SubmittedBy, &v.ApprovedAt, &v.ApprovedBy, &v.SupersededAt, &v.Platform, &v.CancelledAt)
	return v, err
}

func listRouteChanges(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, "SELECT "+routeChangeColumns+`
		FROM collection_route_changes WHERE merchant_id=$1 ORDER BY submitted_at DESC, id`, scanRouteChange)
}

func submitPlatformRoute(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	var input struct {
		SiteID    string `json:"site_id"`
		AccountID string `json:"account_id"`
		Activate  bool   `json:"activate"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if !clientID.MatchString(input.SiteID) || !clientID.MatchString(input.AccountID) {
		fail(w, http.StatusBadRequest, "invalid_route", "Supply a site ID and a channel account ID.")
		return 0, nil, nil
	}
	if err := lockRouteSite(r, tx, merchant, input.SiteID); err != nil {
		return 0, nil, err
	}
	provider, err := eligibleRouteProvider(r, tx, merchant, input.SiteID, input.AccountID)
	if err != nil {
		return 0, nil, err
	}
	profile, ok := profileFor(provider)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid_route", "The channel account has no supported routing profile.")
		return 0, nil, nil
	}
	return savePlatformRoute(w, r, tx, actor, merchant, input.SiteID, input.AccountID, profile.capability, input.Activate)
}

func savePlatformRoute(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant, site, account string, profile capability, activate bool) (int, any, error) {
	ctx := r.Context()
	if activate {
		current, err := scanRouteChange(tx.QueryRowContext(ctx, "SELECT "+routeChangeColumns+`
			FROM collection_route_changes WHERE merchant_id=$1 AND site_id=$2 AND account_id=$3
				AND (market,currency,method)=($4,$5,$6) AND `+currentCollectionRoute,
			merchant, site, account, profile.Market, profile.Currency, profile.Method))
		if !errors.Is(err, sql.ErrNoRows) {
			return http.StatusOK, current, err
		}
	}
	value, err := scanRouteChange(tx.QueryRowContext(ctx, `
		INSERT INTO collection_route_changes (id, merchant_id, site_id, account_id, market, currency, method, platform_submitted_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (merchant_id, site_id, market, currency, method) WHERE platform_submitted_by IS NOT NULL AND approved_at IS NULL AND cancelled_at IS NULL DO NOTHING
		RETURNING `+routeChangeColumns, newResourceID("route_"), merchant, site, account,
		profile.Market, profile.Currency, profile.Method, actor))
	if errors.Is(err, sql.ErrNoRows) {
		return routeConflict(w, "route_pending", "This site and method already have a pending route change.")
	}
	if err != nil {
		return 0, nil, err
	}
	if activate {
		value, err = activatePlatformRoute(r, tx, actor, merchant, value)
		return http.StatusCreated, value, err
	}
	return http.StatusCreated, value, platformAudit(r, tx, actor, "route.submit", merchant, value.ID, value)
}

func approvePlatformRoute(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	return resolvePlatformRoute(w, r, tx, actor, merchant, true)
}

func cancelPlatformRoute(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	return resolvePlatformRoute(w, r, tx, actor, merchant, false)
}

func resolvePlatformRoute(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string, approve bool) (int, any, error) {
	ctx := r.Context()
	id := r.PathValue("id")
	var site string
	if err := tx.QueryRowContext(ctx, `
		SELECT site_id FROM collection_route_changes WHERE merchant_id=$1 AND id=$2 AND platform_submitted_by IS NOT NULL`, merchant, id).Scan(&site); err != nil {
		return 0, nil, err
	}
	if err := lockRouteSite(r, tx, merchant, site); err != nil {
		return 0, nil, err
	}
	value, err := scanRouteChange(tx.QueryRowContext(ctx, "SELECT "+routeChangeColumns+`
		FROM collection_route_changes WHERE merchant_id=$1 AND id=$2 FOR UPDATE`, merchant, id))
	switch {
	case err != nil:
		return 0, nil, err
	case value.CancelledAt != nil || value.ApprovedAt != nil:
		return routeConflict(w, "route_not_pending", "This change is no longer pending.")
	}
	if !approve {
		value, err = scanRouteChange(tx.QueryRowContext(ctx, `
   UPDATE collection_route_changes SET cancelled_at=clock_timestamp()
   WHERE merchant_id=$1 AND id=$2 RETURNING `+routeChangeColumns, merchant, id))
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, value, platformAudit(r, tx, actor, "route.cancel", merchant, id, value)
	}
	provider, err := eligibleRouteProvider(r, tx, merchant, site, value.AccountID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	if profile, ok := profileFor(provider); err != nil || !ok || profile.capability != value.capability {
		return routeConflict(w, "route_account_unavailable", "The proposed account is no longer eligible for this route.")
	}
	value, err = activatePlatformRoute(r, tx, actor, merchant, value)
	return http.StatusOK, value, err
}

// The caller holds the site lock and has verified the target account and channel.
func activatePlatformRoute(r *http.Request, tx *sql.Tx, actor, merchant string, value routeChange) (routeChange, error) {
	ctx := r.Context()
	// Share one database timestamp across retirement and activation.
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return routeChange{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collection_route_changes SET superseded_at=$6
		WHERE merchant_id=$1 AND site_id=$2 AND market=$3 AND currency=$4 AND method=$5
		  AND approved_at IS NOT NULL AND superseded_at IS NULL`,
		merchant, value.SiteID, value.Market, value.Currency, value.Method, at); err != nil {
		return routeChange{}, err
	}
	value, err := scanRouteChange(tx.QueryRowContext(ctx, `
		UPDATE collection_route_changes SET approved_at=$3, platform_approved_by=$4
		WHERE merchant_id=$1 AND id=$2 RETURNING `+routeChangeColumns, merchant, value.ID, at, actor))
	if err != nil {
		return routeChange{}, err
	}
	return value, platformAudit(r, tx, actor, "route.approve", merchant, value.ID, value)
}

func eligibleRouteProvider(r *http.Request, tx *sql.Tx, merchant, site, account string) (string, error) {
	ctx := r.Context()
	var provider string
	var channel sql.NullString
	if err := tx.QueryRowContext(ctx, `
  SELECT provider, platform_channel_id FROM sandbox_channel_accounts
  WHERE merchant_id=$1 AND site_id=$2 AND id=$3 AND environment='sandbox' AND enabled FOR SHARE`,
		merchant, site, account).Scan(&provider, &channel); err != nil {
		return "", err
	}
	return provider, lockEnabledPlatformChannel(ctx, tx, channel)
}

// A shared channel lock keeps approval and payment reservation ordered with status updates.
func lockEnabledPlatformChannel(ctx context.Context, tx *sql.Tx, channel sql.NullString) error {
	if !channel.Valid {
		return nil
	}
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM sandbox_platform_channels WHERE id=$1 AND enabled FOR SHARE`, channel.String).Scan(&id)
}

func lockRouteSite(r *http.Request, tx *sql.Tx, merchant, siteID string) error {
	var id string
	return tx.QueryRowContext(r.Context(), `SELECT id FROM merchant_sites
		WHERE merchant_id=$1 AND id=$2 FOR UPDATE`, merchant, siteID).Scan(&id)
}

func routeConflict(w http.ResponseWriter, code, message string) (int, any, error) {
	fail(w, http.StatusConflict, code, message)
	return 0, nil, nil
}
