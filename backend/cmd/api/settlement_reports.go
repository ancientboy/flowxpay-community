package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type settlementAmounts struct {
	GrossMinor    int64 `json:"gross_minor"`
	FeeMinor      int64 `json:"fee_minor"`
	NetMinor      int64 `json:"net_minor"`
	CalculatedNet int64 `json:"calculated_net_minor"`
}

type batchSummary struct {
	ID                  string    `json:"id"`
	AccountID           string    `json:"account_id"`
	Currency            string    `json:"currency"`
	Date                string    `json:"statement_date"`
	SettlementStatus    string    `json:"settlement_status"`
	WithdrawalStatus    string    `json:"withdrawal_status"`
	WithdrawalReference string    `json:"withdrawal_reference,omitempty"`
	ImportedBy          string    `json:"imported_by"`
	ImportedAt          time.Time `json:"imported_at"`
	RowCount            int       `json:"row_count"`
	settlementAmounts
}

type settlementPage[T any] struct {
	Data      []T    `json:"data"`
	NextAfter string `json:"next_after,omitempty"`
}

const settlementBatchColumns = `id,account_id,currency,to_char(statement_date,'YYYY-MM-DD'),
	settlement_status,withdrawal_status,withdrawal_reference,imported_by,imported_at,row_count,gross_minor,fee_minor,net_minor`

func readSettlementBatch(row scanner) (batchSummary, error) {
	var b batchSummary
	err := row.Scan(&b.ID, &b.AccountID, &b.Currency, &b.Date, &b.SettlementStatus, &b.WithdrawalStatus,
		&b.WithdrawalReference, &b.ImportedBy, &b.ImportedAt, &b.RowCount, &b.GrossMinor, &b.FeeMinor, &b.NetMinor)
	b.CalculatedNet = b.GrossMinor - b.FeeMinor
	return b, err
}

func settlementTotals(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	from, through := r.URL.Query().Get("from"), r.URL.Query().Get("through")
	start, startErr := time.Parse(time.DateOnly, from)
	end, endErr := time.Parse(time.DateOnly, through)
	if startErr != nil || endErr != nil || start.Year() < 1 || from != start.Format(time.DateOnly) ||
		through != end.Format(time.DateOnly) || end.Before(start) || end.After(start.AddDate(0, 0, 365)) {
		return settlementError(w, http.StatusBadRequest, "invalid_period", "Provide ISO dates from and through, at most 366 days inclusive.")
	}
	value := struct {
		AccountID     string `json:"account_id"`
		Currency      string `json:"currency"`
		From          string `json:"from"`
		Through       string `json:"through"`
		BatchCount    int64  `json:"batch_count"`
		RowCount      int64  `json:"row_count"`
		GrossMinor    string `json:"gross_minor"`
		FeeMinor      string `json:"fee_minor"`
		NetMinor      string `json:"net_minor"`
		CalculatedNet string `json:"calculated_net_minor"`
	}{AccountID: scope.account, Currency: scope.currency, From: from, Through: through}
	err := tx.QueryRowContext(r.Context(), `
		SELECT COALESCE(sum(batch_count),0), COALESCE(sum(row_count),0),
		    COALESCE(sum(gross_minor),0)::text, COALESCE(sum(fee_minor),0)::text,
		    COALESCE(sum(net_minor),0)::text, COALESCE(sum(gross_minor-fee_minor),0)::text
		FROM sandbox_settlement_daily WHERE merchant_id=$1 AND account_id=$2 AND currency=$3
		    AND statement_date BETWEEN $4 AND $5`,
		scope.merchant, scope.account, scope.currency, from, through).
		Scan(&value.BatchCount, &value.RowCount, &value.GrossMinor, &value.FeeMinor, &value.NetMinor, &value.CalculatedNet)
	return http.StatusOK, value, err
}

