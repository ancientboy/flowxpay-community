package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"flowxpay.local/platform/internal/sitedomain"
)

type site struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Default        bool    `json:"is_default"`
	BusinessDomain *string `json:"business_domain"`
}

const siteColumns = "id, name, is_default, business_domain"

type channelAccount struct {
	ID          string  `json:"id"`
	SiteID      string  `json:"site_id"`
	Provider    string  `json:"provider"`
	Environment string  `json:"environment"`
	Enabled     bool    `json:"enabled"`
	ChannelID   *string `json:"platform_channel_id,omitempty"`
	ChannelName *string `json:"platform_channel_name,omitempty"`
	Standby     bool    `json:"routing_standby"`
}

func inventoryRequestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", true
	}
	if len(values) == 1 && clientID.MatchString(values[0]) {
		return values[0], true
	}
	fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Supply one valid Idempotency-Key.")
	return "", false
}

func inventoryKeyConflict(w http.ResponseWriter) (int, any, error) {
	fail(w, http.StatusConflict, "idempotency_conflict", "This request key was already used for different details.")
	return 0, nil, nil
}

func createSite(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	key, ok := inventoryRequestKey(w, r)
	if !ok {
		return 0, nil, nil
	}
	var request struct {
		Name           string `json:"name"`
		BusinessDomain string `json:"business_domain"`
	}
	if !parseJSON(w, r, &request) {
		return 0, nil, nil
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsRune(name, 0) {
		fail(w, http.StatusBadRequest, "invalid_name", "Supply a site name of 1 to 100 characters.")
		return 0, nil, nil
	}
	domain := strings.ToLower(strings.TrimSpace(request.BusinessDomain))
	if domain != "" && !sitedomain.Valid(domain) {
		fail(w, http.StatusBadRequest, "invalid_business_domain", "Enter a domain such as shop.example.com.")
		return 0, nil, nil
	}
	ctx := r.Context()
	value, err := scanSite(tx.QueryRowContext(ctx, `
		INSERT INTO merchant_sites (id, merchant_id, name, request_key, business_domain) VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))
		ON CONFLICT (merchant_id, request_key) DO NOTHING RETURNING `+siteColumns,
		newResourceID("site_"), m.MerchantID, name, key, domain))
	if key == "" || !errors.Is(err, sql.ErrNoRows) {
		return http.StatusCreated, value, err
	}
	// A separate statement sees the row committed by a concurrent winner.
	value, err = scanSite(tx.QueryRowContext(ctx, `
		SELECT `+siteColumns+` FROM merchant_sites WHERE merchant_id = $1 AND request_key = $2`, m.MerchantID, key))
	if err == nil && (value.Name != name || siteDomain(value.BusinessDomain) != domain) {
		return inventoryKeyConflict(w)
	}
	return http.StatusOK, value, err
}

func siteDomain(domain *string) string {
	if domain != nil {
		return *domain
	}
	return ""
}

func getSite(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	value, err := scanSite(tx.QueryRowContext(r.Context(), `
		SELECT `+siteColumns+` FROM merchant_sites WHERE merchant_id = $1 AND id = $2`,
		m.MerchantID, r.PathValue("id")))
	return http.StatusOK, value, err
}

func scanSite(row scanner) (site, error) {
	var value site
	err := row.Scan(&value.ID, &value.Name, &value.Default, &value.BusinessDomain)
	return value, err
}

func listSites(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, `
		SELECT `+siteColumns+` FROM merchant_sites WHERE merchant_id = $1 ORDER BY lower(name), id`, scanSite)
}

func platformManaged(w http.ResponseWriter, _ *http.Request, _ *sql.Tx, _ member) (int, any, error) {
	fail(w, http.StatusForbidden, "platform_managed", "This action requires a platform operator session.")
	return 0, nil, nil
}

const channelAccountSelect = `SELECT a.id,a.site_id,a.provider,a.environment,
 a.enabled AND COALESCE(c.enabled,true),a.platform_channel_id,
 a.routing_standby AND NOT EXISTS (SELECT 1 FROM collection_route_changes r
  WHERE r.account_id=a.id AND r.approved_at IS NOT NULL AND r.superseded_at IS NULL), c.name
 FROM sandbox_channel_accounts a LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id`

func getChannelAccount(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	account, err := scanChannelAccount(tx.QueryRowContext(r.Context(), channelAccountSelect+`
 WHERE a.merchant_id=$1 AND a.id=$2`, m.MerchantID, r.PathValue("id")))
	return http.StatusOK, account, err
}

func listChannelAccounts(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, channelAccountSelect+`
 WHERE a.merchant_id=$1 ORDER BY a.site_id,a.provider,a.id`, scanChannelAccount)
}

func listScopedRows[T any](r *http.Request, tx *sql.Tx, merchant string, query string, scan func(scanner) (T, error), filters ...any) (int, any, error) {
	rows, err := tx.QueryContext(r.Context(), query, append([]any{merchant}, filters...)...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return 0, nil, err
		}
		items = append(items, item)
	}
	return http.StatusOK, items, rows.Err()
}

func scanChannelAccount(row scanner) (channelAccount, error) {
	var account channelAccount
	err := row.Scan(&account.ID, &account.SiteID, &account.Provider, &account.Environment, &account.Enabled, &account.ChannelID, &account.Standby, &account.ChannelName)
	return account, err
}
