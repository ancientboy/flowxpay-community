package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type sandboxRefund struct {
	ID          string          `json:"id"`
	PaymentID   string          `json:"payment_id"`
	Currency    string          `json:"currency"`
	AmountMinor int64           `json:"amount_minor"`
	Status      string          `json:"status"`
	FailureCode string          `json:"failure_code,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	MerchantID  string          `json:"-"`
	Reference   string          `json:"-"`
	Original    string          `json:"-"`
	Route       collectionRoute `json:"-"`
}

const refundColumns = `id, payment_id, currency, amount_minor, status, failure_code, created_at, updated_at,
merchant_id, channel_reference, payment_reference, route_snapshot`

func readRefund(row scanner) (sandboxRefund, error) {
	var f sandboxRefund
	var route []byte
	err := row.Scan(&f.ID, &f.PaymentID, &f.Currency, &f.AmountMinor, &f.Status, &f.FailureCode, &f.CreatedAt,
		&f.UpdatedAt, &f.MerchantID, &f.Reference, &f.Original, &route)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(route, &f.Route)
}

var (
	errRefundConflict    = errors.New("refund idempotency conflict")
	errRefundUnavailable = errors.New("refund unavailable")
	errRefundAmount      = errors.New("amount exceeds unreserved payment balance")
)

func (s *server) reserveRefund(ctx context.Context, merchantID, paymentID, key string, amount int64) (sandboxRefund, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sandboxRefund{}, false, err
	}
	defer tx.Rollback()
	// Lock the original payment before any balance check, across workers and API processes.
	p, err := readPayment(tx.QueryRowContext(ctx, "SELECT "+paymentColumns+
		" FROM payments WHERE id=$1 AND merchant_id=$2 FOR NO KEY UPDATE", paymentID, merchantID))
	if err != nil {
		return sandboxRefund{}, false, err
	}
	lookup := func() (sandboxRefund, error) {
		f, err := readRefund(tx.QueryRowContext(ctx, "SELECT "+refundColumns+
			" FROM sandbox_refunds WHERE merchant_id=$1 AND idempotency_key=$2", merchantID, key))
		if err == nil && (f.PaymentID != paymentID || f.AmountMinor != amount) {
			return f, errRefundConflict
		}
		return f, err
	}
	f, err := lookup()
	if !errors.Is(err, sql.ErrNoRows) {
		return f, false, err
	}
	route, valid := resolveOriginalRoute(p)
	if p.Status != "succeeded" || !valid || !clientID.MatchString(p.ChannelReference.String) {
		return sandboxRefund{}, false, errRefundUnavailable
	}
	var reserved int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount_minor),0) FROM sandbox_refunds
		WHERE payment_id=$1 AND status IN ('pending_confirmation','succeeded')`, paymentID).Scan(&reserved); err != nil {
		return sandboxRefund{}, false, err
	}
	if amount > p.AmountMinor-reserved {
		return sandboxRefund{}, false, errRefundAmount
	}
	id := newResourceID("ref_")
	f, err = readRefund(tx.QueryRowContext(ctx, `
		INSERT INTO sandbox_refunds (id, merchant_id, payment_id, idempotency_key, amount_minor, currency,
			route_snapshot, payment_reference, status, channel_reference, failure_code, next_check_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending_confirmation',$9,'channel_confirmation_pending',now()+interval '5 seconds')
		ON CONFLICT (merchant_id,idempotency_key) DO NOTHING RETURNING `+refundColumns,
		id, merchantID, paymentID, key, amount, p.Currency, p.RouteSnapshot, p.ChannelReference.String, "sim_"+id))
	if errors.Is(err, sql.ErrNoRows) {
		f, err = lookup()
		return f, false, err
	}
	if err != nil {
		return f, false, err
	}
	if err := takeCollectionSlot(ctx, tx, route); err != nil {
		return f, false, err
	}
	return f, true, tx.Commit()
}

