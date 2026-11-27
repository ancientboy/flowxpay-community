package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

type payout struct {
	ID string `json:"id"`
	payoutRequest
	RecipientType     string     `json:"recipient_type"`
	Status            string     `json:"status"`
	CreatedBy         string     `json:"created_by"`
	ReviewedBy        string     `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	ChannelReference  string     `json:"channel_reference,omitempty"`
	TransferReference string     `json:"transfer_reference,omitempty"`
	FailureCode       string     `json:"failure_code,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	merchantID        string
	requestKey        string
	checkAttempts     int
	Quote             *orderFee `json:"quote"`
	PlatformChannelID string    `json:"platform_channel_id,omitempty"`
}

type payoutRequest struct {
	Reference   string `json:"merchant_reference"`
	AccountID   string `json:"account_id"`
	RecipientID string `json:"recipient_id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}

const payoutColumns = `
	id, merchant_id, merchant_reference, account_id, recipient_id, recipient_type, currency, amount_minor,
	status, created_by, COALESCE(reviewed_by,''), reviewed_at, channel_reference, transfer_reference,
	failure_code, created_at, updated_at, check_attempts, request_key, pricing_snapshot->'quote', COALESCE(pricing_snapshot->>'channel_id','')`

func readPayout(row scanner) (payout, error) {
	var p payout
	var quote []byte
	err := row.Scan(&p.ID, &p.merchantID, &p.Reference, &p.AccountID, &p.RecipientID, &p.RecipientType,
		&p.Currency, &p.AmountMinor, &p.Status, &p.CreatedBy, &p.ReviewedBy, &p.ReviewedAt,
		&p.ChannelReference, &p.TransferReference, &p.FailureCode, &p.CreatedAt, &p.UpdatedAt, &p.checkAttempts, &p.requestKey, &quote, &p.PlatformChannelID)
	if err == nil && quote != nil {
		err = json.Unmarshal(quote, &p.Quote)
	}
	return p, err
}

type payoutRecipient struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Masked string `json:"masked_destination"`
}

func readPayoutRecipient(row scanner) (payoutRecipient, error) {
	var recipient payoutRecipient
	var suffix string
	err := row.Scan(&recipient.ID, &recipient.Type, &suffix)
	recipient.Masked = "•••• " + suffix
	return recipient, err
}

type payoutBalance struct {
	AccountID     string `json:"account_id"`
	SiteID        string `json:"site_id"`
	Provider      string `json:"provider"`
	Currency      string `json:"currency"`
	RecipientType string `json:"recipient_type"`
	Available     int64  `json:"available_minor"`
	Reserved      int64  `json:"reserved_minor"`
	MaxPayout     int64  `json:"max_payout_minor"`
	Enabled       bool   `json:"enabled"`
}

func readPayoutBalance(row scanner) (payoutBalance, error) {
	var balance payoutBalance
	err := row.Scan(&balance.AccountID, &balance.SiteID, &balance.Provider, &balance.Currency,
		&balance.RecipientType, &balance.Available, &balance.Reserved, &balance.MaxPayout, &balance.Enabled)
	return balance, err
}

func (s *server) registerPayoutRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/payout-accounts", s.authorizeMember(readInventory, listPayoutBalances))
	mux.HandleFunc("GET /v1/payout-recipients", s.authorizeMember(readInventory, listPayoutRecipients))
	mux.HandleFunc("POST /v1/payout-recipients", s.authorizeMember(draftPayouts, createPayoutRecipient))
	mux.HandleFunc("GET /v1/payouts", s.authorizeMember(readInventory, listPayouts))
	mux.HandleFunc("GET /v1/payouts/{id}", s.authorizeMember(readInventory, getPayout))
	mux.HandleFunc("POST /v1/payouts", s.authorizeMember(draftPayouts, createPayout))
	mux.HandleFunc("POST /v1/payouts/{id}/approve", s.authorizeMember(reviewPayouts, s.approvePayout))
	mux.HandleFunc("POST /v1/payouts/{id}/reject", s.authorizeMember(reviewPayouts, s.rejectPayout))
}

func payoutRequestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) == 1 && clientID.MatchString(key) {
		return key, true
	}
	fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Supply one valid Idempotency-Key.")
	return "", false
}

func listPayoutBalances(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, `
		SELECT b.account_id,c.site_id,c.provider,b.currency,b.recipient_type,
		       b.available_minor,b.reserved_minor,b.max_payout_minor,c.enabled AND COALESCE(pc.enabled,true)
		FROM sandbox_payout_accounts b JOIN sandbox_channel_accounts c ON c.id=b.account_id
		LEFT JOIN sandbox_platform_channels pc ON pc.id=c.platform_channel_id
		WHERE b.merchant_id=$1 ORDER BY b.currency,b.account_id`, readPayoutBalance)
}

func listPayoutRecipients(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, `
		SELECT id,recipient_type,test_suffix FROM sandbox_payout_recipients
		WHERE merchant_id=$1 ORDER BY created_at DESC,id DESC`, readPayoutRecipient)
}

var payoutTestSuffix = regexp.MustCompile(`^[0-9]{4}$`)

func createPayoutRecipient(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	key, ok := payoutRequestKey(w, r)
	if !ok {
		return 0, nil, nil
	}
	var input struct {
		Type       string `json:"type"`
		TestSuffix string `json:"test_suffix"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if !payoutTestSuffix.MatchString(input.TestSuffix) ||
		(input.Type != "bank_account" && input.Type != "pix" && input.Type != "upi") {
		fail(w, http.StatusBadRequest, "invalid_recipient", "Select a supported type and enter exactly four fictional digits.")
		return 0, nil, nil
	}
	ctx := r.Context()
	recipient, err := readPayoutRecipient(tx.QueryRowContext(ctx, `
		INSERT INTO sandbox_payout_recipients (id,merchant_id,request_key,recipient_type,test_suffix)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (merchant_id,request_key) DO NOTHING
		RETURNING id,recipient_type,test_suffix`, newResourceID("rcp_"), m.MerchantID, key, input.Type, input.TestSuffix))
	if !errors.Is(err, sql.ErrNoRows) {
		return http.StatusCreated, recipient, err
	}
	recipient, err = readPayoutRecipient(tx.QueryRowContext(ctx, `
		SELECT id,recipient_type,test_suffix FROM sandbox_payout_recipients WHERE merchant_id=$1 AND request_key=$2`,
		m.MerchantID, key))
	if err == nil && (recipient.Type != input.Type || recipient.Masked != "•••• "+input.TestSuffix) {
		return inventoryKeyConflict(w)
	}
	return http.StatusOK, recipient, err
}