func listSettlementBatches(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	after := r.URL.Query().Get("after")
	if after != "" && !clientID.MatchString(after) {
		return settlementError(w, http.StatusBadRequest, "invalid_cursor", "Use the cursor from the previous page.")
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT "+settlementBatchColumns+`
		FROM sandbox_settlement_batches WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id>$4
		ORDER BY id LIMIT $5`, scope.merchant, scope.account, scope.currency, after, settlementPageSize+1)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	page := settlementPage[batchSummary]{Data: []batchSummary{}}
	for rows.Next() {
		b, err := readSettlementBatch(rows)
		if err != nil {
			return 0, nil, err
		}
		if len(page.Data) == settlementPageSize {
			page.NextAfter = page.Data[len(page.Data)-1].ID
			break
		}
		page.Data = append(page.Data, b)
	}
	return http.StatusOK, page, rows.Err()
}

const settlementEntryColumns = `id,kind,channel_reference,COALESCE(reverses_entry,''),
	gross_minor,fee_minor,net_minor,COALESCE(order_id,''),COALESCE(order_status,''),
	expected_gross_minor,match_status,acknowledged,revision`

func readSettlementEntry(row scanner) (settlementEntry, error) {
	var e settlementEntry
	var gross, fee, net int64
	err := row.Scan(&e.statementRow.ID, &e.Kind, &e.ChannelReference, &e.ReversesEntry, &gross, &fee, &net,
		&e.orderCheck.ID, &e.Status, &e.Expected, &e.Match, &e.Acknowledged, &e.Revision)
	e.GrossMinor, e.FeeMinor, e.NetMinor = &gross, &fee, &net
	e.updateDifference()
	return e, err
}

func (e *settlementEntry) updateDifference() {
	e.CalculatedNet = *e.GrossMinor - *e.FeeMinor
	switch {
	case e.Match == "matched" && e.CalculatedNet == *e.NetMinor:
		e.Difference = "matched"
	case e.Acknowledged:
		e.Difference = "acknowledged"
	default:
		e.Difference = "open"
	}
}

type settlementBatchDetail struct {
	batchSummary
	Entries []settlementEntry `json:"entries"`
}

func loadSettlementBatch(r *http.Request, tx *sql.Tx, scope settlementScope) (settlementBatchDetail, error) {
	ctx := r.Context()
	id := r.PathValue("batch")
	b, err := readSettlementBatch(tx.QueryRowContext(ctx, "SELECT "+settlementBatchColumns+`
		FROM sandbox_settlement_batches WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id=$4`,
		scope.merchant, scope.account, scope.currency, id))
	if err != nil {
		return settlementBatchDetail{}, err
	}
	result := settlementBatchDetail{batchSummary: b, Entries: make([]settlementEntry, 0, b.RowCount)}
	rows, err := tx.QueryContext(ctx, "SELECT "+settlementEntryColumns+`
		FROM sandbox_settlement_entries WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND batch_id=$4
		ORDER BY id LIMIT $5`, scope.merchant, scope.account, scope.currency, id, settlementBatchLimit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := readSettlementEntry(rows)
		if err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, e)
	}
	return result, rows.Err()
}

func getSettlementBatch(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	value, err := loadSettlementBatch(r, tx, scope)
	return http.StatusOK, value, err
}

type settlementCSV struct {
	data      []byte
	name      string
	nextAfter string
}

func (file settlementCSV) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+file.name)
	if file.nextAfter != "" {
		w.Header().Set("X-Next-After", file.nextAfter)
	}
	_, _ = w.Write(file.data)
}

func safeCSVText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\n\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

func exportSettlementBatch(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	b, err := loadSettlementBatch(r, tx, scope)
	if err != nil {
		return 0, nil, err
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"account_id", "currency", "batch_id", "statement_date", "settlement_status", "withdrawal_status",
		"withdrawal_reference", "entry_id", "kind", "channel_reference", "reverses_entry", "gross_minor", "fee_minor",
		"net_minor", "calculated_net_minor", "order_id", "order_status", "expected_gross_minor", "match_status", "difference_status"})
	number := func(a int64) string { return strconv.FormatInt(a, 10) }
	for _, e := range b.Entries {
		var expected string
		if e.Expected != nil {
			expected = number(*e.Expected)
		}
		_ = writer.Write([]string{safeCSVText(b.AccountID), b.Currency, safeCSVText(b.ID), b.Date, b.SettlementStatus,
			b.WithdrawalStatus, safeCSVText(b.WithdrawalReference), safeCSVText(e.statementRow.ID), e.Kind,
			safeCSVText(e.ChannelReference), safeCSVText(e.ReversesEntry), number(*e.GrossMinor), number(*e.FeeMinor),
			number(*e.NetMinor), number(e.CalculatedNet), safeCSVText(e.orderCheck.ID), e.Status, expected, e.Match, e.Difference})
	}
	writer.Flush()
	return http.StatusOK, settlementCSV{data: buffer.Bytes(), name: "settlement-entries.csv"}, writer.Error()
}

type settlementOrder struct {
	ID               string `json:"id"`
	ChannelReference string `json:"channel_reference,omitempty"`
	ExpectedGross    int64  `json:"expected_gross_minor"`
	Status           string `json:"status"`
	EntryCount       int64  `json:"entry_count"`
	settlementAmounts
}

func listSettlementOrders(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	after, kind := r.URL.Query().Get("after"), r.PathValue("kind")
	source := settlementOrderSource(kind)
	if source == "" || (after != "" && !clientID.MatchString(after)) {
		return settlementError(w, http.StatusBadRequest, "invalid_order_filter", "Use collection, refund or payout and the cursor from the previous page.")
	}
	rows, err := tx.QueryContext(r.Context(), `
		WITH orders(id,reference,amount,status) AS (`+source+` AND id>$4 ORDER BY id LIMIT $5)
		SELECT o.id,COALESCE(o.reference,''),o.amount,o.status,e.rows,e.gross,e.fee,e.net
		FROM orders o CROSS JOIN LATERAL (
		    SELECT count(*) AS rows,COALESCE(sum(gross_minor),0) AS gross,
		        COALESCE(sum(fee_minor),0) AS fee,COALESCE(sum(net_minor),0) AS net
		    FROM sandbox_settlement_entries WHERE merchant_id=$1 AND account_id=$2 AND currency=$3
		        AND order_id=o.id AND (kind=$6 OR kind='reversal')
		) e ORDER BY o.id`, scope.merchant, scope.account, scope.currency, after, settlementPageSize+1, kind)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	page := settlementPage[settlementOrder]{Data: []settlementOrder{}}
	for rows.Next() {
		var o settlementOrder
		if err := rows.Scan(&o.ID, &o.ChannelReference, &o.ExpectedGross, &o.Status, &o.EntryCount,
			&o.GrossMinor, &o.FeeMinor, &o.NetMinor); err != nil {
			return 0, nil, err
		}
		if len(page.Data) == settlementPageSize {
			page.NextAfter = page.Data[len(page.Data)-1].ID
			break
		}
		o.CalculatedNet = o.GrossMinor - o.FeeMinor
		page.Data = append(page.Data, o)
	}
	return http.StatusOK, page, rows.Err()
}

func exportSettlementOrders(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member, scope settlementScope) (int, any, error) {
	status, body, err := listSettlementOrders(w, r, tx, m, scope)
	if status != http.StatusOK || err != nil {
		return status, body, err
	}
	page := body.(settlementPage[settlementOrder])
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	kind := r.PathValue("kind")
	_ = writer.Write([]string{"account_id", "currency", "kind", "order_id", "channel_reference", "status",
		"expected_gross_minor", "entry_count", "gross_minor", "fee_minor", "net_minor", "calculated_net_minor"})
	number := func(a int64) string { return strconv.FormatInt(a, 10) }
	for _, o := range page.Data {
		_ = writer.Write([]string{safeCSVText(scope.account), scope.currency, kind, safeCSVText(o.ID),
			safeCSVText(o.ChannelReference), o.Status, number(o.ExpectedGross), number(o.EntryCount),
			number(o.GrossMinor), number(o.FeeMinor), number(o.NetMinor), number(o.CalculatedNet)})
	}
	writer.Flush()
	return http.StatusOK, settlementCSV{data: buffer.Bytes(), name: "settlement-orders.csv", nextAfter: page.NextAfter}, writer.Error()
}
