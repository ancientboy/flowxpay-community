package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flowxpay.local/platform/internal/requestsign"
)

type merchantRequestKeys map[string]map[string]ed25519.PublicKey

const merchantSignatureWindow = 300
const merchantSignedBodyLimit = 4096

func loadMerchantRequestKeys(value string) (merchantRequestKeys, error) {
	if value == "" {
		return nil, nil
	}
	var input map[string]map[string]string
	invalid := errors.New("invalid sandbox merchant request public-key configuration")
	if err := json.Unmarshal([]byte(value), &input); err != nil || len(input) == 0 {
		return nil, invalid
	}
	keys := make(merchantRequestKeys, len(input))
	registered := make(map[string]bool)
	for merchant, entries := range input {
		if !clientID.MatchString(merchant) || len(entries) == 0 {
			return nil, invalid
		}
		keys[merchant] = make(map[string]ed25519.PublicKey, len(entries))
		for id, encoded := range entries {
			key, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
			if !clientID.MatchString(id) || err != nil || len(key) != ed25519.PublicKeySize ||
				base64.RawURLEncoding.EncodeToString(key) != encoded || registered[encoded] {
				return nil, invalid
			}
			keys[merchant][id] = ed25519.PublicKey(key)
			registered[encoded] = true
		}
	}
	return keys, nil
}

func singleRequestHeader(r *http.Request, name string) string {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func merchantSigningInput(merchant, keyID, method, target, contentType, idempotencyKey, timestamp, nonce string, body []byte) []byte {
	return requestsign.Message("sandbox", merchant, keyID, method, target, contentType, idempotencyKey, timestamp, nonce, body)
}

func (s *server) requireSignedWrite(next merchantHandler) merchantHandler {
	return func(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
		keys := s.requestKeys[merchant.ID]
		if len(keys) == 0 {
			next(w, r, merchant)
			return
		}

		keyID := singleRequestHeader(r, "FlowXPay-Key-ID")
		stamp := singleRequestHeader(r, "FlowXPay-Request-Timestamp")
		nonce := singleRequestHeader(r, "FlowXPay-Request-Nonce")
		encoded := singleRequestHeader(r, "FlowXPay-Request-Signature")
		idempotencyKey := singleRequestHeader(r, "Idempotency-Key")
		contentType := singleRequestHeader(r, "Content-Type")
		seconds, timeErr := strconv.ParseInt(stamp, 10, 64)
		signature, signErr := base64.RawURLEncoding.Strict().DecodeString(encoded)
		nonceBytes, nonceErr := base64.RawURLEncoding.Strict().DecodeString(nonce)
		now := time.Now().Unix()
		key := keys[keyID]
		if len(key) != ed25519.PublicKeySize || timeErr != nil || strconv.FormatInt(seconds, 10) != stamp ||
			seconds < now-merchantSignatureWindow || seconds > now+merchantSignatureWindow ||
			nonceErr != nil || len(nonceBytes) < 16 || len(nonceBytes) > 48 ||
			base64.RawURLEncoding.EncodeToString(nonceBytes) != nonce || !clientID.MatchString(idempotencyKey) ||
			!strings.EqualFold(contentType, "application/json") || len(r.Header.Values("Content-Encoding")) != 0 ||
			signErr != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != encoded {
			fail(w, http.StatusUnauthorized, "invalid_request_signature", "Supply a valid signed request.")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, merchantSignedBodyLimit))
		r.Body.Close()
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_body", "Send a valid JSON object matching the API contract.")
			return
		}
		signed := merchantSigningInput(merchant.ID, keyID, r.Method, r.URL.RequestURI(), contentType, idempotencyKey, stamp, nonce, body)
		if !ed25519.Verify(key, signed, signature) {
			fail(w, http.StatusUnauthorized, "invalid_request_signature", "Supply a valid signed request.")
			return
		}

		// A new timestamp and nonce are required even when retrying the original idempotency key.
		result, err := s.db.ExecContext(r.Context(), `
			INSERT INTO merchant_request_nonces(merchant_id, nonce, expires_at)
			VALUES ($1, $2, now() + interval '11 minutes')
			ON CONFLICT (merchant_id, nonce) DO NOTHING`, merchant.ID, nonce)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "signature_store_unavailable", "Retry with a fresh signature and the original Idempotency-Key.")
			return
		}
		written, err := result.RowsAffected()
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "signature_store_unavailable", "Retry with a fresh signature and the original Idempotency-Key.")
			return
		}
		if written == 0 {
			fail(w, http.StatusConflict, "request_nonce_reused", "Sign again with a fresh nonce and the original Idempotency-Key.")
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r, merchant)
	}
}

func (s *server) pruneMerchantRequestNonces() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := s.db.ExecContext(ctx, `DELETE FROM merchant_request_nonces WHERE expires_at <= now()`)
		cancel()
		if err != nil {
			log.Print("sandbox could not prune expired request nonces")
		}
	}
}