func createPayout(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	key, ok := payoutRequestKey(w, r)
	if !ok {
		return 0, nil, nil
	}
	var input payoutRequest
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if !clientID.MatchString(input.Reference) || !clientID.MatchString(input.AccountID) ||
		!clientID.MatchString(input.RecipientID) || input.AmountMinor < 1 || input.AmountMinor > 999999999999 ||
		(input.Currency != "USD" && input.Currency != "BRL" && input.Currency != "INR") {
		fail(w, http.StatusBadRequest, "invalid_payout", "Supply a reference, payout account, recipient, currency and positive amount in minor units.")
		return 0, nil, nil
	}
	ctx := r.Context()
	replay := func() (int, any, error) {
		p, err := readPayout(tx.QueryRowContext(ctx, "SELECT "+payoutColumns+`
			FROM sandbox_payouts WHERE merchant_id=$1 AND (request_key=$2 OR merchant_reference=$3)
			ORDER BY (request_key=$2) DESC LIMIT 1`, m.MerchantID, key, input.Reference))
		if err != nil {
			return 0, nil, err
		}
		if p.requestKey != key || p.payoutRequest != input {
			return inventoryKeyConflict(w)
		}
		w.Header().Set("Location", "/v1/payouts/"+p.ID)
		return http.StatusOK, p, nil
	}
	if status, result, err := replay(); !errors.Is(err, sql.ErrNoRows) {
		return status, result, err
	}

	var provider, recipientType string
	var channel sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT c.provider,c.platform_channel_id,b.recipient_type
		FROM sandbox_payout_accounts b JOIN sandbox_channel_accounts c ON c.id=b.account_id AND c.merchant_id=b.merchant_id
		JOIN sandbox_payout_recipients r ON r.merchant_id=b.merchant_id AND r.recipient_type=b.recipient_type
		WHERE b.merchant_id=$1 AND b.account_id=$2 AND r.id=$3 AND b.currency=$4 AND c.enabled AND $5<=b.max_payout_minor
		FOR SHARE OF c`, m.MerchantID, input.AccountID, input.RecipientID, input.Currency, input.AmountMinor).
		Scan(&provider, &channel, &recipientType)
	if err == nil {
		err = lockEnabledPlatformChannel(ctx, tx, channel)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if status, result, readErr := replay(); !errors.Is(readErr, sql.ErrNoRows) {
			return status, result, readErr
		}
		fail(w, http.StatusUnprocessableEntity, "ineligible_payout", "The recipient, currency or amount is ineligible for this enabled payout account.")
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	pricing, err := snapshotPricing(ctx, tx, m.MerchantID, channel.String, provider, "payout", input.Currency, input.AmountMinor)
	if err != nil {
		return 0, nil, err
	}
	pricingJSON, err := json.Marshal(pricing)
	if err != nil {
		return 0, nil, err
	}
	for range idAttempts {
		id := newResourceID(payoutPrefix)
		p, err := readPayout(tx.QueryRowContext(ctx, `
			INSERT INTO sandbox_payouts (id,merchant_id,merchant_reference,request_key,account_id,recipient_id,recipient_type,
				currency,amount_minor,status,created_by,channel_reference,pricing_snapshot)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'draft',$10,$11,$12)
			ON CONFLICT DO NOTHING RETURNING `+payoutColumns,
			id, m.MerchantID, input.Reference, key, input.AccountID, input.RecipientID, recipientType, input.Currency, input.AmountMinor, m.ID, "sim_"+id, pricingJSON))
		if !errors.Is(err, sql.ErrNoRows) {
			if err == nil {
				w.Header().Set("Location", "/v1/payouts/"+p.ID)
			}
			return http.StatusCreated, p, err
		}
		if status, result, err := replay(); !errors.Is(err, sql.ErrNoRows) {
			return status, result, err
		}
	}
	return 0, nil, errIDAllocation
}

func listPayouts(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	query := r.URL.Query()
	ref, search := query.Get("merchant_reference"), query.Get("reference")
	if ref != "" && !clientID.MatchString(ref) {
		fail(w, http.StatusBadRequest, "invalid_merchant_reference", "Supply a valid merchant reference.")
		return 0, nil, nil
	}
	if search != "" && !clientID.MatchString(search) {
		fail(w, http.StatusBadRequest, "invalid_payout_reference", "Supply a valid payout ID or merchant reference.")
		return 0, nil, nil
	}
	return listScopedRows(r, tx, m.MerchantID, "SELECT "+payoutColumns+`
		FROM sandbox_payouts WHERE merchant_id=$1
		  AND ($2='' OR merchant_reference=$2)
		  AND ($3='' OR id=$3 OR merchant_reference=$3)
		ORDER BY created_at DESC,id DESC LIMIT 100`, readPayout, ref, search)
}

func getPayout(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	p, err := readPayout(tx.QueryRowContext(r.Context(), "SELECT "+payoutColumns+
		" FROM sandbox_payouts WHERE merchant_id=$1 AND id=$2", m.MerchantID, r.PathValue("id")))
	return http.StatusOK, p, err
}

func (s *server) approvePayout(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return s.reviewPayout(w, r, tx, m, true)
}

func (s *server) rejectPayout(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return s.reviewPayout(w, r, tx, m, false)
}

func (s *server) reviewPayout(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member, approve bool) (int, any, error) {
	ctx := r.Context()
	p, err := readPayout(tx.QueryRowContext(ctx, "SELECT "+payoutColumns+
		" FROM sandbox_payouts WHERE merchant_id=$1 AND id=$2 FOR NO KEY UPDATE", m.MerchantID, r.PathValue("id")))
	if err != nil {
		return 0, nil, err
	}
	if p.CreatedBy == m.ID {
		fail(w, http.StatusForbidden, "self_review", "A different admin must review this payout.")
		return 0, nil, nil
	}
	if p.Status != "draft" {
		if p.ReviewedBy == m.ID && (p.Status != "rejected") == approve {
			return http.StatusOK, p, nil
		}
		fail(w, http.StatusConflict, "payout_already_reviewed", "This payout already has a review decision.")
		return 0, nil, nil
	}
	status, failure := "rejected", "review_rejected"
	if approve {
		reserved, err := reservePayoutBalance(ctx, tx, p)
		if err != nil {
			return 0, nil, err
		}
		if !reserved {
			fail(w, http.StatusConflict, "payout_funds_unavailable", "The original payout account is disabled, over its limit or has insufficient available funds.")
			return 0, nil, nil
		}
		status, failure = "processing", ""
	}
	p, err = readPayout(tx.QueryRowContext(ctx, `
		UPDATE sandbox_payouts SET status=$2, reviewed_by=$3, reviewed_at=now(), updated_at=now(), failure_code=$4
		WHERE id=$1 RETURNING `+payoutColumns, p.ID, status, m.ID, failure))
	if err == nil && !approve {
		err = s.enqueuePayoutEvent(ctx, tx, p)
	}
	return http.StatusOK, p, err
}

func reservePayoutBalance(ctx context.Context, tx *sql.Tx, p payout) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `
		SELECT enabled FROM sandbox_channel_accounts WHERE merchant_id=$1 AND id=$2 FOR SHARE`,
		p.merchantID, p.AccountID).Scan(&enabled)
	if err != nil || !enabled {
		return false, err
	}
	if err := lockEnabledPlatformChannel(ctx, tx, sql.NullString{String: p.PlatformChannelID, Valid: p.PlatformChannelID != ""}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sandbox_payout_accounts SET available_minor=available_minor-$3, reserved_minor=reserved_minor+$3
		WHERE merchant_id=$1 AND account_id=$2 AND available_minor >= $3 AND max_payout_minor >= $3`,
		p.merchantID, p.AccountID, p.AmountMinor)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
