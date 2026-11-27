package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"time"
)

type paymentRequest struct {
	MerchantReference string `json:"merchant_reference"`
	Market            string `json:"market"`
	Currency          string `json:"currency"`
	Method            string `json:"method"`
	AmountMinor       int64  `json:"amount_minor"`
	SiteID            string `json:"site_id,omitempty"`
}

type payment struct {
	ID, MerchantID string
	paymentRequest
	Status, Route, RequestHash                   string
	CreatedAt, ExpiresAt                         time.Time
	PaidAt                                       sql.NullTime
	ChannelAction, FailureCode, ChannelReference sql.NullString
	RouteSnapshot                                json.RawMessage
	Quote                                        *orderFee
}

type paymentView struct {
	ID string `json:"id"`
	paymentRequest
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	PaidAt        *time.Time      `json:"paid_at,omitempty"`
	CheckoutPath  string          `json:"checkout_path,omitempty"`
	NextAction    *action         `json:"next_action,omitempty"`
	CheckoutURL   string          `json:"checkout_url,omitempty"`
	FailureCode   string          `json:"failure_code,omitempty"`
	RouteSnapshot json.RawMessage `json:"route_snapshot,omitempty"`
	Quote         *orderFee       `json:"quote"`
}

type action struct {
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	QRPayload  string `json:"qr_payload,omitempty"`
	QRImageURL string `json:"qr_image_url,omitempty"`
}

