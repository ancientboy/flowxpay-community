package main

import (
	"database/sql"
	"net/http"
	"time"
)

type platformFinanceAmount struct {
	Currency        string  `json:"currency"`
	Direction       string  `json:"direction"`
	Orders          int64   `json:"orders"`
	Succeeded       int64   `json:"succeeded"`
	InProgress      int64   `json:"in_progress"`
	SuccessfulMinor string  `json:"successful_minor"`
	Priced          *int64  `json:"priced,omitempty"`
	RevenueMinor    *string `json:"estimated_revenue_minor,omitempty"`
	CostMinor       *string `json:"estimated_cost_minor,omitempty"`
	MarginMinor     *string `json:"estimated_margin_minor,omitempty"`
}

type platformPayoutRow struct {
	ID          string `json:"id"`
	MerchantID  string `json:"merchant_id"`
	Merchant    string `json:"merchant"`
	Reference   string `json:"merchant_reference"`
	Currency    string `json:"currency"`
	AmountMinor string `json:"amount_minor"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type platformSettlementRow struct {
	ID             string `json:"id"`
	MerchantID     string `json:"merchant_id"`
	Merchant       string `json:"merchant"`
	AccountID      string `json:"account_id"`
	Currency       string `json:"currency"`
	StatementDate  string `json:"statement_date"`
	Settlement     string `json:"settlement"`
	Withdrawal     string `json:"withdrawal"`
	StatementGross string `json:"statement_gross_minor"`
	StatementFee   string `json:"statement_fee_minor"`
	StatementNet   string `json:"statement_net_minor"`
	Rows           int    `json:"rows"`
	Differences    int    `json:"differences"`
}

func readPlatformFinance(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _, merchant string) (int, any, error) {
	const date = "2006-01-02"
	query := r.URL.Query()
	now := time.Now().UTC().Truncate(24 * time.Hour)
	from, to := now.AddDate(0, 0, -6), now
	var err error
	if value := query.Get("from"); value != "" {
		from, err = time.Parse(date, value)
		if err != nil {
			fail(w, 400, "invalid_date_range", "Use UTC dates in YYYY-MM-DD format.")
			return 0, nil, nil
		}
	}
	if value := query.Get("to"); value != "" {
		to, err = time.Parse(date, value)
		if err != nil {
			fail(w, 400, "invalid_date_range", "Use UTC dates in YYYY-MM-DD format.")
			return 0, nil, nil
		}
	}
	if from.After(to) || to.Sub(from) > 30*24*time.Hour {
		fail(w, 400, "invalid_date_range", "Select up to 31 UTC calendar days.")
		return 0, nil, nil
	}
	end := to.AddDate(0, 0, 1)
	ctx := r.Context()
	operator := ctx.Value(platformRoleKey{}) == "operator"

	summary, err := readPlatformRows(ctx, tx, `
		WITH orders AS (
			SELECT currency, 'collection' AS direction, amount_minor, status, pricing_snapshot FROM payments
			WHERE ($1='' OR merchant_id=$1) AND created_at >= $2 AND created_at < $3
			UNION ALL
			SELECT currency, 'payout', amount_minor, status, pricing_snapshot FROM sandbox_payouts
			WHERE ($1='' OR merchant_id=$1) AND created_at >= $2 AND created_at < $3
		), priced AS (
			SELECT *, CASE WHEN $4 AND status='succeeded'
				AND jsonb_typeof(pricing_snapshot->'quote'->'fee_minor')='number'
				AND jsonb_typeof(pricing_snapshot->'cost'->'fee_minor')='number'
				THEN (pricing_snapshot->'quote'->>'fee_minor')::numeric END AS revenue,
				CASE WHEN $4 AND status='succeeded'
				AND jsonb_typeof(pricing_snapshot->'quote'->'fee_minor')='number'
				AND jsonb_typeof(pricing_snapshot->'cost'->'fee_minor')='number'
				THEN (pricing_snapshot->'cost'->>'fee_minor')::numeric END AS cost
			FROM orders
		)
		SELECT currency,direction,count(*),count(*) FILTER (WHERE status='succeeded'),
			count(*) FILTER (WHERE status IN ('draft','processing','pending_confirmation','requires_action')),
			COALESCE(sum(amount_minor) FILTER (WHERE status='succeeded'),0)::text,
			count(revenue),sum(revenue)::text,sum(cost)::text,sum(revenue-cost)::text
		FROM priced GROUP BY currency,direction ORDER BY currency,direction`, func(rows *sql.Rows, v *platformFinanceAmount) error {
		var priced int64
		if err := rows.Scan(&v.Currency, &v.Direction, &v.Orders, &v.Succeeded, &v.InProgress,
			&v.SuccessfulMinor, &priced, &v.RevenueMinor, &v.CostMinor, &v.MarginMinor); err != nil {
			return err
		}
		if operator {
			v.Priced = &priced
		}
		return nil
	}, merchant, from, end, operator)
	if err != nil {
		return 0, nil, err
	}

	payouts, err := readPlatformRows(ctx, tx, `
		SELECT p.id,p.merchant_id,m.name,p.merchant_reference,p.currency,p.amount_minor::text,p.status,
			to_char(p.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM sandbox_payouts p JOIN merchants m ON m.id=p.merchant_id
		WHERE ($1='' OR p.merchant_id=$1) AND p.created_at >= $2 AND p.created_at < $3
		ORDER BY p.created_at DESC,p.id DESC LIMIT 100`, func(rows *sql.Rows, v *platformPayoutRow) error {
		return rows.Scan(&v.ID, &v.MerchantID, &v.Merchant, &v.Reference, &v.Currency, &v.AmountMinor, &v.Status, &v.CreatedAt)
	}, merchant, from, end)
	if err != nil {
		return 0, nil, err
	}

	settlements, err := readPlatformRows(ctx, tx, `
		WITH recent AS (
			SELECT * FROM sandbox_settlement_batches
			WHERE ($1='' OR merchant_id=$1) AND statement_date >= $2::date AND statement_date < $3::date
			ORDER BY statement_date DESC,merchant_id,account_id,currency,id LIMIT 100
		)
		SELECT b.id,b.merchant_id,m.name,b.account_id,b.currency,to_char(b.statement_date,'YYYY-MM-DD'),
			b.settlement_status,b.withdrawal_status,b.gross_minor::text,b.fee_minor::text,b.net_minor::text,b.row_count,
			(SELECT count(*) FROM sandbox_settlement_entries e
			 WHERE (e.merchant_id,e.account_id,e.currency,e.batch_id)=(b.merchant_id,b.account_id,b.currency,b.id)
			   AND (e.match_status<>'matched' OR e.gross_minor::numeric-e.fee_minor<>e.net_minor))
		FROM recent b JOIN merchants m ON m.id=b.merchant_id
		ORDER BY b.statement_date DESC,b.merchant_id,b.account_id,b.currency,b.id`, func(rows *sql.Rows, v *platformSettlementRow) error {
		return rows.Scan(&v.ID, &v.MerchantID, &v.Merchant, &v.AccountID, &v.Currency, &v.StatementDate,
			&v.Settlement, &v.Withdrawal, &v.StatementGross, &v.StatementFee, &v.StatementNet, &v.Rows, &v.Differences)
	}, merchant, from.Format(date), end.Format(date))
	return http.StatusOK, map[string]any{
		"from": from.Format(date), "to": to.Format(date), "summary": summary,
		"payouts": payouts, "settlements": settlements,
	}, err
}
