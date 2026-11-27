package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func sandboxTestSignature(root []byte, purpose, scope, timestamp string, body []byte) string {
	derive := hmac.New(sha256.New, root)
	fmt.Fprintf(derive, "flowxpay-sandbox-v1\n%s\n%s", purpose, scope)
	mac := hmac.New(sha256.New, derive.Sum(nil))
	fmt.Fprintf(mac, "v1\n%s\n%s\n", timestamp, scope)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

type recoveryProbe struct {
	sandboxChannelRecovery
	refundCalls atomic.Int32
	lookups     []channelLookup
}

func (p *recoveryProbe) Refund(ctx context.Context, refund sandboxRefund) error {
	p.refundCalls.Add(1)
	switch refund.AmountMinor {
	case 60:
		if err := p.sandboxChannelRecovery.Refund(ctx, refund); err != nil {
			return err
		}
		return context.DeadlineExceeded
	case 10:
		return errChannelRejected
	case 15:
		return context.DeadlineExceeded
	default:
		return p.sandboxChannelRecovery.Refund(ctx, refund)
	}
}

func (p *recoveryProbe) Lookup(ctx context.Context, tx *sql.Tx, lookup channelLookup) (channelObservation, error) {
	p.lookups = append(p.lookups, lookup)
	return p.sandboxChannelRecovery.Lookup(ctx, tx, lookup)
}

func TestSandboxCollectionEventsAndRefunds(t *testing.T) {
	dsn := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL via scripts/local-sandbox.sh test")
	}
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	fixtureDB := stdlib.OpenDB(*cfg)
	defer fixtureDB.Close()
	schema := "events_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := fixtureDB.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	openDB := func() *sql.DB {
		db := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { db.Close() })
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := openDB()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const account, site = "evt_card", "evt_site"
	exec("INSERT INTO merchant_sites (id,merchant_id,name) VALUES ($1,'M10028','Event tests')", site)
	exec("INSERT INTO sandbox_channel_accounts (id,merchant_id,site_id,provider) VALUES ($1,'M10028',$2,'mock_card')", account, site)
	var webhookCalls atomic.Int32
	webhookRoot, callbackRoot := bytes.Repeat([]byte("w"), 32), bytes.Repeat([]byte("c"), 32)
	webhookEvents := make(chan businessEvent, 32)
	merchantSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		var event businessEvent
		stamp := r.Header.Get("FlowXPay-Timestamp")
		if err != nil || json.Unmarshal(body, &event) != nil || stamp == "" ||
			r.Header.Get("FlowXPay-Event-ID") != event.ID || r.URL.Path != "/merchant-hook" ||
			r.Header.Get("FlowXPay-Signature") != sandboxTestSignature(webhookRoot, "webhook", "M10028", stamp, body) {
			t.Error("invalid merchant webhook request")
			w.WriteHeader(400)
			return
		}
		webhookEvents <- event
		if webhookCalls.Add(1) == 1 || event.Type == "refund.failed" {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(merchantSink.Close)
	config := eventConfig{callbackRoot: callbackRoot, webhookRoot: webhookRoot,
		destinations: map[string]string{"M10028": merchantSink.URL + "/merchant-hook"}}
	var paymentCalls atomic.Int32
	makeApp := func(db *sql.DB) (*server, *recoveryProbe) {
		probe := &recoveryProbe{sandboxChannelRecovery: sandboxChannelRecovery{db}}
		return &server{
			db: db, events: config, recovery: probe, signing: bytes.Repeat([]byte("s"), 32),
			merchants: [2]sandboxMerchant{{"M10028", "A", strings.Repeat("a", 32)}, {"M20029", "B", strings.Repeat("b", 32)}},
			collection: collectionAdapterFunc(func(ctx context.Context, input collectionInput) (collectionResult, error) {
				paymentCalls.Add(1)
				result, err := (sandboxCollection{db: db}).Create(ctx, input)
				if err == nil && input.Request.MerchantReference == "EVT-UNKNOWN" {
					return collectionResult{}, context.DeadlineExceeded
				}
				return result, err
			}),
		}, probe
	}
	app, probe := makeApp(db)
	httpServer := httptest.NewServer(app.routes())
	t.Cleanup(httpServer.Close)
	key, foreignKey := app.merchants[0].Key, app.merchants[1].Key
	type response struct {
		status int
		result map[string]any
		err    error
		header http.Header
	}
	call := func(method, path, authorization, idem, body string, headers map[string]string) response {
		req, err := http.NewRequestWithContext(ctx, method, httpServer.URL+path, strings.NewReader(body))
		if err != nil {
			return response{err: err}
		}
		if authorization != "" {
			req.Header.Set("Authorization", "Bearer "+authorization)
		}
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		res, err := httpServer.Client().Do(req)
		if err != nil {
			return response{err: err}
		}
		defer res.Body.Close()
		var result map[string]any
		err = json.NewDecoder(res.Body).Decode(&result)
		return response{res.StatusCode, result, err, res.Header}
	}
	expect := func(res response, code int, field, value string) map[string]any {
		t.Helper()
		if res.err != nil || res.status != code || field != "" && res.result[field] != value {
			t.Fatalf("wanted %d %s=%q, got %d %v (%v)", code, field, value, res.status, res.result, res.err)
		}
		return res.result
	}
	create := func(reference string) response {
		return call("POST", "/v1/payments", key, reference, fmt.Sprintf(
			`{"merchant_reference":%q,"site_id":%q,"market":"US","currency":"USD","method":"card","amount_minor":100}`,
			reference, site), nil)
	}
	payment := expect(create("EVT-FIRST"), 201, "status", "requires_action")
	paymentID := payment["id"].(string)
	path := "/v1/payments/" + paymentID
	refundPath := path + "/refunds"
	callback := func(account, signingAccount, stamp string, event channelCallback, tamper bool) response {
		body, _ := json.Marshal(event)
		sig := sandboxTestSignature(callbackRoot, "callback", signingAccount, stamp, body)
		if tamper {
			body = append(body, ' ')
		}
		return call("POST", "/sandbox/channel-callbacks/"+account, "", "", string(body), map[string]string{
			"FlowXPay-Timestamp": stamp, "FlowXPay-Signature": sig,
		})
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	event := channelCallback{EventID: "evt-first", Kind: "payment", SubjectID: paymentID, Reference: "sim_" + paymentID, Status: "succeeded", Currency: "USD", Amount: 100}
	expect(callback(account, account, stamp, event, true), 401, "", "")
	expect(callback(account, account, strconv.FormatInt(time.Now().Unix()-301, 10), event, false), 401, "", "")
	expect(callback("foreign", account, stamp, event, false), 401, "", "")
	expect(callback("foreign", "foreign", stamp, event, false), 404, "", "")
	changed := event
	changed.Amount = 101
	expect(callback(account, account, stamp, changed, false), 404, "", "")
	changed = event
	changed.Reference = "sim_wrong"
	expect(callback(account, account, stamp, changed, false), 404, "", "")
	// Prove a late success still records a single terminal event.
	exec("UPDATE payments SET expires_at=now()-interval '1 minute' WHERE id=$1", paymentID)
	expect(call("GET", path, key, "", "", nil), 200, "status", "expired")
	var group sync.WaitGroup
	callbacks := make(chan response, 12)
	for range cap(callbacks) {
		group.Go(func() { callbacks <- callback(account, account, stamp, event, false) })
	}
	group.Wait()
	close(callbacks)
	var applied int
	for res := range callbacks {
		if res.result["outcome"] == "applied" {
			applied++
		} else {
			expect(res, 200, "outcome", "duplicate")
		}
	}
	if applied != 1 {
		t.Fatalf("applied %d callbacks", applied)
	}
	event.Status = "failed"
	expect(callback(account, account, stamp, event, false), 409, "", "")
	event.EventID = "evt-conflict"
	expect(callback(account, account, stamp, event, false), 200, "outcome", "terminal_conflict")
	event.EventID, event.Status = "evt-late-pending", "pending"
	expect(callback(account, account, stamp, event, false), 200, "outcome", "ignored")
	expect(call("GET", path, key, "", "", nil), 200, "status", "succeeded")
	expect(call("GET", path, foreignKey, "", "", nil), 404, "", "")

	// An outbox retry after an unavailable receiver retains the payload and ID.
	if err := app.deliverWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	var outboxState string
	var attempts int
	eventID := "evt_" + paymentID + "_succeeded"
	assertWebhook := func(want string, count int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, "SELECT state, attempts FROM collection_outbox WHERE id=$1", eventID).
			Scan(&outboxState, &attempts); err != nil || outboxState != want || attempts != count {
			t.Fatalf("outbox: %s/%d, error %v", outboxState, attempts, err)
		}
	}
	assertWebhook("pending", 1)
	unknownPayment := expect(create("EVT-UNKNOWN"), 201, "status", "pending_confirmation")["id"].(string)

	// Removing the account from new-order routing does not move old refunds or lookups.
	exec("UPDATE sandbox_channel_accounts SET enabled=false WHERE id=$1", account)
	expect(create("EVT-NEW-BLOCKED"), 400, "", "")
	refund := func(keyValue, idem string, amount int) response {
		return call("POST", refundPath, keyValue, idem, fmt.Sprintf(`{"amount_minor":%d}`, amount), nil)
	}
	expect(refund(foreignKey, "evt-other", 10), 404, "", "")
	results := make(chan response, 16)
	for range cap(results) {
		group.Go(func() { results <- refund(key, "evt-unknown-refund", 60) })
	}
	group.Wait()
	close(results)
	var refundID string
	var created int
	for res := range results {
		switch res.status {
		case 201:
			created++
		case 200:
			if res.header.Get("Idempotency-Replayed") != "true" {
				t.Fatal("missing refund replay header")
			}
		default:
			t.Fatalf("unexpected concurrent refund: %+v", res)
		}
		result := expect(res, res.status, "status", "pending_confirmation")
		if refundID != "" && refundID != result["id"] {
			t.Fatal("multiple refund IDs for one request")
		}
		refundID = result["id"].(string)
	}
	if created != 1 || probe.refundCalls.Load() != 1 {
		t.Fatal("refund dispatched more than once")
	}
	expect(refund(key, "evt-unknown-refund", 61), 409, "", "")
	expect(refund(key, "evt-too-much", 41), 409, "", "")
	failedRefund := expect(refund(key, "evt-rejected", 10), 201, "status", "failed")["id"].(string)
	pendingRefund := expect(refund(key, "evt-pending", 15), 201, "status", "pending_confirmation")["id"].(string)
	expect(call("GET", "/v1/refunds/"+refundID, foreignKey, "", "", nil), 404, "", "")
	oldProbe := probe

	// Simulate process replacement and make persisted schedules due without waiting for wall-clock backoff.
	httpServer.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openDB()
	app, probe = makeApp(db)
	httpServer = httptest.NewServer(app.routes())
	t.Cleanup(httpServer.Close)
	exec("UPDATE collection_outbox SET next_attempt_at=now()-interval '1 hour' WHERE id=$1", eventID)
	exec("UPDATE payments SET next_check_at=now()-interval '1 hour' WHERE id=$1", unknownPayment)
	exec("UPDATE sandbox_refunds SET next_check_at=now()-interval '1 hour' WHERE id=$1", refundID)
	for _, work := range []func(context.Context) error{app.reconcilePayment, app.reconcileRefund, app.deliverWebhook} {
		if err := work(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertWebhook("delivered", 2)
	first, second := <-webhookEvents, <-webhookEvents
	if first != second || second.ID != eventID || second.Type != "payment.succeeded" {
		t.Fatalf("webhook was changed during retry: %v %v", first, second)
	}
	if len(probe.lookups) != 2 || probe.lookups[0].SubjectID != unknownPayment || probe.lookups[1].SubjectID != refundID ||
		probe.lookups[0].Route.AccountID != account || probe.lookups[1].Route.AccountID != account ||
		paymentCalls.Load() != 2 || probe.refundCalls.Load() != 0 || oldProbe.refundCalls.Load() != 3 {
		t.Fatalf("incorrect recovery: %+v", probe.lookups)
	}
	expect(call("GET", "/v1/payments/"+unknownPayment, key, "", "", nil), 200, "status", "requires_action")
	expect(call("GET", "/v1/refunds/"+refundID, key, "", "", nil), 200, "status", "succeeded")
	expect(refund(key, "evt-unknown-refund", 60), 200, "status", "succeeded")

	refundEvent := channelCallback{EventID: "evt-refund-success", Kind: "refund", SubjectID: pendingRefund, Reference: "sim_" + pendingRefund, Status: "succeeded", Currency: "USD", Amount: 15}
	expect(callback(account, account, stamp, refundEvent, false), 200, "outcome", "applied")
	refundEvent.EventID, refundEvent.Status = "evt-refund-stale", "failed"
	expect(callback(account, account, stamp, refundEvent, false), 200, "outcome", "terminal_conflict")
	expect(call("GET", "/v1/refunds/"+pendingRefund, key, "", "", nil), 200, "status", "succeeded")

	// Different idempotency keys compete for the final balance under a payment row lock.
	results = make(chan response, 10)
	for i := range cap(results) {
		group.Go(func() { results <- refund(key, fmt.Sprintf("evt-balance-%d", i), 25) })
	}
	group.Wait()
	close(results)
	created = 0
	for res := range results {
		if res.status == 201 {
			expect(res, 201, "status", "succeeded")
			created++
		} else {
			expect(res, 409, "", "")
		}
	}
	if created != 1 || probe.refundCalls.Load() != 1 {
		t.Fatal("concurrent requests exceeded final balance")
	}
	items := expect(call("GET", refundPath, key, "", "", nil), 200, "", "")["data"].([]any)
	if len(items) != 4 {
		t.Fatalf("incorrect refund item count: %d", len(items))
	}

	// Channel limits are shared by both resources, including when the original route is disabled.
	exec("UPDATE payments SET route_snapshot=jsonb_set(route_snapshot,'{requests_per_second}','1') WHERE id=$1", unknownPayment)
	exec(`INSERT INTO sandbox_channel_rate VALUES ($1,date_trunc('second',now())+interval '1 minute',60)
		ON CONFLICT (account_id) DO UPDATE SET window_start=EXCLUDED.window_start,attempts=EXCLUDED.attempts`, account)
	expect(call("POST", strings.Replace(payment["checkout_path"].(string), "/checkout/", "/sandbox/checkout/", 1)+"/confirm",
		"", "", `{"confirm":true}`, nil), 410, "", "")
	recovered := expect(call("GET", "/v1/payments/"+unknownPayment, key, "", "", nil), 200, "", "")
	checkoutPath := strings.Replace(recovered["checkout_path"].(string), "/checkout/", "/sandbox/checkout/", 1)
	expect(call("POST", checkoutPath+"/confirm", "", "", `{"confirm":true}`, nil), 200, "status", "succeeded")
	limitedPath := "/v1/payments/" + unknownPayment + "/refunds"
	res := call("POST", limitedPath, key, "evt-limited", `{"amount_minor":1}`, nil)
	expect(res, 429, "", "")
	if res.header.Get("Retry-After") != "1" {
		t.Fatal("missing rate limit retry window")
	}
	exec("UPDATE sandbox_channel_rate SET window_start=now()-interval '1 minute' WHERE account_id=$1", account)
	expect(call("POST", limitedPath, key, "evt-limited", `{"amount_minor":1}`, nil), 201, "status", "succeeded")

	failedEventID := "evt_" + failedRefund + "_failed"
	for range maxWebhookAttempts {
		exec("UPDATE collection_outbox SET next_attempt_at=now()-interval '1 hour' WHERE id=$1", failedEventID)
		if err := app.deliverWebhook(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var failedState string
	var failedAttempts int
	if err := db.QueryRowContext(ctx, "SELECT state, attempts FROM collection_outbox WHERE id=$1", failedEventID).
		Scan(&failedState, &failedAttempts); err != nil || failedState != "failed" || failedAttempts != 5 {
		t.Fatalf("failed destination: %s/%d (%v)", failedState, failedAttempts, err)
	}
	found := expect(call("GET", "/v1/collection-exceptions", key, "", "", nil), 200, "", "")
	for _, kind := range []string{"replay_conflict", "terminal_conflict", "webhook_failed"} {
		if !strings.Contains(fmt.Sprint(found), kind) {
			t.Fatalf("missing %s from %v", kind, found)
		}
	}
	foreign := expect(call("GET", "/v1/collection-exceptions", foreignKey, "", "", nil), 200, "", "")
	if len(foreign["data"].([]any)) != 0 {
		t.Fatalf("cross-merchant exception leak: %v", foreign)
	}
}

func TestSandboxWebhookBoundary(t *testing.T) {
	for _, destination := range []string{
		"https://127.0.0.1:1234/hook", "http://localhost:1234/hook", "http://10.1.2.3:1234/hook",
		"http://169.254.169.254:80/hook", "http://127.0.0.1/hook", "http://127.0.0.1:99999/hook",
		"http://127.0.0.1:1234/hook#fragment", "http://user@127.0.0.1:1234/hook",
		"http://127.0.0.1:1234/hook?next=1", "http://[::ffff:127.0.0.1]:1234/hook",
	} {
		if localWebhookURL(destination) {
			t.Errorf("accepted %s", destination)
		}
	}
	for _, destination := range []string{"http://127.0.0.1:1234/hook", "http://[::1]:1234/hook"} {
		if !localWebhookURL(destination) {
			t.Errorf("rejected %s", destination)
		}
	}
	var redirectFollowed atomic.Bool
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/follow" {
			redirectFollowed.Store(true)
			w.WriteHeader(204)
			return
		}
		http.Redirect(w, r, "/follow", 307)
	}))
	defer redirect.Close()
	app := server{events: eventConfig{webhookRoot: bytes.Repeat([]byte("w"), 32)}}
	if app.sendWebhook(t.Context(), redirect.URL+"/start", "M10028", "evt", []byte("{}")) || redirectFollowed.Load() {
		t.Fatal("webhook followed a redirect")
	}
	conn, err := webhookClient.Transport.(*http.Transport).DialContext(t.Context(), "tcp", "192.0.2.1:80")
	if conn != nil {
		conn.Close()
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outbound IP was not rejected immediately: %v", err)
	}
}