func (s *server) createRefund(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !clientID.MatchString(key) {
		fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Supply one valid Idempotency-Key.")
		return
	}
	var input struct {
		AmountMinor int64 `json:"amount_minor"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	if input.AmountMinor < 1 || input.AmountMinor > 999999999999 {
		fail(w, http.StatusBadRequest, "invalid_refund_amount", "Supply a positive refund amount in minor units.")
		return
	}
	f, created, err := s.reserveRefund(r.Context(), merchant.ID, r.PathValue("id"), key, input.AmountMinor)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(w, http.StatusNotFound, "payment_not_found", "Payment not found.")
	case errors.Is(err, errRefundConflict):
		fail(w, http.StatusConflict, "idempotency_conflict", "The key is already assigned to a different refund.")
	case errors.Is(err, errRefundUnavailable):
		fail(w, http.StatusConflict, "refund_unavailable", "This payment has no refundable original channel transaction.")
	case errors.Is(err, errRefundAmount):
		fail(w, http.StatusConflict, "refund_exceeds_balance", "That amount exceeds the remaining refundable balance.")
	case errors.Is(err, errChannelRateLimited):
		w.Header().Set("Retry-After", "1")
		fail(w, http.StatusTooManyRequests, "channel_rate_limited", "Retry with the original Idempotency-Key.")
	case err != nil:
		respondEventStoreError(w)
	case created:
		if updated, err := s.dispatchRefund(r.Context(), f); err == nil {
			f = updated
		}
		w.Header().Set("Location", "/v1/refunds/"+f.ID)
		respond(w, http.StatusCreated, f)
	default:
		w.Header().Set("Idempotency-Replayed", "true")
		respond(w, http.StatusOK, f)
	}
}

func (s *server) getRefund(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	f, err := readRefund(s.db.QueryRowContext(r.Context(), "SELECT "+refundColumns+
		" FROM sandbox_refunds WHERE id=$1 AND merchant_id=$2", r.PathValue("id"), merchant.ID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(w, http.StatusNotFound, "refund_not_found", "Refund not found.")
	case err != nil:
		respondEventStoreError(w)
	default:
		respond(w, http.StatusOK, f)
	}
}

func (s *server) listRefunds(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	ctx, paymentID := r.Context(), r.PathValue("id")
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM payments WHERE id=$1 AND merchant_id=$2)",
		paymentID, merchant.ID).Scan(&exists)
	if err != nil {
		respondEventStoreError(w)
		return
	}
	if !exists {
		fail(w, http.StatusNotFound, "payment_not_found", "Payment not found.")
		return
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+refundColumns+
		" FROM sandbox_refunds WHERE payment_id=$1 AND merchant_id=$2 ORDER BY created_at DESC, id DESC LIMIT 100",
		paymentID, merchant.ID)
	if err != nil {
		respondEventStoreError(w)
		return
	}
	defer rows.Close()
	items := []sandboxRefund{}
	for rows.Next() {
		f, err := readRefund(rows)
		if err != nil {
			respondEventStoreError(w)
			return
		}
		items = append(items, f)
	}
	if rows.Err() != nil {
		respondEventStoreError(w)
		return
	}
	respond(w, http.StatusOK, map[string]any{"data": items})
}

func (s *server) dispatchRefund(ctx context.Context, f sandboxRefund) (sandboxRefund, error) {
	ctx = context.WithoutCancel(ctx)
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := s.channelRecovery().Refund(attempt, f)
	if attempt.Err() != nil {
		err = attempt.Err()
	}
	cancel()
	var status, failure string
	switch {
	case err == nil:
		status = "succeeded"
	case errors.Is(err, errChannelRejected):
		status, failure = "failed", "channel_rejected"
	case errors.Is(err, errChannelRateLimited):
		status, failure = "failed", "channel_rate_limited"
	default:
		return f, err
	}
	write, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	tx, err := s.db.BeginTx(write, nil)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	current, err := readRefund(tx.QueryRowContext(write, "SELECT "+refundColumns+" FROM sandbox_refunds WHERE id=$1 FOR NO KEY UPDATE", f.ID))
	if err != nil {
		return f, err
	}
	if terminalOutcome(current.Status, status) == "applied" {
		current, err = s.finishRefund(write, tx, current, status, failure)
		if err != nil {
			return f, err
		}
	}
	return current, tx.Commit()
}

func (s *server) finishRefund(ctx context.Context, tx *sql.Tx, f sandboxRefund, status, failure string) (sandboxRefund, error) {
	f, err := readRefund(tx.QueryRowContext(ctx, `
		UPDATE sandbox_refunds SET status=$2, failure_code=$3, updated_at=now(), next_check_at=NULL
		WHERE id=$1 RETURNING `+refundColumns, f.ID, status, failure))
	if err != nil {
		return f, err
	}
	return f, s.enqueueEvent(ctx, tx, f.MerchantID, f.ID, businessEvent{
		Type: "refund." + status, PaymentID: f.PaymentID, RefundID: f.ID, Status: status, AmountMinor: f.AmountMinor, Currency: f.Currency,
	})
}
