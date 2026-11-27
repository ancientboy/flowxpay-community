package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

func recordChargeback(ctx context.Context, tx *sql.Tx, account string, p payment, event channelCallback) (string, error) {
	// The payment is locked by the callback transaction. The case key also
	// serializes attempts to use the same account-scoped ID on different orders.
	var inserted bool
	err := tx.QueryRowContext(ctx, `
		INSERT INTO sandbox_chargebacks (account_id,id,merchant_id,payment_id,status,currency,amount_minor)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING RETURNING true`,
		account, event.ChargebackID, p.MerchantID, p.ID, event.Status, event.Currency, event.Amount).Scan(&inserted)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	outcome := "applied"
	if !inserted {
		var paymentID, merchantID, status, currency string
		var amount int64
		err = tx.QueryRowContext(ctx, `
			SELECT payment_id,merchant_id,status,currency,amount_minor
			FROM sandbox_chargebacks WHERE account_id=$1 AND id=$2 FOR UPDATE`, account, event.ChargebackID).
			Scan(&paymentID, &merchantID, &status, &currency, &amount)
		if err != nil {
			return "", err
		}
		if paymentID != p.ID || merchantID != p.MerchantID || currency != event.Currency || amount != event.Amount {
			return "", errSubjectNotFound
		}
		switch {
		case status == event.Status || event.Status == "opened":
			outcome = "ignored"
		case status != "opened":
			outcome = "terminal_conflict"
		default:
			_, err = tx.ExecContext(ctx, `
				UPDATE sandbox_chargebacks SET status=$3,updated_at=clock_timestamp()
				WHERE account_id=$1 AND id=$2`, account, event.ChargebackID, event.Status)
		}
		if err != nil {
			return "", err
		}
	}
	if outcome != "applied" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE collection_inbox SET outcome=$3 WHERE account_id=$1 AND event_id=$2`,
			account, event.EventID, outcome); err != nil {
			return "", err
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sandbox_chargeback_events (account_id,chargeback_id,event_id,status) VALUES ($1,$2,$3,$4)`,
		account, event.ChargebackID, event.EventID, event.Status)
	return outcome, err
}

type chargeback struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	PaymentID         string    `json:"payment_id"`
	MerchantReference string    `json:"merchant_reference"`
	Status            string    `json:"status"`
	Currency          string    `json:"currency"`
	Amount            int64     `json:"amount_minor"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const chargebackColumns = `c.id,c.account_id,c.payment_id,p.merchant_reference,c.status,c.currency,
	c.amount_minor,c.created_at,c.updated_at`

func (c *chargeback) scanFields() []any {
	return []any{&c.ID, &c.AccountID, &c.PaymentID, &c.MerchantReference, &c.Status, &c.Currency,
		&c.Amount, &c.CreatedAt, &c.UpdatedAt}
}

func (s *server) listChargebacks(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	rows, err := tx.QueryContext(r.Context(), `
		SELECT `+chargebackColumns+` FROM sandbox_chargebacks c
		JOIN payments p ON p.id=c.payment_id AND p.merchant_id=c.merchant_id
		WHERE c.merchant_id=$1 ORDER BY c.updated_at DESC,c.account_id,c.id LIMIT 100`, m.MerchantID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []chargeback{}
	for rows.Next() {
		var c chargeback
		if err := rows.Scan(c.scanFields()...); err != nil {
			return 0, nil, err
		}
		items = append(items, c)
	}
	return http.StatusOK, items, rows.Err()
}

type chargebackDetail struct {
	chargeback
	Payment struct {
		Status    string     `json:"status"`
		Amount    int64      `json:"amount_minor"`
		PaidAt    *time.Time `json:"paid_at"`
		Reference string     `json:"channel_reference"`
	} `json:"payment"`
	History json.RawMessage `json:"history"`
}

func (s *server) getChargeback(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var result chargebackDetail
	err := tx.QueryRowContext(r.Context(), `
		SELECT `+chargebackColumns+`,p.status,p.amount_minor,p.paid_at,p.channel_reference,
		(SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'event_id',e.event_id,'status',e.status,'outcome',i.outcome,
			'received_at',i.received_at,'replay_conflict_at',i.replay_conflict_at)
			ORDER BY e.sequence), '[]'::jsonb)
			FROM sandbox_chargeback_events e JOIN collection_inbox i USING (account_id,event_id)
			WHERE e.account_id=c.account_id AND e.chargeback_id=c.id)
		FROM sandbox_chargebacks c JOIN payments p ON p.id=c.payment_id AND p.merchant_id=c.merchant_id
		WHERE c.merchant_id=$1 AND c.account_id=$2 AND c.id=$3`,
		m.MerchantID, r.PathValue("account"), r.PathValue("id")).Scan(append(result.scanFields(),
		&result.Payment.Status, &result.Payment.Amount, &result.Payment.PaidAt, &result.Payment.Reference, &result.History)...)
	return http.StatusOK, result, err
}
