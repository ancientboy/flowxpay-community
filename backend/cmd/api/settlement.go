package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
)

const settlementBatchLimit = 500
const settlementPageSize = 100
const settlementRoot = "/sandbox/workspace/settlement/{account}/{currency}"

type settlementScope struct {
	merchant, account, currency string
}

type statementBatch struct {
	ID                  string         `json:"id"`
	Date                string         `json:"statement_date"`
	SettlementStatus    string         `json:"settlement_status"`
	WithdrawalStatus    string         `json:"withdrawal_status"`
	WithdrawalReference string         `json:"withdrawal_reference"`
	Entries             []statementRow `json:"entries"`
}

type statementRow struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	ChannelReference string `json:"channel_reference"`
	ReversesEntry    string `json:"reverses_entry,omitempty"`
	GrossMinor       *int64 `json:"gross_minor"`
	FeeMinor         *int64 `json:"fee_minor"`
	NetMinor         *int64 `json:"net_minor"`
}

type orderCheck struct {
	ID       string `json:"order_id,omitempty"`
	Status   string `json:"order_status,omitempty"`
	Expected *int64 `json:"expected_gross_minor,omitempty"`
	Match    string `json:"match_status"`
}

type settlementEntry struct {
	statementRow
	orderCheck
	CalculatedNet int64  `json:"calculated_net_minor"`
	Acknowledged  bool   `json:"acknowledged"`
	Revision      int    `json:"revision"`
	Difference    string `json:"difference_status"`
}

func (s *server) registerSettlementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+settlementRoot+"/totals", s.settlementRoute(readInventory, settlementTotals))
	mux.HandleFunc("GET "+settlementRoot+"/batches", s.settlementRoute(readInventory, listSettlementBatches))
	mux.HandleFunc("POST "+settlementRoot+"/batches", s.settlementRoute(manageSettlement, importSettlement))
	mux.HandleFunc("GET "+settlementRoot+"/batches/{batch}", s.settlementRoute(readInventory, getSettlementBatch))
	mux.HandleFunc("GET "+settlementRoot+"/batches/{batch}/entries.csv", s.settlementRoute(readInventory, exportSettlementBatch))
	mux.HandleFunc("GET "+settlementRoot+"/orders/{kind}/report.csv", s.settlementRoute(readInventory, exportSettlementOrders))
	mux.HandleFunc("GET "+settlementRoot+"/orders/{kind}", s.settlementRoute(readInventory, listSettlementOrders))
	mux.HandleFunc("GET "+settlementRoot+"/entries/{entry}/actions", s.settlementRoute(readInventory, listSettlementActions))
	mux.HandleFunc("POST "+settlementRoot+"/entries/{entry}/actions", s.settlementRoute(manageSettlement, actOnSettlement))
}

type settlementHandler func(http.ResponseWriter, *http.Request, *sql.Tx, member, settlementScope) (int, any, error)

func (s *server) settlementRoute(permission memberPermission, handle settlementHandler) http.HandlerFunc {
	authorized := s.authorizeMember(permission, func(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
		scope := settlementScope{m.MerchantID, r.PathValue("account"), r.PathValue("currency")}
		var provider string
		err := tx.QueryRowContext(r.Context(), `
			SELECT provider FROM sandbox_channel_accounts WHERE merchant_id=$1 AND id=$2`,
			scope.merchant, scope.account).Scan(&provider)
		if err != nil {
			return 0, nil, err
		}
		profile, supported := profileFor(provider)
		if !supported || profile.capability.Currency != scope.currency {
			return settlementError(w, http.StatusBadRequest, "invalid_currency", "Use the currency assigned to this channel account.")
		}
		return handle(w, r, tx, m, scope)
	})
	return s.settlementReport(authorized)
}

func (s *server) settlementReport(authorized http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bound reporting work before it can consume a connection from the shared pool.
		for i := range s.settlementSlots {
			if s.settlementSlots[i].TryLock() {
				defer s.settlementSlots[i].Unlock()
				ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
				defer cancel()
				authorized(w, r.WithContext(ctx))
				return
			}
		}
		w.Header().Set("Retry-After", "1")
		fail(w, http.StatusTooManyRequests, "report_busy", "Settlement reports are busy. Retry shortly.")
	}
}

func settlementError(w http.ResponseWriter, status int, code, message string) (int, any, error) {
	fail(w, status, code, message)
	return 0, nil, nil
}

func validStatementAmount(amount *int64) bool {
	const limit = 999999999999
	return amount != nil && *amount >= -limit && *amount <= limit
}

