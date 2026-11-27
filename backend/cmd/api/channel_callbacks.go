package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

type channelCallback struct {
	EventID           string `json:"event_id"`
	Kind              string `json:"kind"`
	ChargebackID      string `json:"chargeback_id,omitempty"`
	SubjectID         string `json:"subject_id"`
	Reference         string `json:"channel_reference"`
	Status            string `json:"status"`
	Currency          string `json:"currency"`
	Amount            int64  `json:"amount_minor"`
	TransferReference string `json:"transfer_reference,omitempty"`
}

func (e channelCallback) valid() bool {
	if e.Kind == "payout" && e.Status == "succeeded" {
		if !clientID.MatchString(e.TransferReference) {
			return false
		}
	} else if e.TransferReference != "" {
		return false
	}
	validStatus := terminal(e.Status) || e.Status == "pending"
	switch e.Kind {
	case "payment", "refund", "payout":
		if e.ChargebackID != "" {
			return false
		}
	case "chargeback":
		if !clientID.MatchString(e.ChargebackID) {
			return false
		}
		validStatus = e.Status == "opened" || e.Status == "won" || e.Status == "lost"
	default:
		return false
	}
	return clientID.MatchString(e.EventID) && clientID.MatchString(e.SubjectID) && clientID.MatchString(e.Reference) &&
		validStatus &&
		e.Amount > 0 && e.Amount <= 999999999999 && (e.Currency == "USD" || e.Currency == "BRL" || e.Currency == "INR")
}

func (s *server) receiveCallback(w http.ResponseWriter, r *http.Request) {
	account := r.PathValue("account")
	if len(s.events.callbackRoot) == 0 || !clientID.MatchString(account) {
		fail(w, http.StatusNotFound, "callback_not_available", "Callback endpoint unavailable.")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Send application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_body", "Callback body is unreadable or too large.")
		return
	}
	if key := deriveEventKey(s.events.callbackRoot, "callback", account); !validEventSignature(r.Header, key, account, body, time.Now()) {
		fail(w, http.StatusUnauthorized, "invalid_callback_signature", "Callback signature or timestamp is invalid.")
		return
	}
	var event channelCallback
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&event) != nil || decoder.Decode(new(any)) != io.EOF || !event.valid() {
		fail(w, http.StatusBadRequest, "invalid_callback", "Callback fields are invalid.")
		return
	}
	outcome, err := s.recordCallback(r.Context(), account, event, sha256.Sum256(body))
	switch {
	case errors.Is(err, errSubjectNotFound):
		fail(w, http.StatusNotFound, "callback_subject_not_found", "The callback does not match an original channel transaction.")
	case err != nil:
		respondEventStoreError(w)
	case outcome == "replay_conflict":
		fail(w, http.StatusConflict, "callback_event_conflict", "This event ID is already assigned to a different message.")
	default:
		respond(w, http.StatusOK, map[string]string{"outcome": outcome})
	}
}

