package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type businessEvent struct {
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	PaymentID         string    `json:"payment_id,omitempty"`
	RefundID          string    `json:"refund_id,omitempty"`
	PayoutID          string    `json:"payout_id,omitempty"`
	AccountID         string    `json:"account_id,omitempty"`
	TransferReference string    `json:"transfer_reference,omitempty"`
	Status            string    `json:"status"`
	Currency          string    `json:"currency"`
	AmountMinor       int64     `json:"amount_minor"`
	OccurredAt        time.Time `json:"occurred_at"`
}

func terminal(status string) bool {
	return status == "succeeded" || status == "failed"
}

func terminalOutcome(current, incoming string) string {
	switch {
	case !terminal(incoming), current == incoming:
		return "ignored"
	case terminal(current):
		return "terminal_conflict"
	default:
		return "applied"
	}
}

func resolveOriginalRoute(p payment) (collectionRoute, bool) {
	var route collectionRoute
	if json.Unmarshal(p.RouteSnapshot, &route) != nil {
		return route, false
	}
	profile, ok := profileFor(route.Provider)
	return route, ok && route.Version == capabilityVersion && route.Environment == "sandbox" &&
		route.Provider == p.Route && route.Capability == profile.capability &&
		route.Capability == (capability{p.Market, p.Currency, p.Method}) &&
		clientID.MatchString(route.AccountID) && route.LimitsRPS >= 1 && route.LimitsRPS <= 1000
}

// Event creation is part of the same transaction as the state change.
func (s *server) enqueueEvent(ctx context.Context, tx *sql.Tx, merchantID, subjectID string, event businessEvent) error {
	event.ID = "evt_" + subjectID + "_" + event.Status
	event.OccurredAt = time.Now().UTC()
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	destination := s.events.destinations[merchantID]
	state := "unconfigured"
	if destination != "" {
		state = "pending"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO collection_outbox (id, merchant_id, payment_id, payout_id, subject_id, payload, destination, state)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8)`, event.ID, merchantID, event.PaymentID, event.PayoutID, subjectID,
		payload, destination, state)
	return err
}

func (s *server) finishPayment(ctx context.Context, tx *sql.Tx, p payment, status, reference, failure string) (payment, error) {
	p, err := readPayment(tx.QueryRowContext(ctx, `
		UPDATE payments SET status=$2, paid_at=CASE WHEN $2='succeeded' THEN now() END,
			channel_reference=COALESCE(NULLIF($3,''), channel_reference),
			failure_code=NULLIF($4,''), channel_action=NULL, next_check_at=NULL
		WHERE id=$1 RETURNING `+paymentColumns, p.ID, status, reference, failure))
	if err != nil {
		return payment{}, err
	}
	return p, s.enqueueEvent(ctx, tx, p.MerchantID, p.ID, businessEvent{
		Type: "payment." + status, PaymentID: p.ID, Status: status, AmountMinor: p.AmountMinor, Currency: p.Currency,
	})
}

func (s *server) confirmPayment(ctx context.Context, id string) (payment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return payment{}, err
	}
	defer tx.Rollback()
	p, err := readPayment(tx.QueryRowContext(ctx, "SELECT "+paymentColumns+" FROM payments WHERE id=$1 FOR NO KEY UPDATE", id))
	if err != nil {
		return payment{}, err
	}
	if p.Status != "requires_action" || !time.Now().Before(p.ExpiresAt) {
		return payment{}, sql.ErrNoRows
	}
	p, err = s.finishPayment(ctx, tx, p, "succeeded", "", "")
	if err != nil {
		return payment{}, err
	}
	return p, tx.Commit()
}

func deriveEventKey(root []byte, purpose, scope string) []byte {
	mac := hmac.New(sha256.New, root)
	mac.Write([]byte("flowxpay-sandbox-v1\n" + purpose + "\n" + scope))
	return mac.Sum(nil)
}

func eventMAC(key []byte, timestamp, bodyScope string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("v1\n" + timestamp + "\n" + bodyScope + "\n"))
	mac.Write(body)
	return mac.Sum(nil)
}

func validEventSignature(h http.Header, key []byte, scope string, body []byte, now time.Time) bool {
	stamps, signatures := h.Values("FlowXPay-Timestamp"), h.Values("FlowXPay-Signature")
	if len(stamps) != 1 || len(signatures) != 1 || len(signatures[0]) != 64 {
		return false
	}
	seconds, err := strconv.ParseInt(stamps[0], 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != stamps[0] ||
		seconds < now.Unix()-300 || seconds > now.Unix()+300 {
		return false
	}
	signature, err := hex.DecodeString(signatures[0])
	return err == nil && hmac.Equal(signature, eventMAC(key, stamps[0], scope, body))
}

func respondEventStoreError(w http.ResponseWriter) {
	fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "The event or refund store is unavailable.")
}

var errSubjectNotFound = errors.New("subject not found")

func (s *server) registerEventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /sandbox/channel-callbacks/{account}", s.receiveCallback)
	mux.HandleFunc("POST /v1/payments/{id}/refunds", s.authorize(s.requireSignedWrite(s.createRefund)))
	mux.HandleFunc("GET /v1/payments/{id}/refunds", s.authorize(s.listRefunds))
	mux.HandleFunc("GET /v1/refunds/{id}", s.authorize(s.getRefund))
	mux.HandleFunc("GET /sandbox/workspace/chargebacks", s.authorizeMember(readInventory, s.listChargebacks))
	mux.HandleFunc("GET /sandbox/workspace/chargebacks/{account}/{id}", s.authorizeMember(readInventory, s.getChargeback))
	mux.HandleFunc("GET /v1/collection-exceptions", s.authorize(s.listCollectionExceptions))
}
