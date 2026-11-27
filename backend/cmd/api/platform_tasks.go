package main

import (
	"context"
	"database/sql"
)

type platformTask struct {
	Kind         string `json:"kind"`
	MerchantID   string `json:"merchant_id"`
	MerchantName string `json:"merchant_name"`
	SiteID       string `json:"site_id"`
	SiteName     string `json:"site_name"`
	RouteID      string `json:"route_id,omitempty"`
	Currency     string `json:"currency,omitempty"`
	Method       string `json:"method,omitempty"`
}

type platformTaskGroup struct {
	Kind  string         `json:"kind"`
	Total int            `json:"total"`
	Items []platformTask `json:"items"`
}

func readPlatformTasks(ctx context.Context, tx *sql.Tx, merchant string, session platformSession) ([]platformTaskGroup, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH tasks AS (
			SELECT 'assignment' AS kind, s.merchant_id, s.id AS site_id,
				'' AS route_id, '' AS currency, '' AS method, NULL::timestamptz AS submitted_at
			FROM merchant_sites s
			WHERE ($1='' OR s.merchant_id=$1) AND NOT EXISTS (
				SELECT 1 FROM sandbox_channel_accounts a
				WHERE a.merchant_id=s.merchant_id AND a.site_id=s.id AND a.environment='sandbox'
			)
			UNION ALL
			SELECT CASE
				WHEN NOT (a.environment='sandbox' AND a.enabled AND COALESCE(c.enabled,true)) THEN 'blocked'
				WHEN $2 <> 'operator' THEN 'pending'
				ELSE 'can_approve' END,
				r.merchant_id, r.site_id, r.id, r.currency, r.method, r.submitted_at
			FROM collection_route_changes r
			JOIN sandbox_channel_accounts a ON (a.id,a.merchant_id,a.site_id)=(r.account_id,r.merchant_id,r.site_id)
			LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
			WHERE ($1='' OR r.merchant_id=$1) AND r.platform_submitted_by IS NOT NULL
				AND r.approved_at IS NULL AND r.cancelled_at IS NULL
		), ranked AS (
			SELECT tasks.*, m.name AS merchant_name, s.name AS site_name,
				count(*) OVER (PARTITION BY kind) AS total,
				row_number() OVER (PARTITION BY kind ORDER BY submitted_at, lower(m.name), lower(s.name), site_id, route_id) AS position
			FROM tasks JOIN merchants m ON m.id=tasks.merchant_id
			JOIN merchant_sites s ON s.id=tasks.site_id AND s.merchant_id=tasks.merchant_id
		)
		SELECT kind,merchant_id,merchant_name,site_id,site_name,route_id,currency,method,total
		FROM ranked WHERE position<=10 ORDER BY kind,position`, merchant, session.Role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []platformTaskGroup{}
	for rows.Next() {
		var task platformTask
		var total int
		if err := rows.Scan(&task.Kind, &task.MerchantID, &task.MerchantName, &task.SiteID, &task.SiteName,
			&task.RouteID, &task.Currency, &task.Method, &total); err != nil {
			return nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].Kind != task.Kind {
			groups = append(groups, platformTaskGroup{Kind: task.Kind, Total: total})
		}
		group := &groups[len(groups)-1]
		group.Items = append(group.Items, task)
	}
	return groups, rows.Err()
}
