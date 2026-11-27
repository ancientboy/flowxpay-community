package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestSandboxSettlement(t *testing.T) {
	dsn := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL via scripts/local-sandbox.sh test")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	schema := fmt.Sprintf("settlement_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	testDB := stdlib.OpenDB(*cfg)
	defer testDB.Close()
	testDB.SetMaxOpenConns(12)
	if err := migrations.Apply(ctx, testDB); err != nil {
		t.Fatal(err)
	}
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := testDB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	session := func(merchant, id, role string) string {
		t.Helper()
		run("INSERT INTO merchant_members (id,merchant_id,name,role) VALUES ($1,$2,$1,$3)", id, merchant, role)
		token, digest := newAuthToken("fxs_")
		run("INSERT INTO merchant_sessions (token_hash,member_id,expires_at) VALUES ($1,$2,now()+interval '1 hour')", digest[:], id)
		return token
	}
	admin := session("M10028", "settlement-admin", "admin")
	operator := session("M10028", "settlement-operator", "operator")
	viewer := session("M10028", "settlement-viewer", "viewer")
	other := session("M20029", "settlement-other", "admin")
	expired := session("M10028", "settlement-expired", "admin")
	run("UPDATE merchant_sessions SET expires_at=now()-interval '1 second' WHERE member_id='settlement-expired'")

	const usdAccount = "default_M10028_mock_card"
	const brlAccount = "default_M10028_mock_pix"
	const usd = "/sandbox/workspace/settlement/" + usdAccount + "/USD"
	const brl = "/sandbox/workspace/settlement/" + brlAccount + "/BRL"
	const key = "settlement-synthetic-merchant-key"
	app := &server{db: testDB, collection: sandboxCollection{db: testDB}, signing: bytes.Repeat([]byte("s"), 32),
		merchants: [2]sandboxMerchant{{ID: "M10028", Name: "Cedar Commerce", Key: key}}}
	routes := app.routes()
	type response struct {
		code    int
		body    string
		headers http.Header
	}
	call := func(method, path, token, body string) response {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if path == "/v1/payments" && method == "POST" {
			request.Header.Set("Idempotency-Key", "payment-while-report-busy")
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, request)
		return response{recorder.Code, recorder.Body.String(), recorder.Header()}
	}
	check := func(want int, result response) string {
		t.Helper()
		if result.code != want {
			t.Fatalf("want HTTP %d, got %d: %s", want, result.code, result.body)
		}
		return result.body
	}
	fixture := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "fixtures", "phase-5", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	sample := fixture("usd-statement.json")
	reversal := fixture("usd-reversal.json")
	brlSample := fixture("brl-statement.json")
	const insertPayment = `INSERT INTO payments
		(id,merchant_id,merchant_reference,idempotency_key,request_hash,market,currency,method,amount_minor,status,route,
		 route_snapshot,channel_reference) VALUES ($1,$2,$1,$1,'sample',$3,$4,$5,$6,$7,'mock',
		 jsonb_build_object('account_id',$8::text),$9)`
	const oldCollection = "pay_0123456789abcdef0123456789abcdef"
	newCollection := newResourceID(paymentPrefix)
	run(insertPayment, oldCollection, "M10028", "US", "USD", "card", 10000, "succeeded", usdAccount, "mock-collection-01")
	run(insertPayment, "settlement-pay-late", "M10028", "US", "USD", "card", 900, "pending_confirmation", usdAccount, "mock-collection-late")
	run(insertPayment, "settlement-pay-wrong", "M10028", "US", "USD", "card", 800, "succeeded", usdAccount, "mock-collection-wrong")
	run(insertPayment, "settlement-pay-internal-only", "M10028", "US", "USD", "card", 180, "succeeded", usdAccount, "unreported")
	run(insertPayment, newCollection, "M10028", "BR", "BRL", "pix", 3000, "succeeded", brlAccount, "mock-collection-01")
	run(insertPayment, "settlement-foreign", "M20029", "US", "USD", "card", 500, "succeeded", "default_M20029_mock_card", "mock-collection-missing")
	run(`INSERT INTO sandbox_refunds
		(id,merchant_id,payment_id,idempotency_key,amount_minor,currency,route_snapshot,payment_reference,status,channel_reference)
		VALUES ('settlement-refund','M10028',$2,'settlement-refund',2000,'USD',
		        jsonb_build_object('account_id',$1::text),'mock-collection-01','succeeded','mock-refund-01')`, usdAccount, oldCollection)
	run(`INSERT INTO sandbox_payout_recipients (id,merchant_id,request_key,recipient_type,test_suffix)
		VALUES ('settlement-recipient','M10028','settlement-recipient','bank_account','1234')`)
	run(`INSERT INTO sandbox_payouts
		(id,merchant_id,merchant_reference,request_key,account_id,recipient_id,recipient_type,amount_minor,currency,
		 status,created_by,reviewed_by,reviewed_at,channel_reference,transfer_reference)
		VALUES ('settlement-payout','M10028','settlement-payout','settlement-payout',$1,'settlement-recipient',
		        'bank_account',1000,'USD','succeeded','settlement-operator','settlement-admin',now(),'mock-payout-01','fake-transfer-01')`, usdAccount)

	check(403, call("POST", usd+"/batches", viewer, sample))
	check(401, call("GET", usd+"/batches", expired, ""))
	check(401, call("GET", usd+"/batches", key, ""))
	check(404, call("POST", usd+"/batches", other, sample))
	check(400, call("POST", strings.Replace(usd, "/USD", "/BRL", 1)+"/batches", admin, sample))
	check(400, call("POST", usd+"/batches", admin, strings.Replace(sample, `"fee_minor":300,`, "", 1)))
	check(409, call("POST", usd+"/batches", admin, reversal))

	duplicate := make(chan response, 2)
	for range 2 {
		go func() { duplicate <- call("POST", usd+"/batches", operator, sample) }()
	}
	first, second := <-duplicate, <-duplicate
	if first.code == 200 {
		first, second = second, first
	}
	check(201, first)
	check(200, second)
	var reordered statementBatch
	if err := json.Unmarshal([]byte(sample), &reordered); err != nil {
		t.Fatal(err)
	}
	reordered.Entries[0], reordered.Entries[5] = reordered.Entries[5], reordered.Entries[0]
	reorderedJSON, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	check(200, call("POST", usd+"/batches", admin, string(reorderedJSON)))
	check(409, call("POST", usd+"/batches", admin, strings.Replace(sample, `"settled"`, `"pending"`, 1)))
	check(409, call("POST", usd+"/batches", admin, strings.NewReplacer(
		"sample-usd-20261119", "changed-batch", "-usd-unmatched", "-usd-another-row", "mock-collection-missing", "another-reference").Replace(sample)))
	check(404, call("GET", usd+"/batches/changed-batch", admin, ""))
	const secondaryUSD = "/sandbox/workspace/settlement/settlement-card2/USD"
	run(`INSERT INTO sandbox_channel_accounts (id,merchant_id,site_id,provider)
	    VALUES ('settlement-card2','M10028','default_M10028','mock_card')`)
	check(404, call("GET", secondaryUSD+"/batches/sample-usd-20261119", viewer, ""))
	check(201, call("POST", secondaryUSD+"/batches", admin, brlSample))
	otherAccount := check(200, call("GET", secondaryUSD+"/batches/sample-usd-20261119", viewer, ""))
	if !strings.Contains(otherAccount, `"match_status":"missing"`) {
		t.Fatal(otherAccount)
	}

	check(201, call("POST", brl+"/batches", admin, brlSample))
	check(409, call("POST", brl+"/batches", admin, reversal))
	check(409, call("POST", usd+"/batches", admin, strings.Replace(reversal, "2026-12-01", "2026-11-18", 1)))
	check(409, call("POST", usd+"/batches", admin, strings.Replace(reversal, `"fee_minor":-300`, `"fee_minor":-299`, 1)))
	check(201, call("POST", usd+"/batches", admin, reversal))
	check(200, call("POST", usd+"/batches", admin, reversal))
	check(409, call("POST", usd+"/batches", admin, strings.NewReplacer(
		"sample-usd-20261201", "another-reversal", "usd-reversal-1", "usd-reversal-2").Replace(reversal)))

	detailPath := usd + "/batches/sample-usd-20261119"
	body := check(200, call("GET", detailPath, viewer, ""))
	var batch settlementBatchDetail
	if err := json.Unmarshal([]byte(body), &batch); err != nil {
		t.Fatal(err)
	}
	var gross, fee, net int64
	states := map[string]string{
		"usd-collection-1": "matched", "usd-refund-1": "matched", "usd-payout-1": "matched",
		"usd-late-order": "status_mismatch", "usd-amount-difference": "amount_mismatch", "-usd-unmatched": "missing",
	}
	for _, entry := range batch.Entries {
		gross += *entry.GrossMinor
		fee += *entry.FeeMinor
		net += *entry.NetMinor
		if entry.Match != states[entry.statementRow.ID] || entry.CalculatedNet != *entry.GrossMinor-*entry.FeeMinor {
			t.Fatalf("unexpected scope or reconciliation for %s: %+v", entry.statementRow.ID, entry)
		}
	}
	if batch.RowCount != 6 || len(batch.Entries) != 6 || gross != 9100 || fee != 430 || net != 8665 ||
		batch.GrossMinor != gross || batch.FeeMinor != fee || batch.NetMinor != net || batch.CalculatedNet != 8670 ||
		batch.ImportedBy != "settlement-operator" || batch.WithdrawalReference != "mock-withdrawal-usd-01" {
		t.Fatalf("incorrect batch rollup: %+v / %d %d %d", batch.batchSummary, gross, fee, net)
	}
	late := usd + "/entries/usd-late-order/actions"
	action := func(name string, rev int) string {
		return fmt.Sprintf(`{"action":%q,"note":"Reviewed fixture","expected_revision":%d}`, name, rev)
	}
	check(403, call("POST", late, viewer, action("acknowledge", 0)))
	check(404, call("GET", late, other, ""))
	check(200, call("POST", late, operator, action("acknowledge", 0)))
	check(409, call("POST", late, operator, action("acknowledge", 0)))
	check(200, call("POST", late, admin, action("reopen", 1)))
	run("UPDATE payments SET status='succeeded' WHERE id='settlement-pay-late'")
	updated := check(200, call("POST", late, operator, action("recheck", 2)))
	if !strings.Contains(updated, `"difference_status":"matched"`) {
		t.Fatal(updated)
	}
	history := check(200, call("GET", late, viewer, ""))
	for _, required := range []string{`"action":"acknowledge"`, `"action":"reopen"`, `"action":"recheck"`, `"before_check"`, `"after_check"`} {
		if !strings.Contains(history, required) {
			t.Fatalf("audit missing %s: %s", required, history)
		}
	}
	check(409, call("POST", late, admin, action("acknowledge", 3)))

	for _, test := range []struct{ path, gross, fees, net, calculated string }{
		{usd + "/totals?from=2026-11-01&through=2026-11-30", "9100", "430", "8665", "8670"},
		{usd + "/totals?from=2026-11-01&through=2026-12-31", "-900", "130", "-1035", "-1030"},
		{brl + "/totals?from=2026-11-01&through=2026-12-31", "3000", "60", "2940", "2940"},
	} {
		text := check(200, call("GET", test.path, viewer, ""))
		for field, value := range map[string]string{"gross_minor": test.gross, "fee_minor": test.fees, "net_minor": test.net, "calculated_net_minor": test.calculated} {
			if !strings.Contains(text, fmt.Sprintf("%q:%q", field, value)) {
				t.Fatal(text)
			}
		}
	}
	check(400, call("GET", usd+"/totals?from=2026-01-01&through=2028-01-01", viewer, ""))
	text := check(200, call("GET", usd+"/orders/collection", viewer, ""))
	var orders settlementPage[settlementOrder]
	if err := json.Unmarshal([]byte(text), &orders); err != nil {
		t.Fatal(err)
	}
	if len(orders.Data) != 4 || orders.Data[0].ID != oldCollection || orders.Data[0].EntryCount != 2 ||
		orders.Data[0].GrossMinor != 0 || orders.Data[1].ID != "settlement-pay-internal-only" || orders.Data[1].EntryCount != 0 {
		t.Fatal(text)
	}
	for _, kind := range []string{"refund", "payout"} {
		check(200, call("GET", usd+"/orders/"+kind+"/report.csv", viewer, ""))
	}
	export := call("GET", detailPath+"/entries.csv", viewer, "")
	csvRows, err := csv.NewReader(strings.NewReader(check(200, export))).ReadAll()
	if err != nil || len(csvRows) != 7 || csvRows[1][7] != "'-usd-unmatched" ||
		!strings.HasPrefix(export.headers.Get("Content-Type"), "text/csv") {
		t.Fatalf("invalid batch CSV: %v %v", csvRows, err)
	}
	for _, scope := range []struct{ path, order string }{{usd, oldCollection}, {brl, newCollection}} {
		rows, err := csv.NewReader(strings.NewReader(check(200, call("GET",
			scope.path+"/batches/sample-usd-20261119/entries.csv", viewer, "")))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, row := range rows[1:] {
			matched = matched || row[9] == "mock-collection-01" && row[15] == scope.order && row[18] == "matched"
		}
		if !matched {
			t.Fatalf("CSV did not preserve order and upstream references for %s", scope.path)
		}
	}
	t.Log("settlement reconciled and exported exact legacy and new platform collection IDs beside their upstream references")
	check(404, call("GET", detailPath+"/entries.csv", other, ""))
	check(405, call("POST", usd+"/batches/sample-usd-20261119/entries.csv", admin, "{}"))
	t.Log("recomputed USD and BRL entries, cross-period reversal, duplicate import, scoped differences and CSV")

	t.Run("large report and concurrent payment", func(t *testing.T) {
		run(`INSERT INTO sandbox_settlement_batches
		    (merchant_id,account_id,currency,id,statement_date,settlement_status,withdrawal_status,withdrawal_reference,
		     body_hash,imported_by,row_count,gross_minor,fee_minor,net_minor)
		    SELECT 'M10028',$1,'USD','volume-'||lpad(n::text,4,'0'),'2026-10-01','settled','not_reported','',
		           decode('00','hex'),'settlement-admin',500,5000,500,4500 FROM generate_series(1,120) AS n`, usdAccount)
		run(`INSERT INTO sandbox_settlement_entries
		    (merchant_id,account_id,currency,batch_id,id,kind,channel_reference,gross_minor,fee_minor,net_minor,match_status)
		    SELECT 'M10028',$1,'USD','volume-'||lpad(b::text,4,'0'),b||'-volume-'||n,'collection',
		        'mock-volume-'||b||'-'||n,10,1,9,'missing'
		    FROM generate_series(1,120) AS b CROSS JOIN generate_series(1,500) AS n`, usdAccount)
		run(`INSERT INTO sandbox_settlement_daily VALUES ('M10028',$1,'USD','2026-10-01',120,60000,600000,60000,540000)`, usdAccount)
		run(`INSERT INTO payments (id,merchant_id,merchant_reference,idempotency_key,request_hash,market,currency,method,
		    amount_minor,status,route,route_snapshot,channel_reference,expires_at)
		    SELECT 'volume-pay-'||n,'M10028','volume-pay-'||n,'volume-pay-'||n,'sample','US','USD','card',200,
		        'requires_action','mock',jsonb_build_object('account_id',$1::text),'mock-volume-pay-'||n,now()-interval '1 day'
		    FROM generate_series(1,130) AS n`, usdAccount)
		orderFile := call("GET", usd+"/orders/collection/report.csv", viewer, "")
		firstPage, err := csv.NewReader(strings.NewReader(check(200, orderFile))).ReadAll()
		cursor := orderFile.headers.Get("X-Next-After")
		if err != nil || cursor == "" || len(firstPage) != 101 {
			t.Fatalf("invalid first order CSV page: %d, cursor %q, %v", len(firstPage), cursor, err)
		}
		orderFile = call("GET", usd+"/orders/collection/report.csv?after="+cursor, viewer, "")
		lastPage, err := csv.NewReader(strings.NewReader(check(200, orderFile))).ReadAll()
		if err != nil || orderFile.headers.Get("X-Next-After") != "" || len(lastPage) != 35 ||
			lastPage[1][3] <= cursor || lastPage[1][5] != "expired" {
			t.Fatalf("invalid final order CSV page: %d, cursor %q, %v", len(lastPage), cursor, err)
		}

		var seen int
		for cursor := ""; ; {
			body := check(200, call("GET", usd+"/batches?after="+cursor, viewer, ""))
			var page settlementPage[batchSummary]
			if err := json.Unmarshal([]byte(body), &page); err != nil {
				t.Fatal(err)
			}
			for _, b := range page.Data {
				if strings.HasPrefix(b.ID, "volume-") {
					seen += b.RowCount
				}
			}
			if page.NextAfter == "" {
				break
			}
			cursor = page.NextAfter
		}
		file := check(200, call("GET", usd+"/batches/volume-0120/entries.csv", viewer, ""))
		lines, err := csv.NewReader(strings.NewReader(file)).ReadAll()
		if err != nil || len(lines) != 501 || seen != 60000 {
			t.Fatalf("volume report %d rows, CSV %d rows, error %v", seen, len(lines), err)
		}
		totalsURL := usd + "/totals?from=2026-10-01&through=2026-10-31"
		totals := check(200, call("GET", totalsURL, viewer, ""))
		if !strings.Contains(totals, `"row_count":60000`) || !strings.Contains(totals, `"net_minor":"540000"`) {
			t.Fatal(totals)
		}

		lock, err := testDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback()
		if _, err := lock.ExecContext(ctx, "LOCK TABLE sandbox_settlement_daily IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		reports := make(chan response, 2)
		for range 2 {
			go func() { reports <- call("GET", totalsURL, viewer, "") }()
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			var waiting int
			err := testDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			    WHERE wait_event_type='Lock' AND query LIKE '%FROM sandbox_settlement_daily WHERE merchant_id=%'`).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting == 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("two reports did not reach the database lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
		check(429, call("GET", totalsURL, viewer, ""))
		start := time.Now()
		created := check(201, call("POST", "/v1/payments", key,
			`{"merchant_reference":"payment-while-report-busy","market":"US","currency":"USD","method":"card","amount_minor":199}`))
		var immediate struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(created), &immediate); err != nil || immediate.ID == "" {
			t.Fatalf("%v %s", err, created)
		}
		check(200, call("GET", "/v1/payments/"+immediate.ID, key, ""))
		elapsed := time.Since(start)
		if elapsed > time.Second {
			t.Fatalf("payment and lookup took %s with report connections occupied", elapsed)
		}
		if err := lock.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Fatal(err)
		}
		check(200, <-reports)
		check(200, <-reports)
		t.Logf("60,000 entries, 120 batches, 500-row CSV. Payment and lookup while two reports waited: %s", elapsed)
	})
}