var clientID = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,80}$`)

const paymentColumns = `id, merchant_id, merchant_reference, market, currency, method,
amount_minor, status, route, request_hash, created_at, expires_at, paid_at,
channel_action, failure_code, channel_reference, route_snapshot, COALESCE(route_snapshot->>'site_id',''), pricing_snapshot->'quote'`

type scanner interface {
	Scan(...any) error
}

func readPayment(row scanner) (payment, error) {
	var p payment
	var snapshot, quote []byte
	err := row.Scan(&p.ID, &p.MerchantID, &p.MerchantReference,
		&p.Market, &p.Currency, &p.Method, &p.AmountMinor, &p.Status,
		&p.Route, &p.RequestHash, &p.CreatedAt, &p.ExpiresAt, &p.PaidAt,
		&p.ChannelAction, &p.FailureCode, &p.ChannelReference, &snapshot, &p.SiteID, &quote)
	p.RouteSnapshot = snapshot
	if err == nil && quote != nil {
		err = json.Unmarshal(quote, &p.Quote)
	}
	return p, err
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func parseJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return parseJSONUpTo(w, r, dst, 4096)
}

func parseJSONUpTo(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Send application/json.")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		fail(w, http.StatusBadRequest, "invalid_body", "Send a valid JSON object matching the API contract.")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_body", "Send exactly one JSON object.")
		return false
	}
	return true
}

func (s *server) createPayment(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	key := r.Header.Get("Idempotency-Key")
	if !clientID.MatchString(key) || len(r.Header.Values("Idempotency-Key")) != 1 {
		fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Supply one valid Idempotency-Key.")
		return
	}
	var request paymentRequest
	if !parseJSON(w, r, &request) {
		return
	}
	if !clientID.MatchString(request.MerchantReference) || request.AmountMinor < 1 || request.AmountMinor > 999999999999 {
		fail(w, http.StatusBadRequest, "invalid_payment", "Supply a valid merchant reference and a positive amount in minor units.")
		return
	}
	if request.SiteID != "" && !clientID.MatchString(request.SiteID) {
		fail(w, http.StatusBadRequest, "invalid_site_id", "Supply a valid site ID.")
		return
	}
	canonical, _ := json.Marshal(request)
	sum := sha256.Sum256(canonical)
	hash := hex.EncodeToString(sum[:])
	p, route, created, err := s.reservePayment(r, merchant.ID, key, hash, request)
	switch {
	case errors.Is(err, errSiteUnavailable):
		fail(w, http.StatusNotFound, "site_not_found", "Site not found.")
	case errors.Is(err, errMethodUnavailable):
		fail(w, http.StatusBadRequest, "payment_method_unavailable", "The market, currency and method combination is not enabled for this site.")
	case errors.Is(err, errChannelRateLimited):
		w.Header().Set("Retry-After", "1")
		fail(w, http.StatusTooManyRequests, "channel_rate_limited", "Retry with the original Idempotency-Key.")
	case errors.Is(err, sql.ErrNoRows):
		fail(w, http.StatusConflict, "merchant_reference_exists", "A payment with that merchant reference already exists.")
	case err != nil:
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Retry with the original Idempotency-Key.")
	case p.RequestHash != hash:
		fail(w, http.StatusConflict, "idempotency_conflict", "The key is already assigned to a different request.")
	case created:
		if updated, err := s.dispatchCollection(r.Context(), p, route); err == nil {
			p = updated
		}
		w.Header().Set("Location", "/v1/payments/"+p.ID)
		respond(w, http.StatusCreated, s.view(p, true))
	default:
		w.Header().Set("Idempotency-Replayed", "true")
		respond(w, http.StatusOK, s.view(p, true))
	}
}

func (s *server) reservePayment(r *http.Request, merchantID, key, hash string, request paymentRequest) (payment, collectionRoute, bool, error) {
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return payment{}, collectionRoute{}, false, err
	}
	defer tx.Rollback()
	lookup := func() (payment, error) {
		return readPayment(tx.QueryRowContext(ctx, "SELECT "+paymentColumns+
			" FROM payments WHERE merchant_id=$1 AND idempotency_key=$2", merchantID, key))
	}
	p, err := lookup()
	if !errors.Is(err, sql.ErrNoRows) {
		return p, collectionRoute{}, false, err
	}
	route, err := selectCollectionRoute(ctx, tx, merchantID, request)
	if err != nil {
		if errors.Is(err, errSiteUnavailable) || errors.Is(err, errMethodUnavailable) {
			// Account changes must not prevent concurrent replays of an already reserved payment.
			if original, readErr := lookup(); !errors.Is(readErr, sql.ErrNoRows) {
				return original, route, false, readErr
			}
		}
		return payment{}, route, false, err
	}
	snapshot, err := json.Marshal(route)
	if err != nil {
		return payment{}, route, false, err
	}
	pricing, err := snapshotPricing(ctx, tx, merchantID, route.ChannelID, route.Provider, "collection", request.Currency, request.AmountMinor)
	if err != nil {
		return payment{}, route, false, err
	}
	pricingJSON, err := json.Marshal(pricing)
	if err != nil {
		return payment{}, route, false, err
	}
	for range idAttempts {
		id := newResourceID(paymentPrefix)
		p, err = readPayment(tx.QueryRowContext(ctx, `
			INSERT INTO payments (id, merchant_id, merchant_reference, idempotency_key, request_hash,
				market, currency, method, amount_minor, status, route, route_snapshot, pricing_snapshot, failure_code, next_check_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending_confirmation',$10,$11,$12,'channel_confirmation_pending',now()+interval '5 seconds')
			ON CONFLICT DO NOTHING RETURNING `+paymentColumns,
			id, merchantID, request.MerchantReference, key, hash,
			request.Market, request.Currency, request.Method, request.AmountMinor, route.Provider, snapshot, pricingJSON))
		if !errors.Is(err, sql.ErrNoRows) {
			break
		}
		var collision bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM payments WHERE id=$1)", id).Scan(&collision); err != nil {
			return payment{}, route, false, err
		}
		if !collision {
			p, err = lookup()
			return p, route, false, err
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return payment{}, route, false, errIDAllocation
	}
	if err != nil {
		return payment{}, route, false, err
	}
	if err := takeCollectionSlot(ctx, tx, route); err != nil {
		return payment{}, route, false, err
	}
	return p, route, true, tx.Commit()
}

func (s *server) getPayment(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	p, err := readPayment(s.db.QueryRowContext(r.Context(), "SELECT "+paymentColumns+
		" FROM payments WHERE id=$1 AND merchant_id=$2", r.PathValue("id"), merchant.ID))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "payment_not_found", "Payment not found.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to read the payment.")
		return
	}
	respond(w, http.StatusOK, s.view(p, true))
}

func (s *server) listPayments(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	reference := r.URL.Query().Get("merchant_reference")
	if reference != "" && !clientID.MatchString(reference) {
		fail(w, http.StatusBadRequest, "invalid_merchant_reference", "Supply a valid merchant reference.")
		return
	}
	query := "SELECT " + paymentColumns + " FROM payments WHERE merchant_id=$1"
	args := []any{merchant.ID}
	if reference != "" {
		query += " AND merchant_reference=$2"
		args = append(args, reference)
	}
	rows, err := s.db.QueryContext(r.Context(), query+" ORDER BY created_at DESC, id DESC LIMIT 100", args...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to list payments.")
		return
	}
	defer rows.Close()
	payments := make([]paymentView, 0)
	for rows.Next() {
		p, err := readPayment(rows)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to list payments.")
			return
		}
		payments = append(payments, s.view(p, false))
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to list payments.")
		return
	}
	respond(w, http.StatusOK, map[string]any{"data": payments})
}

func (s *server) view(p payment, includeCheckout bool) paymentView {
	status := p.statusAt(time.Now())
	v := paymentView{ID: p.ID, paymentRequest: p.paymentRequest, Status: status,
		CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, FailureCode: p.FailureCode.String, RouteSnapshot: p.RouteSnapshot, Quote: p.Quote}
	if p.PaidAt.Valid {
		v.PaidAt = &p.PaidAt.Time
	}
	if includeCheckout && (status == "requires_action" || status == "pending_confirmation" && p.ChannelAction.String == "unsupported" && time.Now().Before(p.ExpiresAt)) {
		token := s.checkoutToken(p.ID)
		v.CheckoutPath = "/checkout/" + token
		v.CheckoutURL = v.CheckoutPath
		v.NextAction = p.nextAction(token)
	}
	return v
}

func (p payment) statusAt(now time.Time) string {
	if p.Status == "requires_action" && !now.Before(p.ExpiresAt) {
		return "expired"
	}
	return p.Status
}