func (b *statementBatch) validate() bool {
	date, err := time.Parse(time.DateOnly, b.Date)
	if err != nil || date.Year() < 1 || b.Date != date.Format(time.DateOnly) || !clientID.MatchString(b.ID) ||
		len(b.Entries) == 0 || len(b.Entries) > settlementBatchLimit ||
		(b.SettlementStatus != "pending" && b.SettlementStatus != "settled") {
		return false
	}
	switch b.WithdrawalStatus {
	case "not_reported":
		if b.WithdrawalReference != "" {
			return false
		}
	case "pending", "completed", "failed":
		if !clientID.MatchString(b.WithdrawalReference) {
			return false
		}
	default:
		return false
	}
	ids := make(map[string]bool, len(b.Entries))
	for _, row := range b.Entries {
		if !clientID.MatchString(row.ID) || ids[row.ID] || !clientID.MatchString(row.ChannelReference) ||
			!validStatementAmount(row.GrossMinor) || !validStatementAmount(row.FeeMinor) || !validStatementAmount(row.NetMinor) {
			return false
		}
		ids[row.ID] = true
		switch row.Kind {
		case "collection":
			if row.ReversesEntry != "" || *row.GrossMinor <= 0 || *row.FeeMinor < 0 {
				return false
			}
		case "refund", "payout":
			if row.ReversesEntry != "" || *row.GrossMinor >= 0 || *row.FeeMinor < 0 {
				return false
			}
		case "reversal":
			if !clientID.MatchString(row.ReversesEntry) || row.ReversesEntry == row.ID {
				return false
			}
		default:
			return false
		}
	}
	// Accept retransmission with reordered rows. Base entries always precede reversals.
	sort.Slice(b.Entries, func(i, j int) bool {
		a, b := b.Entries[i], b.Entries[j]
		if (a.Kind == "reversal") != (b.Kind == "reversal") {
			return a.Kind != "reversal"
		}
		return a.ID < b.ID
	})
	return true
}

