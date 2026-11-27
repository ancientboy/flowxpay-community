package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func (s *server) checkoutToken(id string) string {
	h := hmac.New(sha256.New, s.signing)
	_, _ = h.Write([]byte("flowxpay-sandbox-checkout:" + id))
	return id + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *server) readCheckout(w http.ResponseWriter, r *http.Request) (payment, bool) {
	token := r.PathValue("token")
	id, _, ok := strings.Cut(token, ".")
	if !ok || !validCheckoutPaymentID(id) ||
		!hmac.Equal([]byte(s.checkoutToken(id)), []byte(token)) {
		fail(w, http.StatusNotFound, "checkout_not_found", "Checkout session not found.")
		return payment{}, false
	}
	p, err := readPayment(s.db.QueryRowContext(r.Context(), "SELECT "+paymentColumns+
		" FROM payments WHERE id=$1", id))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "checkout_not_found", "Checkout session not found.")
		return payment{}, false
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to check this session.")
		return payment{}, false
	}
	if !time.Now().Before(p.ExpiresAt) {
		fail(w, http.StatusGone, "checkout_expired", "This checkout link expired.")
		return payment{}, false
	}
	return p, true
}

func (s *server) viewCheckout(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readCheckout(w, r)
	if ok {
		s.respondToCheckout(w, r, p)
	}
}

func (s *server) confirmCheckout(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Confirm bool `json:"confirm"`
	}
	if !parseJSON(w, r, &request) {
		return
	}
	if !request.Confirm {
		fail(w, http.StatusBadRequest, "invalid_confirmation", "Specify confirm=true for this sandbox simulation.")
		return
	}
	p, ok := s.readCheckout(w, r)
	if !ok {
		return
	}
	updated, err := s.confirmPayment(r.Context(), p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		p, ok = s.readCheckout(w, r)
		if !ok {
			return
		}
		if p.Status == "succeeded" {
			s.respondToCheckout(w, r, p)
			return
		}
		fail(w, http.StatusConflict, "checkout_not_actionable", "This checkout has no available payment action.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to confirm this session.")
		return
	}
	s.respondToCheckout(w, r, updated)
}

func (s *server) respondToCheckout(w http.ResponseWriter, r *http.Request, p payment) {
	var merchantName string
	if err := s.db.QueryRowContext(r.Context(), "SELECT name FROM merchants WHERE id=$1", p.MerchantID).Scan(&merchantName); err != nil {
		fail(w, http.StatusServiceUnavailable, "payment_store_unavailable", "Unable to check this session.")
		return
	}
	view := map[string]any{
		"merchant_name": merchantName, "reference": p.MerchantReference, "currency": p.Currency,
		"method": p.Method, "market": p.Market, "amount_minor": p.AmountMinor,
		"status": p.statusAt(time.Now()), "expires_at": p.ExpiresAt,
	}
	if p.Status == "requires_action" || p.Status == "pending_confirmation" && p.ChannelAction.String == "unsupported" {
		view["next_action"] = p.nextAction(s.checkoutToken(p.ID))
	}
	respond(w, http.StatusOK, view)
}

func (p payment) nextAction(token string) *action {
	switch p.ChannelAction.String {
	case "redirect":
		return &action{Type: "redirect", URL: "/checkout/" + token + "/pay"}
	case "display_qr":
		// The PNG encodes the same synthetic payload, with no payable Pix or UPI data.
		return &action{
			Type: "display_qr", QRPayload: "FLOWXPAY-SANDBOX:" + p.Market + ":" + p.ID,
			QRImageURL: "/sandbox/checkout/" + token + "/qr",
		}
	case "unsupported":
		return &action{Type: "unsupported"}
	}
	if len(p.RouteSnapshot) == 0 && p.Status == "requires_action" {
		return &action{Type: "redirect", URL: "/checkout/" + token}
	}
	return nil
}

func (s *server) checkoutQR(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readCheckout(w, r)
	if !ok {
		return
	}
	if p.Status != "requires_action" || p.ChannelAction.String != "display_qr" {
		fail(w, http.StatusNotFound, "checkout_action_not_found", "This checkout has no QR action.")
		return
	}
	png, err := qrcode.Encode(p.nextAction(r.PathValue("token")).QRPayload, qrcode.Medium, 256)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal_error", "Unable to render the sandbox QR.")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)

}