func (s *server) recordCallback(ctx context.Context, account string, event channelCallback, digest [32]byte) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var paymentID, payoutID, merchantID, outcome string
	var apply func() error
	var chargebackPayment payment
	switch event.Kind {
	case "payment", "chargeback":
		p, err := readPayment(tx.QueryRowContext(ctx, "SELECT "+paymentColumns+
			" FROM payments WHERE id=$1 FOR NO KEY UPDATE", event.SubjectID))
		if errors.Is(err, sql.ErrNoRows) {
			return "", errSubjectNotFound
		}
		if err != nil {
			return "", err
		}
		route, valid := resolveOriginalRoute(p)
		reference := p.ChannelReference.String
		if reference == "" {
			reference = "sim_" + p.ID
		}
		if !valid || route.AccountID != account || p.Currency != event.Currency || reference != event.Reference {
			return "", errSubjectNotFound
		}
		paymentID, merchantID = p.ID, p.MerchantID
		if event.Kind == "chargeback" {
			if p.Status != "succeeded" || !p.PaidAt.Valid || p.ChannelReference.String != event.Reference || event.Amount > p.AmountMinor {
				return "", errSubjectNotFound
			}
			chargebackPayment, outcome = p, "applied"
			break
		}
		if p.AmountMinor != event.Amount {
			return "", errSubjectNotFound
		}
		outcome = terminalOutcome(p.Status, event.Status)
		apply = func() error {
			_, err := s.finishPayment(ctx, tx, p, event.Status, reference, callbackFailure(event.Status))
			return err
		}
	case "refund":
		f, err := readRefund(tx.QueryRowContext(ctx, "SELECT "+refundColumns+
			" FROM sandbox_refunds WHERE id=$1 FOR NO KEY UPDATE", event.SubjectID))
		if errors.Is(err, sql.ErrNoRows) {
			return "", errSubjectNotFound
		}
		if err != nil {
			return "", err
		}
		if f.Route.AccountID != account || f.Currency != event.Currency || f.AmountMinor != event.Amount || f.Reference != event.Reference {
			return "", errSubjectNotFound
		}
		paymentID, merchantID = f.PaymentID, f.MerchantID
		outcome = terminalOutcome(f.Status, event.Status)
		apply = func() error {
			_, err := s.finishRefund(ctx, tx, f, event.Status, callbackFailure(event.Status))
			return err
		}
	case "payout":
		p, err := readPayout(tx.QueryRowContext(ctx, "SELECT "+payoutColumns+
			" FROM sandbox_payouts WHERE id=$1 FOR NO KEY UPDATE", event.SubjectID))
		if errors.Is(err, sql.ErrNoRows) {
			return "", errSubjectNotFound
		}
		if err != nil {
			return "", err
		}
		if p.AccountID != account || p.Currency != event.Currency || p.AmountMinor != event.Amount ||
			p.ChannelReference != event.Reference || (p.Status != "pending_confirmation" && !terminal(p.Status)) {
			return "", errSubjectNotFound
		}
		payoutID, merchantID = p.ID, p.merchantID
		outcome = terminalOutcome(p.Status, event.Status)
		if p.Status == "succeeded" && event.Status == p.Status && event.TransferReference != p.TransferReference {
			outcome = "terminal_conflict"
		}
		apply = func() error {
			return s.finishPayout(ctx, tx, p, event.Status, event.TransferReference, callbackFailure(event.Status))
		}
	default:
		return "", errSubjectNotFound
	}
	tag, err := tx.ExecContext(ctx, `
		INSERT INTO collection_inbox (account_id,event_id,merchant_id,payment_id,payout_id,subject_id,body_hash,outcome)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8) ON CONFLICT DO NOTHING`,
		account, event.EventID, merchantID, paymentID, payoutID, event.SubjectID, digest[:], outcome)
	if err != nil {
		return "", err
	}
	count, err := tag.RowsAffected()
	if err != nil {
		return "", err
	}
	if count == 0 {
		var existing []byte
		if err := tx.QueryRowContext(ctx, "SELECT body_hash FROM collection_inbox WHERE account_id=$1 AND event_id=$2",
			account, event.EventID).Scan(&existing); err != nil {
			return "", err
		}
		if bytes.Equal(existing, digest[:]) {
			return "duplicate", nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE collection_inbox SET replay_conflict_at=COALESCE(replay_conflict_at,now())
			WHERE account_id=$1 AND event_id=$2`, account, event.EventID)
		if err != nil {
			return "", err
		}
		outcome = "replay_conflict"
	} else if event.Kind == "chargeback" {
		outcome, err = recordChargeback(ctx, tx, account, chargebackPayment, event)
		if err != nil {
			return "", err
		}
	} else if outcome == "applied" {
		if err := apply(); err != nil {
			return "", err
		}
	}
	return outcome, tx.Commit()
}

func callbackFailure(status string) string {
	if status == "failed" {
		return "channel_rejected"
	}
	return ""
}
