package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
)

func TestSandboxMerchantSignedWrites(t *testing.T) {
	url := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := loadMerchantRequestKeys(`{"M10028":{"fixture":"` + base64.RawURLEncoding.EncodeToString(public) + `"}}`)
	if err != nil {
		t.Fatal(err)
	}
	app := &server{
		db: db, collection: sandboxCollection{db: db}, signing: []byte(strings.Repeat("s", 32)), requestKeys: keys,
		merchants: [2]sandboxMerchant{{ID: "M10028", Key: strings.Repeat("a", 32)}, {ID: "M20029", Key: strings.Repeat("b", 32)}},
	}
	const collectionPath = "/v1/payments"
	const order = `{"market":"US","currency":"USD","method":"card","amount_minor":400,"merchant_reference":"SIGNED-TEST-01"}`
	baseTime := time.Now().Unix()
	call := func(path, signedPath, sent, signed, idem, nonce string, stamp int64, duplicate bool, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(sent))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Idempotency-Key", idem)
		if nonce != "" {
			nonce = base64.RawURLEncoding.EncodeToString([]byte("fixture-unique-nonce-" + nonce))
			timestamp := strconv.FormatInt(stamp, 10)
			sig := ed25519.Sign(private, merchantSigningInput("M10028", "fixture", "POST", signedPath, "application/json", idem, timestamp, nonce, []byte(signed)))
			req.Header.Set("FlowXPay-Key-ID", "fixture")
			req.Header.Set("FlowXPay-Request-Timestamp", timestamp)
			req.Header.Set("FlowXPay-Request-Nonce", nonce)
			req.Header.Set("FlowXPay-Request-Signature", base64.RawURLEncoding.EncodeToString(sig))
			if duplicate {
				req.Header.Add("FlowXPay-Request-Nonce", nonce)
			}
		}
		res := httptest.NewRecorder()
		app.routes().ServeHTTP(res, req)
		return res
	}
	expect := func(res *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if res.Code != status || code != "" && !strings.Contains(res.Body.String(), `"`+code+`"`) {
			t.Fatalf("expected %d %s, got %d %s", status, code, res.Code, res.Body.String())
		}
	}
	bearer := app.merchants[0].Key
	for _, merchant := range app.merchants {
		req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
		req.Header.Set("Authorization", "Bearer "+merchant.Key)
		res := httptest.NewRecorder()
		app.routes().ServeHTTP(res, req)
		want := strconv.FormatBool(merchant.ID == "M10028")
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"request_signing_required":`+want) {
			t.Fatalf("expected capabilities to report signature requirement %s", want)
		}
	}
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "", baseTime, false, bearer), 401, "invalid_request_signature")
	expect(call(collectionPath, collectionPath, order+" ", order, "signed-key-01", "tampered", baseTime, false, bearer), 401, "invalid_request_signature")
	expect(call(collectionPath+"?x=1", collectionPath+"?x=0", order, order, "signed-key-01", "query", baseTime, false, bearer), 401, "invalid_request_signature")
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "expired", baseTime-301, false, bearer), 401, "invalid_request_signature")
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "dupe", baseTime, true, bearer), 401, "invalid_request_signature")
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "first", baseTime, false, bearer), 201, "")
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "first", baseTime, false, bearer), 409, "request_nonce_reused")
	expect(call(collectionPath, collectionPath, order, order, "signed-key-01", "retry", baseTime, false, bearer), 200, "")
	if count := db.QueryRow("SELECT count(*) FROM payments WHERE merchant_reference = 'SIGNED-TEST-01'"); count.Err() != nil {
		t.Fatal(count.Err())
	} else {
		var rows int
		if err := count.Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("expected one payment, got %d (%v)", rows, err)
		}
	}
	const refundPath = "/v1/payments/NO-SUCH-ORDER/refunds"
	const refund = `{"amount_minor":100}`
	expect(call(refundPath, refundPath, refund, refund, "signed-refund-01", "", baseTime, false, bearer), 401, "invalid_request_signature")
	expect(call(refundPath, collectionPath, refund, refund, "signed-refund-01", "cross-path", baseTime, false, bearer), 401, "invalid_request_signature")
	expect(call(refundPath, refundPath, refund, refund, "signed-refund-01", "refund", baseTime, false, bearer), 404, "payment_not_found")
}
