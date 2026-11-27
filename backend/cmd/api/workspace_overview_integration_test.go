package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceOverviewAndPayments(t *testing.T) {
	f := newControlsFixture(t)
	overview := controlWorkspace + "/overview?currency=USD&days=7"
	list := controlWorkspace + "/payments"
	f.call(0, "GET", overview, f.legacy, "", "").expect(t, http.StatusUnauthorized)
	f.call(0, "GET", controlWorkspace+"/overview?currency=USD&days=2", f.admin, "", "").expect(t, http.StatusBadRequest)

	order := func(key, reference string) map[string]any {
		return f.call(0, "POST", "/v1/payments", key, reference,
			`{"market":"US","currency":"USD","method":"card","amount_minor":10000,"merchant_reference":"`+reference+`"}`).object(t, http.StatusCreated)
	}
	paid := order(f.legacy, "overview-paid")
	order(f.legacy, "overview-pending")
	order(f.secondary, "foreign-overview")
	f.exec(t, "UPDATE payments SET status='succeeded',paid_at=now() WHERE id=$1", paid["id"])
	for _, bill := range []struct{ id, merchant, account, user, status string }{
		{"settled-usd", "M10028", "default_M10028_mock_card", "control-admin", "settled"},
		{"pending-usd", "M10028", "default_M10028_mock_card", "control-admin", "pending"},
		{"settled-foreign", "M20029", "default_M20029_mock_card", "control-other", "settled"},
	} {
		f.exec(t, `INSERT INTO sandbox_settlement_batches
			(merchant_id,account_id,currency,id,statement_date,settlement_status,withdrawal_status,
			 withdrawal_reference,body_hash,imported_by,row_count,gross_minor,fee_minor,net_minor)
			VALUES ($1,$2,'USD',$3,(now() AT TIME ZONE 'UTC')::date,$4,'not_reported','',decode('aa','hex'),$5,1,10000,300,9700)`,
			bill.merchant, bill.account, bill.id, bill.status, bill.user)
	}
	got := f.call(1, "GET", overview, f.viewer, "", "").object(t, http.StatusOK)
	if got["orders"] != float64(2) || got["paid_orders"] != float64(1) || got["gross_minor"] != "10000" ||
		got["fees_minor"] != "300" || got["settled_net_minor"] != "9700" {
		t.Fatal("overview mixed merchants, included pending settlement, or miscounted paid orders")
	}
	brl := f.call(0, "GET", controlWorkspace+"/overview?currency=BRL&days=30", f.admin, "", "").object(t, http.StatusOK)
	if brl["gross_minor"] != "0" || brl["fees_minor"] != nil || brl["settled_net_minor"] != nil {
		t.Fatal("overview mixed currencies or treated unavailable settlement as zero")
	}

	for _, token := range []string{f.admin, f.viewer} {
		result := f.call(1, "GET", list+"?reference=overview-pending", token, "", "")
		result.expect(t, http.StatusOK)
		var rows []paymentView
		if err := json.Unmarshal([]byte(result.body), &rows); err != nil || len(rows) != 1 || rows[0].MerchantReference != "overview-pending" {
			t.Fatal("reference search failed")
		}
		if (rows[0].CheckoutPath != "") != (token == f.admin) {
			t.Fatal("checkout access did not follow member permissions")
		}
	}
	result := f.call(1, "GET", list, f.viewer, "", "")
	result.expect(t, http.StatusOK)
	if strings.Contains(result.body, "foreign-overview") || !strings.Contains(result.body, "overview-paid") {
		t.Fatal("workspace order list was not scoped to its merchant")
	}
}

