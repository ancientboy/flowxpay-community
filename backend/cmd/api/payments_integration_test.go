package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
)

func TestSandboxPaymentFlow(t *testing.T) {
	url := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("run scripts/local-sandbox.sh test to create an isolated PostgreSQL instance")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(12)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	app := &server{db: db, collectionTimeout: 500 * time.Millisecond, collection: collectionAdapterFunc(
		func(ctx context.Context, input collectionInput) (collectionResult, error) {
			calls.Add(1)
			switch input.Request.MerchantReference {
			case "SLOW":
				<-ctx.Done()
				return collectionResult{}, ctx.Err()
			case "IN-FLIGHT":
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
				}
			case "CHANNEL-429":
				return collectionResult{}, errChannelRateLimited
			case "REJECTED":
				return collectionResult{}, errChannelRejected
			}
			return sandboxCollection{}.Create(ctx, input)
		},
	), signing: []byte(strings.Repeat("s", 32)), merchants: [2]sandboxMerchant{
		{ID: "M10028", Name: "Cedar Commerce", Key: strings.Repeat("a", 32)},
		{ID: "M20029", Name: "Harbor Retail", Key: strings.Repeat("b", 32)},
	}}
	httpServer := httptest.NewServer(app.routes())
	t.Cleanup(httpServer.Close)
	a, b := app.merchants[0].Key, app.merchants[1].Key
	type result struct {
		status  int
		data    map[string]any
		headers http.Header
		err     error
	}
	call := func(method, path, key, idem, payload string) result {
		request, err := http.NewRequest(method, httpServer.URL+path, bytes.NewBufferString(payload))
		if err != nil {
			return result{err: err}
		}
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
		if idem != "" {
			request.Header.Set("Idempotency-Key", idem)
		}
		if payload != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		res, err := httpServer.Client().Do(request)
		if err != nil {
			return result{err: err}
		}
		defer res.Body.Close()
		var data map[string]any
		err = json.NewDecoder(res.Body).Decode(&data)
		return result{status: res.StatusCode, data: data, headers: res.Header, err: err}
	}
	expect := func(r result, status int) map[string]any {
		t.Helper()
		if r.err != nil || r.status != status {
			t.Fatalf("status %d, wanted %d, body %+v, error %v", r.status, status, r.data, r.err)
		}
		return r.data
	}
	expect(call("GET", "/v1/payments", "", "", ""), http.StatusUnauthorized)
	caps := expect(call("GET", "/v1/capabilities", a, "", ""), http.StatusOK)
	if caps["capability_version"] != capabilityVersion || !strings.Contains(fmt.Sprint(caps["sites"]), "default_M10028") ||
		strings.Contains(fmt.Sprint(caps["sites"]), "M20029") {
		t.Fatalf("unexpected matrix: %+v", caps)
	}
	body := `{"merchant_reference":"BR-DEMO-01","market":"BR","currency":"BRL","method":"pix","amount_minor":129000}`
	const concurrent = 40
	var group sync.WaitGroup
	results := make(chan result, concurrent)
	for range concurrent {
		group.Go(func() { results <- call("POST", "/v1/payments", a, "same-key", body) })
	}
	group.Wait()
	close(results)
	var id, checkout string
	created := 0
	replayed := 0
	for r := range results {
		switch r.status {
		case http.StatusCreated:
			created++
			checkout = fmt.Sprint(r.data["checkout_path"])
		case http.StatusOK:
			if r.headers.Get("Idempotency-Replayed") != "true" {
				t.Fatal("missing replay header")
			}
			replayed++
		default:
			t.Fatalf("concurrent create: %+v", r)
		}
		gotID := fmt.Sprint(r.data["id"])
		if id != "" && gotID != id {
			t.Fatal("concurrent requests created different payments")
		}
		id = gotID
	}
	if created != 1 || replayed != concurrent-1 || calls.Load() != 1 {
		t.Fatalf("created %d, replayed %d", created, replayed)
	}
	if !regexp.MustCompile(`^pay_[2-9A-HJ-NP-Z]{24}$`).MatchString(id) {
		t.Fatalf("unexpected collection order number %q", id)
	}
	expect(call("POST", "/v1/payments", a, "same-key", strings.Replace(body, "129000", "129001", 1)), http.StatusConflict)
	expect(call("POST", "/v1/payments", a, "second-key", body), http.StatusConflict)
	expect(call("GET", "/v1/payments/"+id, b, "", ""), http.StatusNotFound)
	expect(call("POST", "/v1/payments", b, "same-key", body), http.StatusCreated)
	otherMerchant := expect(call("GET", "/v1/payments", b, "", ""), http.StatusOK)
	if rows := otherMerchant["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] == id {
		t.Fatalf("cross-merchant list is not isolated: %+v", rows)
	}
	byReference := expect(call("GET", "/v1/payments?merchant_reference=BR-DEMO-01", a, "", ""), http.StatusOK)
	if rows := byReference["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != id {
		t.Fatalf("reference lookup returned an unexpected payment: %+v", rows)
	}

	expect(call("GET", "/sandbox"+checkout+"tampered", "", "", ""), http.StatusNotFound)
	req, err := http.NewRequest("GET", httpServer.URL+"/sandbox"+checkout+"/qr", nil)
	if err != nil {
		t.Fatal(err)
	}
	qr, err := httpServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	qr.Body.Close()
	if qr.StatusCode != 200 || qr.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("QR status %d, content type %s", qr.StatusCode, qr.Header.Get("Content-Type"))
	}
	view := expect(call("GET", "/sandbox"+checkout, "", "", ""), http.StatusOK)
	if view["next_action"].(map[string]any)["type"] != "display_qr" || view["status"] != "requires_action" || view["merchant_name"] != "Cedar Commerce" || view["currency"] != "BRL" || view["amount_minor"] != float64(129000) {
		t.Fatalf("incorrect checkout: %+v", view)
	}
	for _, confidential := range []string{"idempotency_key", "route", "merchant_id", "request_hash"} {
		if _, found := view[confidential]; found {
			t.Fatalf("public checkout exposed %s", confidential)
		}
	}
	const confirmBody = `{"confirm":true}`
	const confirmations = 16
	confirmed := make(chan result, confirmations)
	for range confirmations {
		group.Go(func() { confirmed <- call("POST", "/sandbox"+checkout+"/confirm", "", "", confirmBody) })
	}
	group.Wait()
	close(confirmed)
	for r := range confirmed {
		if data := expect(r, http.StatusOK); data["status"] != "succeeded" {
			t.Fatalf("confirmation state: %+v", data)
		}
	}
	paid := expect(call("GET", "/v1/payments/"+id, a, "", ""), http.StatusOK)
	if paid["status"] != "succeeded" || paid["paid_at"] == nil || paid["checkout_path"] != nil {
		t.Fatalf("unexpected final payment: %+v", paid)
	}

	card := `{"merchant_reference":"US-DEMO-01","market":"US","currency":"USD","method":"card","amount_minor":24900}`
	cardResult := expect(call("POST", "/v1/payments", a, "card-key", card), http.StatusCreated)
	cardPath := fmt.Sprint(cardResult["checkout_path"])
	if cardResult["checkout_url"] != cardPath || cardResult["next_action"].(map[string]any)["url"] != cardPath+"/pay" {
		t.Fatalf("incorrect card action: %+v", cardResult)
	}
	if _, err := db.Exec("UPDATE payments SET expires_at=now() - interval '1 second' WHERE id=$1", cardResult["id"]); err != nil {
		t.Fatal(err)
	}
	expect(call("GET", "/sandbox"+cardPath, "", "", ""), http.StatusGone)
	expect(call("POST", "/sandbox"+cardPath+"/confirm", "", "", confirmBody), http.StatusGone)
	if expired := expect(call("GET", "/v1/payments/"+fmt.Sprint(cardResult["id"]), a, "", ""), http.StatusOK); expired["status"] != "expired" {
		t.Fatalf("wrong expired state %+v", expired)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// A payment created before migration 005 has neither a route snapshot nor an action.
	const legacyID = "pay_0123456789abcdef0123456789abcdef"
	const legacyBody = `{"merchant_reference":"LEGACY","market":"BR","currency":"BRL","method":"pix","amount_minor":1200}`
	exec(`INSERT INTO payments (id, merchant_id, merchant_reference, idempotency_key, request_hash,
		market, currency, method, amount_minor, status, route)
		VALUES ($1,'M10028','LEGACY','legacy',$2,'BR','BRL','pix',1200,'requires_action','mock_pix')`,
		legacyID, fmt.Sprintf("%x", sha256.Sum256([]byte(legacyBody))))
	legacy := expect(call("POST", "/v1/payments", a, "legacy", legacyBody), 200)
	if legacy["id"] != legacyID || legacy["route_snapshot"] != nil || legacy["next_action"].(map[string]any)["type"] != "redirect" {
		t.Fatalf("legacy payment no longer replays: %+v", legacy)
	}
	expect(call("GET", "/v1/payments/"+legacyID, a, "", ""), http.StatusOK)
	expect(call("GET", "/v1/payments/"+legacyID, b, "", ""), http.StatusNotFound)
	// This signature was computed using the original HMAC format and fixture signing key.
	const legacyPath = "/sandbox/checkout/pay_0123456789abcdef0123456789abcdef.TKYmjQBJBw6hOHh4xr588ed3sttTKqAIJWFwmG07CT8"
	if data := expect(call("GET", legacyPath, "", "", ""), http.StatusOK); data["reference"] != "LEGACY" {
		t.Fatalf("legacy checkout resolved incorrectly: %+v", data)
	}
	expect(call("GET", legacyPath+"tampered", "", "", ""), http.StatusNotFound)
	expect(call("POST", legacyPath+"/confirm", "", "", confirmBody), http.StatusOK)
	t.Log("40 concurrent creates reserved one collection, and old/new IDs resolved with their signed checkout")
	exec(`INSERT INTO merchant_sites (id, merchant_id, name) VALUES
		('in-qr','M10028','India QR'), ('in-unknown','M10028','India unknown'), ('empty','M10028','Unbound site')`)
	exec(`INSERT INTO sandbox_channel_accounts (id, merchant_id, site_id, provider) VALUES
		('in-qr-account','M10028','in-qr','mock_in_qr'),
		('in-unknown-account','M10028','in-unknown','mock_in_unknown')`)
	makeBody := func(ref, site string) string {
		return fmt.Sprintf(`{"merchant_reference":%q,"site_id":%q,"market":"IN","currency":"INR","method":"gateway","amount_minor":24900}`, ref, site)
	}
	post := func(key, ref, site string) result {
		return call("POST", "/v1/payments", a, key, makeBody(ref, site))
	}
	codeIs := func(result result, status int, code string) {
		t.Helper()
		data := expect(result, status)
		if data["error"].(map[string]any)["code"] != code {
			t.Fatalf("expected %s, got %+v", code, data)
		}
	}
	codeIs(post("foreign", "FOREIGN", "default_M20029"), 404, "site_not_found")
	codeIs(post("empty", "EMPTY", "empty"), 400, "payment_method_unavailable")
	for _, variant := range []struct{ site, want string }{
		{"", "redirect"}, {"in-qr", "display_qr"}, {"in-unknown", "unsupported"},
	} {
		p := expect(post("india-"+variant.want, "INDIA-"+variant.want, variant.site), 201)
		if p["next_action"].(map[string]any)["type"] != variant.want || p["currency"] != "INR" {
			t.Fatalf("wrong India action: %+v", p)
		}
		path := fmt.Sprint(p["checkout_url"])
		public := expect(call("GET", "/sandbox"+path, "", "", ""), 200)
		if public["next_action"].(map[string]any)["type"] != variant.want || public["route_snapshot"] != nil {
			t.Fatalf("incorrect public checkout: %+v", public)
		}
		if variant.want == "unsupported" {
			if p["status"] != "pending_confirmation" || p["failure_code"] != "channel_action_unsupported" {
				t.Fatalf("unsupported action did not fail closed: %+v", p)
			}
			codeIs(call("POST", "/sandbox"+path+"/confirm", "", "", confirmBody), 409, "checkout_not_actionable")
		}
		paymentPath := "/v1/payments/" + fmt.Sprint(p["id"])
		expect(call("GET", paymentPath, b, "", ""), 404)
		if found := expect(call("GET", paymentPath, a, "", ""), 200); found["status"] != p["status"] {
			t.Fatalf("unified lookup returned %+v", found)
		}
	}

	// Turn off the original account. The immutable snapshot must remain the same on replay.
	first := expect(post("before-disable", "ORIGINAL-ROUTE", "in-qr"), 201)
	exec(`UPDATE sandbox_channel_accounts SET enabled=false WHERE id='in-qr-account'`)
	exec(`INSERT INTO sandbox_channel_accounts (id, merchant_id, site_id, provider)
		VALUES ('in-replacement','M10028','in-qr','mock_in_redirect')`)
	replay := expect(post("before-disable", "ORIGINAL-ROUTE", "in-qr"), 200)
	newPayment := expect(post("after-disable", "NEW-ROUTE", "in-qr"), 201)
	accountID := func(p map[string]any) any { return p["route_snapshot"].(map[string]any)["account_id"] }
	if replay["id"] != first["id"] || accountID(replay) != "in-qr-account" || accountID(newPayment) != "in-replacement" {
		t.Fatalf("unexpected route change: original=%+v replay=%+v new=%+v", first, replay, newPayment)
	}

	inFlight := make(chan result, 1)
	go func() { inFlight <- post("in-flight", "IN-FLIGHT", "") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("adapter did not start")
	}
	during := expect(post("in-flight", "IN-FLIGHT", ""), 200)
	close(release)
	finished := expect(<-inFlight, 201)
	if during["id"] != finished["id"] || during["status"] != "pending_confirmation" ||
		during["next_action"] != nil || finished["status"] != "requires_action" {
		t.Fatalf("unexpected in-flight replay: during=%+v finished=%+v", during, finished)
	}

	for _, fault := range []struct{ ref, status, code string }{
		{"SLOW", "pending_confirmation", "channel_confirmation_pending"},
		{"CHANNEL-429", "failed", "channel_rate_limited"},
		{"REJECTED", "failed", "channel_rejected"},
	} {
		before := calls.Load()
		original := expect(post(fault.ref, fault.ref, ""), 201)
		replayed := expect(post(fault.ref, fault.ref, ""), 200)
		if original["status"] != fault.status || original["failure_code"] != fault.code ||
			original["next_action"] != nil || original["checkout_url"] != nil ||
			replayed["id"] != original["id"] || accountID(replayed) != accountID(original) || calls.Load() != before+1 {
			t.Fatalf("unexpected fault result: %+v / %+v", original, replayed)
		}
	}

	// Put one fixture account into a deterministic saturated window.
	exec(`UPDATE sandbox_channel_accounts SET requests_per_second=1 WHERE id='in-replacement'`)
	exec(`UPDATE sandbox_channel_rate SET window_start=clock_timestamp()+interval '1 minute'
		WHERE account_id='in-replacement'`)
	before := calls.Load()
	throttled := post("budget", "BUDGET", "in-qr")
	codeIs(throttled, 429, "channel_rate_limited")
	if calls.Load() != before || throttled.headers.Get("Retry-After") != "1" {
		t.Fatal("local budget rejection dispatched a channel request or omitted Retry-After")
	}
	expect(call("POST", "/v1/payments", b, "budget", makeBody("BUDGET", "")), 201)
	exec(`UPDATE sandbox_channel_rate SET window_start=clock_timestamp()-interval '1 minute'
		WHERE account_id='in-replacement'`)
	expect(post("budget", "BUDGET", "in-qr"), 201)

}

type collectionAdapterFunc func(context.Context, collectionInput) (collectionResult, error)

func (fn collectionAdapterFunc) Create(ctx context.Context, input collectionInput) (collectionResult, error) {
	return fn(ctx, input)
}