func importSettlement(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member, scope settlementScope) (int, any, error) {
	var input statementBatch
	if !parseJSONUpTo(w, r, &input, 256<<10) {
		return 0, nil, nil
	}
	if !input.validate() {
		return settlementError(w, http.StatusBadRequest, "invalid_statement", "Check the statement date, statuses, row IDs and integer amounts.")
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	hash := sha256.Sum256(canonical)
	var gross, fee, net int64
	for _, row := range input.Entries {
		gross += *row.GrossMinor
		fee += *row.FeeMinor
		net += *row.NetMinor
	}

	ctx := r.Context()
	// Serialize writes only within the statement's account and currency.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		"settlement:"+scope.merchant+"/"+scope.account+"/"+scope.currency); err != nil {
		return 0, nil, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO sandbox_settlement_batches
		    (merchant_id,account_id,currency,id,statement_date,settlement_status,withdrawal_status,
		     withdrawal_reference,body_hash,imported_by,row_count,gross_minor,fee_minor,net_minor)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT DO NOTHING`,
		scope.merchant, scope.account, scope.currency, input.ID, input.Date, input.SettlementStatus, input.WithdrawalStatus,
		input.WithdrawalReference, hash[:], m.ID, len(input.Entries), gross, fee, net)
	if err != nil {
		return 0, nil, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	if count == 0 {
		var previous []byte
		err = tx.QueryRowContext(ctx, `
			SELECT body_hash FROM sandbox_settlement_batches
			WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id=$4`,
			scope.merchant, scope.account, scope.currency, input.ID).Scan(&previous)
		if err != nil {
			return 0, nil, err
		}
		if !bytes.Equal(previous, hash[:]) {
			return settlementError(w, http.StatusConflict, "statement_conflict", "A different statement already uses this batch ID.")
		}
		return http.StatusOK, map[string]any{"id": input.ID, "replayed": true}, nil
	}

	for _, row := range input.Entries {
		check, err := matchSettlement(ctx, tx, scope, row, input.Date)
		if errors.Is(err, errInvalidReversal) {
			return settlementError(w, http.StatusConflict, "invalid_reversal", "The reversal must reference an original row in this account, on or before this date, with opposite gross and fee.")
		}
		if err != nil {
			return 0, nil, err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO sandbox_settlement_entries
			    (merchant_id,account_id,currency,batch_id,id,kind,channel_reference,reverses_entry,gross_minor,fee_minor,
			     net_minor,order_id,order_status,expected_gross_minor,match_status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,NULLIF($12,''),NULLIF($13,''),$14,$15)
			ON CONFLICT DO NOTHING`,
			scope.merchant, scope.account, scope.currency, input.ID, row.ID, row.Kind, row.ChannelReference,
			row.ReversesEntry, *row.GrossMinor, *row.FeeMinor, *row.NetMinor, check.ID, check.Status, check.Expected, check.Match)
		if err != nil {
			return 0, nil, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return 0, nil, err
		}
		if count == 0 {
			return settlementError(w, http.StatusConflict, "entry_conflict", "An entry ID, channel reference or reversal target already exists in this account.")
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sandbox_settlement_daily AS d
		    (merchant_id,account_id,currency,statement_date,batch_count,row_count,gross_minor,fee_minor,net_minor)
		VALUES ($1,$2,$3,$4,1,$5,$6,$7,$8)
		ON CONFLICT (merchant_id,account_id,currency,statement_date) DO UPDATE SET
		    batch_count=d.batch_count+1,row_count=d.row_count+EXCLUDED.row_count,
		    gross_minor=d.gross_minor+EXCLUDED.gross_minor,fee_minor=d.fee_minor+EXCLUDED.fee_minor,
		    net_minor=d.net_minor+EXCLUDED.net_minor`,
		scope.merchant, scope.account, scope.currency, input.Date, len(input.Entries), gross, fee, net)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, map[string]any{"id": input.ID, "replayed": false}, nil
}

var errInvalidReversal = errors.New("invalid statement reversal")

// These queries read the payment path's state without taking row locks.
const paymentOrderSource = `SELECT id, channel_reference, amount_minor,
	CASE WHEN status='requires_action' AND expires_at<=statement_timestamp() THEN 'expired' ELSE status END
	FROM payments
	WHERE merchant_id=$1 AND route_snapshot->>'account_id'=$2 AND currency=$3`
const refundOrderSource = `SELECT id, channel_reference, -amount_minor, status FROM sandbox_refunds
	WHERE merchant_id=$1 AND route_snapshot->>'account_id'=$2 AND currency=$3`
const payoutOrderSource = `SELECT id, channel_reference, -amount_minor, status
	FROM sandbox_payouts WHERE merchant_id=$1 AND account_id=$2 AND currency=$3`

func settlementOrderSource(kind string) string {
	switch kind {
	case "collection":
		return paymentOrderSource
	case "refund":
		return refundOrderSource
	case "payout":
		return payoutOrderSource
	default:
		return ""
	}
}

func matchSettlement(ctx context.Context, tx *sql.Tx, scope settlementScope, row statementRow, date string) (orderCheck, error) {
	kind := row.Kind
	if kind == "reversal" {
		var reference string
		var gross, fee int64
		err := tx.QueryRowContext(ctx, `
			SELECT e.kind,e.channel_reference,e.gross_minor,e.fee_minor
			FROM sandbox_settlement_entries e JOIN sandbox_settlement_batches b
			    ON (b.merchant_id,b.account_id,b.currency,b.id)=(e.merchant_id,e.account_id,e.currency,e.batch_id)
			WHERE e.merchant_id=$1 AND e.account_id=$2 AND e.currency=$3 AND e.id=$4
			  AND e.reverses_entry IS NULL AND b.statement_date <= $5`,
			scope.merchant, scope.account, scope.currency, row.ReversesEntry, date).
			Scan(&kind, &reference, &gross, &fee)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (reference != row.ChannelReference ||
			*row.GrossMinor != -gross || *row.FeeMinor != -fee)) {
			return orderCheck{}, errInvalidReversal
		}
		if err != nil {
			return orderCheck{}, err
		}
	}

	query := settlementOrderSource(kind) + " AND channel_reference=$4 LIMIT 2"
	rows, err := tx.QueryContext(ctx, query, scope.merchant, scope.account, scope.currency, row.ChannelReference)
	if err != nil {
		return orderCheck{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return orderCheck{Match: "missing"}, rows.Err()
	}
	var check orderCheck
	var reference string
	var expected int64
	if err := rows.Scan(&check.ID, &reference, &expected, &check.Status); err != nil {
		return orderCheck{}, err
	}
	if rows.Next() {
		return orderCheck{Match: "ambiguous"}, nil
	}
	if err := rows.Err(); err != nil {
		return orderCheck{}, err
	}
	if row.Kind == "reversal" {
		expected = -expected
	}
	check.Expected = &expected
	switch {
	case expected != *row.GrossMinor:
		check.Match = "amount_mismatch"
	case check.Status != "succeeded":
		check.Match = "status_mismatch"
	default:
		check.Match = "matched"
	}
	return check, nil
}