func TestWorkspaceCards(t *testing.T) {
	f := newControlsFixture(t)
	const summary = controlWorkspace + "/settlement-summary"
	const sites = controlWorkspace + "/sites"
	f.call(0, "GET", summary, f.legacy, "", "").expect(t, 401)
	readSummary := func(token string) []workspaceSettlementCurrency {
		t.Helper()
		result := f.call(0, "GET", summary, token, "", "")
		result.expect(t, 200)
		var rows []workspaceSettlementCurrency
		if err := json.Unmarshal([]byte(result.body), &rows); err != nil || rows == nil {
			t.Fatal("invalid summary response")
		}
		return rows
	}
	if len(readSummary(f.viewer)) != 0 {
		t.Fatal("empty summary should be an empty array")
	}
	for _, invalid := range []string{"https://example.test", "example.test/checkout", "example.test:443", "127.0.0.1", "-store.example.test"} {
		f.call(0, "POST", sites, f.admin, "", `{"name":"Bad","business_domain":"`+invalid+`"}`).expect(t, 400)
	}
	replayKey := "card-site"
	create := func(domain string) controlsResult {
		return f.call(0, "POST", sites, f.admin, replayKey, `{"name":"Second shop","business_domain":"`+domain+`"}`)
	}
	second := create(" SHOP.Example.Test ").object(t, 201)
	if second["business_domain"] != "shop.example.test" || second["is_default"] != false {
		t.Fatal("site domain normalization or default flag incorrect")
	}
	if create("shop.example.test").object(t, 200)["id"] != second["id"] {
		t.Fatal("equivalent domain replay created a site")
	}
	create("other.example.test").expect(t, 409)
	f.call(0, "POST", sites, f.viewer, "", `{"name":"Forbidden"}`).expect(t, 403)
	f.call(0, "GET", sites+"/"+second["id"].(string), f.otherMerchant, "", "").expect(t, 404)
	setup := f.call(0, "GET", controlWorkspace+"/setup", f.viewer, "", "").object(t, 200)
	cards := setup["sites"].([]any)
	defaultSite := cards[0].(map[string]any)
	secondSite := cards[1].(map[string]any)
	if len(cards) != 2 || defaultSite["is_default"] != true || defaultSite["business_domain"] != nil ||
		len(defaultSite["methods"].([]any)) == 0 || secondSite["id"] != second["id"] ||
		len(secondSite["methods"].([]any)) != 0 {
		t.Fatal("site cards did not preserve defaults or isolate effective methods")
	}

	importBatch := func(currency, provider, id, status, date, net string, expected int) {
		t.Helper()
		path := controlWorkspace + "/settlement/default_M10028_" + provider + "/" + currency + "/batches"
		body := `{"id":"` + id + `","statement_date":"` + date + `","settlement_status":"` + status + `",
		    "withdrawal_status":"not_reported","entries":[{"id":"entry-` + id + `","kind":"collection",
		    "channel_reference":"reference-` + id + `","gross_minor":1000,"fee_minor":10,"net_minor":` + net + `}]}`
		f.call(0, "POST", path, f.admin, "", body).expect(t, expected)
	}
	importBatch("USD", "mock_card", "card-pending", "pending", "2026-11-20", "991", 201)
	importBatch("USD", "mock_card", "card-pending", "pending", "2026-11-20", "991", 200)
	importBatch("USD", "mock_card", "card-pending", "settled", "2026-11-20", "100", 409)
	importBatch("USD", "mock_card", "card-invalid", "settled", "2026-02-30", "777", 400)
	importBatch("USD", "mock_card", "card-settled-old", "settled", "2026-11-16", "850", 201)
	importBatch("USD", "mock_card", "card-settled", "settled", "2026-11-19", "995", 201)
	importBatch("BRL", "mock_pix", "pix-pending", "pending", "2026-11-20", "0", 201)
	importBatch("INR", "mock_in_redirect", "india-settled", "settled", "2026-11-18", "-10", 201)

	rows := readSummary(f.viewer)
	if len(rows) != 3 || rows[0].Currency != "BRL" || siteDomain(rows[0].PendingNetMinor) != "0" ||
		rows[0].SettledNetMinor != nil || rows[0].LatestStatementDate != nil ||
		rows[1].Currency != "INR" || rows[1].PendingNetMinor != nil || siteDomain(rows[1].SettledNetMinor) != "-10" ||
		rows[2].Currency != "USD" || siteDomain(rows[2].PendingNetMinor) != "991" ||
		siteDomain(rows[2].SettledNetMinor) != "1845" || siteDomain(rows[2].LatestStatementDate) != "2026-11-19" ||
		rows[2].StatementAccountID != "default_M10028_mock_card" {
		t.Fatalf("unexpected settlement summary: %+v", rows)
	}
	if len(readSummary(f.otherMerchant)) != 0 {
		t.Fatal("a second merchant could see imported batches")
	}
}
