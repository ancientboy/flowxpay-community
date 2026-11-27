package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
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

func TestSandboxPayouts(t *testing.T) {
	dsn := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL via scripts/local-sandbox.sh test")
	}
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if action := os.Getenv("FLOWXPAY_PAYOUT_CHILD_ACTION"); action != "" {
		schema := os.Getenv("FLOWXPAY_PAYOUT_CHILD_SCHEMA")
		destination := os.Getenv("FLOWXPAY_PAYOUT_CHILD_WEBHOOK")
		if !strings.HasPrefix(schema, "payouts_") || !clientID.MatchString(schema) || !localWebhookURL(destination) {
			t.Fatal("invalid payout child fixtures")
		}
		cfg.RuntimeParams["search_path"] = schema
		db := stdlib.OpenDB(*cfg)
		defer db.Close()
		child := &server{db: db, events: eventConfig{destinations: map[string]string{"M10028": destination}}}
		var work func(context.Context) error
		switch action {
		case "dispatch":
			work = child.dispatchPayout
		case "reconcile":
			work = child.reconcilePayout
		default:
			t.Fatal("unexpected payout child action")
		}
		if err := work(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}

	schema := "payouts_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	bootstrap := stdlib.OpenDB(*cfg)
	defer bootstrap.Close()
	if _, err := bootstrap.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	openDB := func() *sql.DB {
		t.Helper()
		db := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { db.Close() })
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := openDB()
	runSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	session := func(id, merchant, role string, expires time.Time) string {
		t.Helper()
		runSQL("INSERT INTO merchant_members (id,merchant_id,name,role) VALUES ($1,$2,$1,$3)", id, merchant, role)
		token, digest := newAuthToken("fxs_")
		runSQL("INSERT INTO merchant_sessions (token_hash,member_id,expires_at) VALUES ($1,$2,$3)", digest[:], id, expires)
		return token
	}
	validUntil := time.Now().Add(time.Hour)
	op := session("payout-operator", "M10028", "operator", validUntil)
	admin := session("payout-admin", "M10028", "admin", validUntil)
	reviewer := session("payout-reviewer", "M10028", "admin", validUntil)
	viewer := session("payout-viewer", "M10028", "viewer", validUntil)
	other := session("payout-other", "M20029", "admin", validUntil)
	stale := session("payout-stale", "M10028", "admin", time.Now().Add(-time.Minute))
	revoked := session("payout-revoked", "M10028", "admin", validUntil)
	runSQL("UPDATE merchant_sessions SET revoked_at=now() WHERE member_id='payout-revoked'")

	const usd = "default_M10028_mock_card"
	const brl = "default_M10028_mock_pix"
	const inr = "default_M10028_mock_in_redirect"
	const foreign = "default_M20029_mock_card"
	runSQL("UPDATE sandbox_payout_accounts SET available_minor=100,max_payout_minor=100,simulation='disconnect' WHERE account_id=$1", usd)
	runSQL("UPDATE sandbox_payout_accounts SET available_minor=100,max_payout_minor=100,simulation='failure' WHERE account_id=$1", brl)
	runSQL("UPDATE sandbox_payout_accounts SET simulation='unresolved' WHERE account_id=$1", inr)
	runSQL("INSERT INTO sandbox_channel_accounts (id,merchant_id,site_id,provider) VALUES ('payout-unprovisioned','M10028','default_M10028','mock_card')")

	root := bytes.Repeat([]byte("p"), 32)
	signing := bytes.Repeat([]byte("s"), 32)
	type delivery struct {
		id, body string
	}
	deliveries := make(chan delivery, 16)
	var webhookCalls atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		id, stamp := r.Header.Get("FlowXPay-Event-ID"), r.Header.Get("FlowXPay-Timestamp")
		if err != nil || id == "" || r.Header.Get("FlowXPay-Signature") != sandboxTestSignature(signing, "webhook", "M10028", stamp, body) {
			t.Error("invalid payout webhook")
		}
		deliveries <- delivery{id, string(body)}
		if webhookCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	config := eventConfig{callbackRoot: root, webhookRoot: signing, destinations: map[string]string{"M10028": sink.URL + "/payout-hook"}}
	app := &server{db: db, events: config}
	secondDB := openDB()
	second := &server{db: secondDB, events: config}
	runWorkerProcess := func(action string) {
		t.Helper()
		bin, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		childContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(childContext, bin, "-test.run=^TestSandboxPayouts$")
		cmd.Env = append(os.Environ(), "FLOWXPAY_PAYOUT_CHILD_ACTION="+action,
			"FLOWXPAY_PAYOUT_CHILD_SCHEMA="+schema, "FLOWXPAY_PAYOUT_CHILD_WEBHOOK="+sink.URL+"/payout-hook")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("payout worker process %s: %v\n%s", action, err, output)
		}
	}
	firstRoutes, secondRoutes := app.routes(), second.routes()
	call := func(handler http.Handler, method, path, token, key, body string, headers http.Header) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		for name, values := range headers {
			r.Header[name] = values
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, code int, contains string) {
		t.Helper()
		if w.Code != code || !strings.Contains(w.Body.String(), contains) {
			t.Fatalf("want HTTP %d with %q, got %d %s", code, contains, w.Code, w.Body)
		}
	}
	payoutFrom := func(w *httptest.ResponseRecorder, code int) payout {
		t.Helper()
		expect(w, code, "")
		var p payout
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.ID == "" {
			t.Fatalf("invalid payout response: %s", w.Body)
		}
		return p
	}
	recipient := func(token, key, kind string) string {
		t.Helper()
		w := call(firstRoutes, "POST", "/v1/payout-recipients", token, key,
			fmt.Sprintf(`{"type":%q,"test_suffix":"0427"}`, kind), nil)
		expect(w, 201, "•••• 0427")
		var value payoutRecipient
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.ID == "" || strings.Contains(w.Body.String(), "test_suffix") {
			t.Fatalf("unmasked or invalid recipient response: %s", w.Body)
		}
		return value.ID
	}
	get := func(token, id string) *httptest.ResponseRecorder {
		return call(firstRoutes, "GET", "/v1/payouts/"+id, token, "", "", nil)
	}
	review := func(handler http.Handler, token, id, action string) *httptest.ResponseRecorder {
		return call(handler, "POST", "/v1/payouts/"+id+"/"+action, token, "", "", nil)
	}
	draft := func(token, key, ref, account, recipient, currency string, amount int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payoutRequest{ref, account, recipient, currency, amount})
		return call(firstRoutes, "POST", "/v1/payouts", token, key, string(body), nil)
	}
	balance := func(account string, available, reserved int64) {
		t.Helper()
		var gotAvailable, gotReserved int64
		err := db.QueryRowContext(ctx, "SELECT available_minor,reserved_minor FROM sandbox_payout_accounts WHERE account_id=$1",
			account).Scan(&gotAvailable, &gotReserved)
		if err != nil || gotAvailable != available || gotReserved != reserved {
			t.Fatalf("%s balance got (%d,%d,%v), want (%d,%d)", account, gotAvailable, gotReserved, err, available, reserved)
		}
	}

	t.Log("recipient minimization, role and session checks, eligibility and draft idempotency")
	for _, token := range []string{"", "not-a-member-key", stale, revoked} {
		expect(call(firstRoutes, "GET", "/v1/payout-accounts", token, "", "", nil), 401, "")
	}
	expect(call(firstRoutes, "POST", "/v1/payout-recipients", viewer, "viewer", "{}", nil), 403, "")
	expect(call(firstRoutes, "POST", "/v1/payout-recipients", op, "raw",
		`{"type":"bank_account","test_suffix":"1234","account_number":"123456789"}`, nil), 400, "")
	bank := recipient(op, "bank", "bank_account")
	pix := recipient(op, "pix", "pix")
	upi := recipient(op, "upi", "upi")
	otherBank := recipient(other, "bank", "bank_account")
	expect(call(secondRoutes, "POST", "/v1/payout-recipients", op, "bank", `{"type":"bank_account","test_suffix":"0427"}`, nil), 200, bank)
	expect(call(secondRoutes, "POST", "/v1/payout-recipients", op, "bank", `{"type":"bank_account","test_suffix":"0428"}`, nil), 409, "")
	expect(call(firstRoutes, "GET", "/v1/payout-recipients", other, "", "", nil), 200, otherBank)
	expect(call(firstRoutes, "GET", "/v1/payout-accounts", viewer, "", "", nil), 200, usd)
	expect(draft(viewer, "viewer", "viewer", usd, bank, "USD", 60), 403, "")
	expect(draft(op, "limit", "limit", usd, bank, "USD", 101), 422, "")
	expect(draft(op, "type", "type", usd, pix, "USD", 20), 422, "")
	expect(draft(op, "currency", "currency", usd, bank, "BRL", 20), 422, "")
	expect(draft(op, "foreign", "foreign", foreign, otherBank, "USD", 20), 422, "")
	expect(draft(op, "foreign-rcp", "foreign-rcp", usd, otherBank, "USD", 20), 422, "")
	expect(draft(op, "unprovisioned", "unprovisioned", "payout-unprovisioned", bank, "USD", 20), 422, "")
	p1 := payoutFrom(draft(admin, "stable-1", "one", usd, bank, "USD", 60), 201)
	p2 := payoutFrom(draft(op, "stable-2", "two", usd, bank, "USD", 60), 201)
	if !regexp.MustCompile(`^po_[2-9A-HJ-NP-Z]{24}$`).MatchString(p1.ID) {
		t.Fatalf("unexpected payout order number %q", p1.ID)
	}
	const legacyPayout = "po_ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	runSQL(`INSERT INTO sandbox_payouts (id,merchant_id,merchant_reference,request_key,account_id,recipient_id,
		recipient_type,currency,amount_minor,status,created_by,channel_reference)
		SELECT $1,merchant_id,'Legacy-Payout','legacy-order-key',account_id,recipient_id,recipient_type,currency,
			amount_minor,status,created_by,'sim_' || $1 FROM sandbox_payouts WHERE id=$2`, legacyPayout, p1.ID)
	for _, filter := range []struct{ query, want string }{
		{"reference=" + p1.ID, p1.ID},
		{"reference=one", p1.ID},
		{"merchant_reference=one", p1.ID},
		{"reference=" + legacyPayout, legacyPayout},
		{"reference=Legacy-Payout", legacyPayout},
		{"reference=" + p1.ID + "&merchant_reference=two", ""},
		{"reference=" + strings.ToLower(p1.ID), ""},
		{"reference=" + p1.ID[:8], ""},
		{"merchant_reference=" + p1.ID, ""},
	} {
		response := call(firstRoutes, "GET", "/v1/payouts?"+filter.query, admin, "", "", nil)
		expect(response, 200, "")
		var rows []payout
		if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if filter.want == "" && len(rows) != 0 || filter.want != "" && (len(rows) != 1 || rows[0].ID != filter.want) {
			t.Fatalf("wrong exact match for %q: %+v", filter.query, rows)
		}
		expect(call(firstRoutes, "GET", "/v1/payouts?"+filter.query, other, "", "", nil), 200, "[]")
	}
	expect(get(admin, legacyPayout), 200, legacyPayout)
	expect(get(other, legacyPayout), 404, "")
	const concurrentDrafts = 12
	var draftGroup sync.WaitGroup
	drafts := make(chan *httptest.ResponseRecorder, concurrentDrafts)
	for i := range concurrentDrafts {
		draftGroup.Go(func() {
			drafts <- call([]http.Handler{firstRoutes, secondRoutes}[i%2], "POST", "/v1/payouts", admin,
				"concurrent-draft", fmt.Sprintf(`{"merchant_reference":"Concurrent","account_id":%q,"recipient_id":%q,"currency":"USD","amount_minor":1}`, usd, bank), nil)
		})
	}
	draftGroup.Wait()
	close(drafts)
	var draftID string
	createdDrafts := 0
	for response := range drafts {
		if response.Code != 201 && response.Code != 200 {
			t.Fatalf("concurrent payout draft: %d %s", response.Code, response.Body)
		}
		if response.Code == 201 {
			createdDrafts++
		}
		p := payoutFrom(response, response.Code)
		if draftID != "" && draftID != p.ID {
			t.Fatal("concurrent requests created different payouts")
		}
		draftID = p.ID
	}
	if createdDrafts != 1 {
		t.Fatalf("created %d concurrent payout drafts", createdDrafts)
	}
	t.Log("12 concurrent creates across two API instances reserved one draft; exact old/new lookup stayed tenant-scoped")
	if actual := payoutFrom(draft(admin, "stable-1", "one", usd, bank, "USD", 60), 200); actual.ID != p1.ID {
		t.Fatal("same key returned a different payout")
	}
	expect(draft(admin, "stable-1", "one", usd, bank, "USD", 61), 409, "")
	expect(draft(admin, "different-key", "one", usd, bank, "USD", 60), 409, "")
	expect(review(firstRoutes, admin, p1.ID, "approve"), 403, "self_review")
	expect(review(firstRoutes, op, p1.ID, "approve"), 403, "")
	expect(review(firstRoutes, viewer, p1.ID, "reject"), 403, "")
	expect(review(firstRoutes, other, p1.ID, "approve"), 404, "")
	expect(get(other, p1.ID), 404, "")
	expect(call(firstRoutes, "GET", "/v1/payouts?merchant_reference=one", other, "", "", nil), 200, "[]")
	balance(usd, 100, 0)

	t.Log("two API instances approve competing drafts on the same balance")
	start := make(chan struct{})
	results := make(chan struct {
		p payout
		w *httptest.ResponseRecorder
	}, 2)
	var approvals sync.WaitGroup
	for i, p := range []payout{p1, p2} {
		approvals.Add(1)
		go func() {
			defer approvals.Done()
			<-start
			results <- struct {
				p payout
				w *httptest.ResponseRecorder
			}{p, review([]http.Handler{firstRoutes, secondRoutes}[i], reviewer, p.ID, "approve")}
		}()
	}
	close(start)
	approvals.Wait()
	close(results)
	var winner, loser payout
	for item := range results {
		switch item.w.Code {
		case 200:
			winner = payoutFrom(item.w, 200)
		case 409:
			expect(item.w, 409, "payout_funds_unavailable")
			loser = item.p
		default:
			t.Fatalf("unexpected approval: %d %s", item.w.Code, item.w.Body)
		}
	}
	if winner.Status != "processing" || loser.ID == "" {
		t.Fatal("expected exactly one successful reservation")
	}
	balance(usd, 40, 60)
	expect(review(secondRoutes, reviewer, winner.ID, "approve"), 200, "processing")
	if winner.CreatedBy == "payout-admin" {
		expect(review(secondRoutes, admin, winner.ID, "approve"), 403, "self_review")
	} else {
		expect(review(secondRoutes, admin, winner.ID, "approve"), 409, "payout_already_reviewed")
	}
	expect(get(viewer, loser.ID), 200, "draft")

	t.Log("durable mock acceptance followed by lost response and a replacement worker")
	runWorkerProcess("dispatch")
	if err := second.dispatchPayout(ctx); err != nil {
		t.Fatal(err)
	}
	expect(get(viewer, winner.ID), 200, "pending_confirmation")
	balance(usd, 40, 60)
	if err := second.reconcilePayout(ctx); err != nil {
		t.Fatal(err)
	}
	expect(get(viewer, winner.ID), 200, "pending_confirmation")
	if err := secondDB.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := &server{db: openDB(), events: config}
	due := func(id string) {
		runSQL("UPDATE sandbox_payouts SET next_check_at=now()-interval '1 second' WHERE id=$1", id)
	}
	due(winner.ID)
	if err := replacement.dispatchPayout(ctx); err != nil {
		t.Fatal(err)
	}
	runWorkerProcess("reconcile")
	winner = payoutFrom(get(viewer, winner.ID), 200)
	if winner.Status != "succeeded" || winner.TransferReference != "utr_"+winner.ID {
		t.Fatalf("unexpected recovered payout %+v", winner)
	}
	balance(usd, 40, 0)
	receiptCount := func(p payout, account string, want int) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sandbox_channel_receipts WHERE subject_id=$1 AND account_id=$2",
			p.ID, account).Scan(&count); err != nil || count != want {
			t.Fatalf("mock receipt count on %s: %d (%v), want %d", account, count, err, want)
		}
	}
	receiptCount(winner, usd, 1)
	receiptCount(winner, foreign, 0)

	t.Log("signed merchant webhook carries a stable event ID and excludes recipient data")
	if err := replacement.deliverWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	nextDelivery := func() delivery {
		t.Helper()
		select {
		case observed := <-deliveries:
			return observed
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for local payout webhook")
			return delivery{}
		}
	}
	first := nextDelivery()
	eventID := "evt_" + winner.ID + "_succeeded"
	if first.id != eventID || !strings.Contains(first.body, winner.TransferReference) ||
		strings.Contains(first.body, "0427") || strings.Contains(first.body, bank) || strings.Contains(first.body, "payment_id") {
		t.Fatalf("unexpected payout notification %+v", first)
	}
	runSQL("UPDATE collection_outbox SET next_attempt_at=now()-interval '1 second' WHERE id=$1", eventID)
	if err := app.deliverWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	if next := nextDelivery(); next != first {
		t.Fatalf("retry changed webhook %s", next.id)
	}

	var deliveryState string
	var deliveryAttempts int
	if err := db.QueryRowContext(ctx, "SELECT state,attempts FROM collection_outbox WHERE id=$1", eventID).
		Scan(&deliveryState, &deliveryAttempts); err != nil || deliveryState != "delivered" || deliveryAttempts != 2 {
		t.Fatalf("webhook state=%s attempts=%d err=%v", deliveryState, deliveryAttempts, err)
	}

	t.Log("definitive failure releases funds, and review rejection never reserves")
	failed := payoutFrom(draft(op, "fail", "fail", brl, pix, "BRL", 75), 201)
	expect(review(firstRoutes, admin, failed.ID, "approve"), 200, "processing")
	balance(brl, 25, 75)
	if err := replacement.dispatchPayout(ctx); err != nil {
		t.Fatal(err)
	}
	due(failed.ID)
	if err := replacement.reconcilePayout(ctx); err != nil {
		t.Fatal(err)
	}
	expect(get(viewer, failed.ID), 200, "channel_rejected")
	balance(brl, 100, 0)
	expect(review(firstRoutes, reviewer, loser.ID, "reject"), 200, "rejected")
	expect(review(firstRoutes, reviewer, loser.ID, "reject"), 200, "rejected")
	balance(usd, 40, 0)
	receiptCount(loser, usd, 0)

	t.Log("account disabled before dispatch releases the hold without a channel receipt")
	disabled := payoutFrom(draft(op, "disabled", "disabled", brl, pix, "BRL", 81), 201)
	expect(review(firstRoutes, admin, disabled.ID, "approve"), 200, "processing")
	balance(brl, 19, 81)
	runSQL("UPDATE sandbox_channel_accounts SET enabled=false WHERE id=$1", brl)
	if err := replacement.dispatchPayout(ctx); err != nil {
		t.Fatal(err)
	}
	expect(get(viewer, disabled.ID), 200, "payout_account_disabled")
	balance(brl, 100, 0)
	receiptCount(disabled, brl, 0)
	expect(draft(op, "no-disabled-draft", "no-disabled-draft", brl, pix, "BRL", 5), 422, "")
	expect(draft(op, "disabled", "disabled", brl, pix, "BRL", 81), 200, disabled.ID)

	t.Log("unresolved recovery preserves holds, then original-account events settle at most once")
	pending := payoutFrom(draft(op, "pending", "pending", inr, upi, "INR", 50), 201)
	expect(review(firstRoutes, admin, pending.ID, "approve"), 200, "processing")
	if err := replacement.dispatchPayout(ctx); err != nil {
		t.Fatal(err)
	}
	for range maxChannelChecks {
		due(pending.ID)
		if err := replacement.reconcilePayout(ctx); err != nil {
			t.Fatal(err)
		}
	}
	expect(get(viewer, pending.ID), 200, "pending_confirmation")
	balance(inr, 999950, 50)
	receiptCount(pending, inr, 1)
	callback := func(account, signedAccount string, event channelCallback) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(event)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		return call(firstRoutes, "POST", "/sandbox/channel-callbacks/"+account, "", "", string(body), http.Header{
			"Flowxpay-Timestamp": {stamp},
			"Flowxpay-Signature": {sandboxTestSignature(root, "callback", signedAccount, stamp, body)},
		})
	}
	event := channelCallback{EventID: "payout-terminal", Kind: "payout", SubjectID: pending.ID,
		Reference: pending.ChannelReference, Status: "succeeded", Currency: "INR", Amount: 50, TransferReference: "UTR-MOCK-123"}
	expect(callback(inr, brl, event), 401, "")
	expect(callback(brl, brl, event), 404, "")
	expect(callback(inr, inr, event), 200, "applied")
	expect(callback(inr, inr, event), 200, "duplicate")
	event.TransferReference = "UTR-MOCK-CHANGED"
	expect(callback(inr, inr, event), 409, "callback_event_conflict")
	event.EventID = "payout-conflicting-reference"
	expect(callback(inr, inr, event), 200, "terminal_conflict")
	event.EventID, event.Status, event.TransferReference = "payout-late", "pending", ""
	expect(callback(inr, inr, event), 200, "ignored")
	event.EventID, event.Status = "payout-conflicting-terminal", "failed"
	expect(callback(inr, inr, event), 200, "terminal_conflict")
	expect(get(viewer, pending.ID), 200, "UTR-MOCK-123")
	balance(inr, 999950, 0)
	var notifications int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM collection_outbox WHERE payout_id IN ($1,$2,$3,$4,$5)",
		winner.ID, failed.ID, loser.ID, pending.ID, disabled.ID).Scan(&notifications); err != nil || notifications != 5 {
		t.Fatalf("expected one notification per terminal payout: %d (%v)", notifications, err)
	}

	t.Log("two worker instances share the payout account rate and daily budgets")
	runSQL("UPDATE sandbox_payouts SET submitted_at=submitted_at-interval '48 hours' WHERE account_id=$1", usd)
	runSQL(`UPDATE sandbox_payout_accounts SET available_minor=1000,simulation='unresolved',
		dispatches_per_second=1,daily_payout_minor=100 WHERE account_id=$1`, usd)
	submit := func(key, account, recipientID, currency, creator, approver string, amount int64) payout {
		t.Helper()
		p := payoutFrom(draft(creator, key, key, account, recipientID, currency, amount), 201)
		expect(review(firstRoutes, approver, p.ID, "approve"), 200, "processing")
		return p
	}
	dispatchRace := func(first, second payout, denial string) payout {
		t.Helper()
		ready := make(chan struct{})
		errs := make(chan error, 2)
		for _, worker := range []*server{app, replacement} {
			go func() {
				<-ready
				errs <- worker.dispatchPayout(ctx)
			}()
		}
		close(ready)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		var admitted payout
		for _, p := range []payout{first, second} {
			p = payoutFrom(get(viewer, p.ID), 200)
			switch {
			case p.Status == "pending_confirmation" && admitted.ID == "":
				admitted = p
				receiptCount(p, usd, 1)
			case p.Status == "failed" && p.FailureCode == denial:
				receiptCount(p, usd, 0)
			default:
				t.Fatalf("unexpected concurrent dispatch result: %s %s", p.Status, p.FailureCode)
			}
		}
		if admitted.ID == "" {
			t.Fatal("both competing payouts were denied")
		}
		return admitted
	}
	dispatch := func() {
		t.Helper()
		if err := replacement.dispatchPayout(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rateA := submit("rate-a", usd, bank, "USD", op, admin, 20)
	rateB := submit("rate-b", usd, bank, "USD", op, admin, 20)
	unknown := dispatchRace(rateA, rateB, "payout_rate_limited")
	replay := payoutFrom(draft(op, unknown.Reference, unknown.Reference, usd, bank, "USD", 20), 200)
	if replay.ID != unknown.ID {
		t.Fatal("replay returned a different payout")
	}
	expect(review(replacement.routes(), admin, unknown.ID, "approve"), 200, "pending_confirmation")
	dispatch()
	receiptCount(unknown, usd, 1)
	balance(usd, 980, 20)
	runSQL("UPDATE sandbox_payouts SET next_check_at=NULL WHERE id IN ($1,$2)", unknown.ID, pending.ID)

	runSQL(`UPDATE sandbox_payout_accounts SET dispatches_per_second=1000,simulation='failure' WHERE account_id=$1`, usd)
	dailyA := submit("daily-a", usd, bank, "USD", op, admin, 80)
	dailyB := submit("daily-b", usd, bank, "USD", op, admin, 80)
	rejectedAtChannel := dispatchRace(dailyA, dailyB, "payout_daily_limit_exceeded")
	balance(usd, 900, 100)
	blocked := submit("daily-full", usd, bank, "USD", op, admin, 1)
	dispatch()
	expect(get(viewer, blocked.ID), 200, "payout_daily_limit_exceeded")
	receiptCount(blocked, usd, 0)
	balance(usd, 900, 100)

	t.Log("saturated accounts do not block a different account or merchant")
	runSQL("UPDATE sandbox_channel_accounts SET enabled=true WHERE id=$1", brl)
	runSQL("UPDATE sandbox_payouts SET submitted_at=submitted_at-interval '48 hours' WHERE account_id=$1", brl)
	runSQL("UPDATE sandbox_payout_accounts SET dispatches_per_second=1,daily_payout_minor=100 WHERE account_id IN ($1,$2)", brl, foreign)
	otherAccount := submit("separate-account", brl, pix, "BRL", op, admin, 100)
	otherOperator := session("payout-other-operator", "M20029", "operator", validUntil)
	otherMerchant := submit("separate-merchant", foreign, otherBank, "USD", otherOperator, other, 100)
	dispatch()
	dispatch()
	expect(get(viewer, otherAccount.ID), 200, "pending_confirmation")
	expect(get(other, otherMerchant.ID), 200, "pending_confirmation")
	expect(get(viewer, otherMerchant.ID), 404, "")
	receiptCount(otherAccount, brl, 1)
	receiptCount(otherMerchant, foreign, 1)
	balance(usd, 900, 100)

	t.Log("known channel failure returns same-day quota and funds, retaining the submission attempt")
	due(rejectedAtChannel.ID)
	if err := replacement.reconcilePayout(ctx); err != nil {
		t.Fatal(err)
	}
	expect(get(viewer, rejectedAtChannel.ID), 200, "channel_rejected")
	balance(usd, 980, 20)
	runSQL("UPDATE sandbox_payout_accounts SET simulation='unresolved' WHERE account_id=$1", usd)
	recoveredQuota := submit("daily-released", usd, bank, "USD", op, admin, 80)
	dispatch()
	expect(get(viewer, recoveredQuota.ID), 200, "pending_confirmation")
	balance(usd, 900, 100)

	t.Log("rolling-second and UTC midnight boundaries remain distinct in a non-UTC database session")
	midnight := time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC)
	runSQL("UPDATE sandbox_payouts SET submitted_at=$2 WHERE account_id=$1 AND submitted_at IS NOT NULL", usd, midnight.AddDate(0, 0, -1))
	runSQL("UPDATE sandbox_payouts SET submitted_at=$2 WHERE id=$1", recoveredQuota.ID, midnight.Add(-500*time.Millisecond))
	runSQL("UPDATE sandbox_payouts SET submitted_at=$2 WHERE id=$1", rejectedAtChannel.ID, midnight.Add(-250*time.Millisecond))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL TIME ZONE 'Asia/Shanghai'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT 1 FROM sandbox_payout_accounts WHERE account_id=$1 FOR NO KEY UPDATE", usd); err != nil {
		t.Fatal(err)
	}
	probe := recoveredQuota
	probe.merchantID, probe.AmountMinor = "M10028", 21
	for _, tc := range []struct {
		at   time.Time
		rate int
		want string
	}{
		{midnight.Add(-time.Microsecond), 3, "payout_daily_limit_exceeded"},
		{midnight, 2, "payout_rate_limited"},
		{midnight.Add(500 * time.Millisecond), 2, ""},
		{midnight.Add(500 * time.Millisecond), 1, "payout_rate_limited"},
		{midnight.Add(750 * time.Millisecond), 1, ""},
	} {
		got, err := payoutDispatchFailure(ctx, tx, probe, tc.at, tc.rate, 100)
		if err != nil || got != tc.want {
			t.Fatalf("dispatch at %s (rate %d): got %q (%v), want %q", tc.at, tc.rate, got, err, tc.want)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	t.Log("migration backfills legacy receipts without turning pre-dispatch failures into attempts")
	runSQL("UPDATE sandbox_payouts SET submitted_at=NULL WHERE id=$1", unknown.ID)
	for range 2 {
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	var backfilled, skipped bool
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT submitted_at=updated_at FROM sandbox_payouts WHERE id=$1),
		(SELECT submitted_at IS NULL FROM sandbox_payouts WHERE id=$2)`, unknown.ID, blocked.ID).Scan(&backfilled, &skipped)
	if err != nil || !backfilled || !skipped {
		t.Fatalf("legacy submission backfill: submitted=%t denied=%t err=%v", backfilled, skipped, err)
	}
}
