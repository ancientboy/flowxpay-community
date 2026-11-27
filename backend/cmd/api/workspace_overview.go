package main

import (
	"database/sql"
	"net/http"
)

type workspaceOverview struct {
	Orders          int64   `json:"orders"`
	PaidOrders      int64   `json:"paid_orders"`
	GrossMinor      string  `json:"gross_minor"`
	FeesMinor       *string `json:"fees_minor"`
	SettledNetMinor *string `json:"settled_net_minor"`
}

func readWorkspaceOverview(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	currency := r.URL.Query().Get("currency")
	days := r.URL.Query().Get("days")
	if (currency != "USD" && currency != "BRL" && currency != "INR") || (days != "7" && days != "30") {
		fail(w, http.StatusBadRequest, "invalid_overview_filter", "Choose a supported currency and a 7 or 30 day period.")
		return 0, nil, nil
	}
	var summary workspaceOverview
	err := tx.QueryRowContext(r.Context(), `
		WITH period AS (
			SELECT date_trunc('day', CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date AS today
		), collections AS (
			SELECT count(*) AS orders, count(*) FILTER (WHERE status = 'succeeded') AS paid,
				COALESCE(sum(amount_minor) FILTER (WHERE status = 'succeeded'), 0)::text AS gross
			FROM payments CROSS JOIN period
			WHERE merchant_id = $1 AND currency = $2
			  AND created_at >= ((today - ($3::int - 1))::timestamp AT TIME ZONE 'UTC')
			  AND created_at < ((today + 1)::timestamp AT TIME ZONE 'UTC')
		), settlement AS (
			SELECT sum(fee_minor)::text AS fee, sum(net_minor)::text AS net
			FROM sandbox_settlement_batches CROSS JOIN period
			WHERE merchant_id = $1 AND currency = $2 AND settlement_status = 'settled'
			  AND statement_date BETWEEN today - ($3::int - 1) AND today
		)
		SELECT orders, paid, gross, fee, net FROM collections CROSS JOIN settlement`,
		m.MerchantID, currency, days).Scan(&summary.Orders, &summary.PaidOrders, &summary.GrossMinor,
		&summary.FeesMinor, &summary.SettledNetMinor)
	return http.StatusOK, summary, err
}

func (s *server) listWorkspacePayments(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	q := r.URL.Query().Get("reference")
	limit := 100
	if r.URL.Query().Get("recent") == "1" {
		limit = 6
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT "+paymentColumns+`
		FROM payments WHERE merchant_id = $1
		  AND ($2 = '' OR merchant_reference = $2 OR id = $2)
		ORDER BY created_at DESC, id DESC LIMIT $3`, m.MerchantID, q, limit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	payments := []paymentView{}
	for rows.Next() {
		p, err := readPayment(rows)
		if err != nil {
			return 0, nil, err
		}
		payments = append(payments, s.view(p, m.Role != "viewer"))
	}
	return http.StatusOK, payments, rows.Err()
}

type workspaceSetup struct {
	Sites               []siteCard `json:"sites"`
	RecentCurrency      *string    `json:"recent_currency"`
	OpenCurrencies      []string   `json:"open_currencies"`
	EffectiveAccountIDs []string   `json:"effective_account_ids"`
}

func readWorkspaceSetup(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	ctx := r.Context()
	result := workspaceSetup{OpenCurrencies: []string{}, EffectiveAccountIDs: []string{}}
	err := tx.QueryRowContext(ctx, `SELECT (SELECT currency FROM payments WHERE merchant_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 1)`, m.MerchantID).Scan(&result.RecentCurrency)
	if err != nil {
		return 0, nil, err
	}
	routes, err := readEffectiveCollectionRoutes(ctx, tx, m.MerchantID)
	if err != nil {
		return 0, nil, err
	}
	seen := make(map[string]bool)
	for _, route := range routes {
		result.EffectiveAccountIDs = append(result.EffectiveAccountIDs, route.AccountID)
		if !seen[route.Currency] {
			result.OpenCurrencies = append(result.OpenCurrencies, route.Currency)
			seen[route.Currency] = true
		}
	}
	result.Sites, err = readSiteCards(ctx, tx, m.MerchantID, routes)
	return http.StatusOK, result, err
}

type workspaceSettlementCurrency struct {
	Currency            string  `json:"currency"`
	PendingNetMinor     *string `json:"pending_net_minor"`
	SettledNetMinor     *string `json:"settled_net_minor"`
	LatestStatementDate *string `json:"latest_settled_statement_date"`
	StatementAccountID  string  `json:"statement_account_id"`
}

func readWorkspaceSettlementSummary(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	// Import validates and commits each batch and its rows together. Sum each
	// stored batch once using reported net, retaining NULL for an absent status.
	values, err := readPlatformRows(r.Context(), tx, `
		SELECT currency,
			(sum(net_minor) FILTER (WHERE settlement_status='pending'))::text,
			(sum(net_minor) FILTER (WHERE settlement_status='settled'))::text,
			to_char(max(statement_date) FILTER (WHERE settlement_status='settled'),'YYYY-MM-DD'),
			min(account_id)
		FROM sandbox_settlement_batches
		WHERE merchant_id=$1
		GROUP BY currency ORDER BY currency`, func(rows *sql.Rows, v *workspaceSettlementCurrency) error {
		return rows.Scan(&v.Currency, &v.PendingNetMinor, &v.SettledNetMinor, &v.LatestStatementDate, &v.StatementAccountID)
	}, m.MerchantID)
	return http.StatusOK, values, err
}
