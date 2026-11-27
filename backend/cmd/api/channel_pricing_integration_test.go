package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
)

func pricingRows(t *testing.T, response controlsResult) []map[string]any {
	t.Helper()
	response.expect(t, 200)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(response.body), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertOrderFee(t *testing.T, order map[string]any, source string, amount float64) map[string]any {
	t.Helper()
	quote, ok := order["quote"].(map[string]any)
	if !ok || quote["source"] != source || quote["fee_minor"] != amount || quote["version_id"] == "" || order["cost"] != nil {
		t.Fatalf("unexpected merchant quote: %+v", quote)
	}
	return quote
}

func TestChannelRatesCollectionSwitchAndHistory(t *testing.T) {
	f := newControlsFixture(t)
	const ops = "/sandbox/ops"
	const merchant = ops + "/merchants/M10028"
	const other = ops + "/merchants/M20029"
	const firstChannel = "channel_builtin_india"
	const nextChannel = "synthetic-pricing-qr"
	const site = "default_M10028"
	const switchPath = merchant + "/sites/" + site + "/switch/" + nextChannel
	const quotePath = controlWorkspace + "/channel-quotes"

	publish := func(path, kind string, bps, fixed, status int) map[string]any {
		t.Helper()
		return f.call(0, "PUT", path, f.ops, "", fmt.Sprintf(`{"direction":"collection","currency":"INR","kind":%q,"bps":%d,"fixed_minor":%d}`, kind, bps, fixed)).object(t, status)
	}
	makePayment := func(i int, token, reference, key string) controlsResult {
		return f.call(i, "POST", "/v1/payments", token, key,
			fmt.Sprintf(`{"merchant_reference":%q,"market":"IN","currency":"INR","method":"gateway","amount_minor":10100}`, reference))
	}
	channelOf := func(p map[string]any) any { return p["route_snapshot"].(map[string]any)["platform_channel_id"] }

	f.exec(t, `INSERT INTO payments (id,merchant_id,merchant_reference,idempotency_key,request_hash,market,currency,method,amount_minor,status,route)
		VALUES ('prepricing-payment','M10028','prepricing-legacy','prepricing-legacy','legacy','IN','INR','gateway',10100,'requires_action','mock_in_redirect')`)
	legacy := f.call(0, "GET", "/v1/payments/prepricing-payment", f.legacy, "", "").object(t, 200)
	missing := makePayment(0, f.legacy, "missing-rate", "missing-rate").object(t, 201)
	if legacy["quote"] != nil || missing["quote"] != nil {
		t.Fatal("unconfigured orders displayed a fee")
	}

	f.call(0, "POST", ops+"/channels", f.ops, "", `{"id":"synthetic-pricing-qr","name":"QR Pricing Test","provider":"mock_in_qr","enabled":true}`).expect(t, 201)
	firstPath := ops + "/channels/" + firstChannel + "/prices"
	nextPath := ops + "/channels/" + nextChannel + "/prices"
	overridePath := merchant + "/channels/" + firstChannel + "/prices"
	publish(firstPath, "cost", 15, 2, 201)
	defaultVersion := publish(firstPath, "default", 25, 7, 201)
	if replay := publish(firstPath, "default", 25, 7, 200); replay["version_id"] != defaultVersion["version_id"] {
		t.Fatal("unchanged PUT created a new price version")
	}
	overrideVersion := publish(overridePath, "override", 50, 11, 201)
	publish(nextPath, "cost", 20, 6, 201)
	publish(nextPath, "default", 75, 4, 201)

	f.call(0, "PUT", firstPath, f.observer, "", `{}`).expect(t, 403)
	f.call(0, "PUT", firstPath, f.admin, "", `{}`).expect(t, 401)
	f.call(0, "PUT", firstPath, f.ops, "", `{"direction":"collection","currency":"USD","kind":"cost","bps":1,"fixed_minor":1}`).expect(t, 400)
	f.call(0, "GET", switchPath, f.admin, "", "").expect(t, 401)
	f.call(0, "GET", other+"/sites/"+site+"/switch/"+nextChannel, f.ops, "", "").expect(t, 404)

	orders := raceWorkspaceControl(func(i int) controlsResult { return makePayment(i, f.legacy, "price-before", "price-before") })
	if orders[1].status == 201 {
		orders[0], orders[1] = orders[1], orders[0]
	}
	original, concurrentReplay := orders[0].object(t, 201), orders[1].object(t, 200)
	quote := assertOrderFee(t, original, "override", 62)
	if original["id"] != concurrentReplay["id"] || !reflect.DeepEqual(quote, concurrentReplay["quote"]) ||
		quote["version_id"] != overrideVersion["version_id"] || channelOf(original) != firstChannel {
		t.Fatal("concurrent requests diverged in order, channel or price")
	}
	second := makePayment(1, f.secondary, "second-merchant", "price-before").object(t, 201)
	if channelOf(second) != firstChannel {
		t.Fatal("the two merchants did not use the same platform channel")
	}
	assertOrderFee(t, second, "default", 32)
	f.call(0, "GET", "/v1/payments/"+original["id"].(string), f.secondary, "", "").expect(t, 404)
	for _, own := range []struct {
		token  string
		source string
		bps    int
	}{{f.admin, "override", 50}, {f.otherMerchant, "default", 25}} {
		var quote *feeVersion
		response := f.call(0, "GET", quotePath+"?merchant=M10028", own.token, "", "")
		response.expect(t, 200)
		var rows []merchantChannelQuote
		if err := json.Unmarshal([]byte(response.body), &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ChannelID == firstChannel && row.Direction == "collection" {
				quote = row.Quote
			}
		}
		if strings.Contains(response.body, `"cost"`) || quote == nil || quote.Source != own.source || quote.BPS != own.bps {
			t.Fatal("merchant rates leaked another scope or returned an incorrect rate")
		}
	}

	preview := func(token string) map[string]any {
		return f.call(1, "GET", switchPath, token, "", "").object(t, 200)
	}
	confirm := func(instance int, value any) controlsResult {
		return f.call(instance, "POST", switchPath, f.ops, "", fmt.Sprintf(`{"preview":%q}`, value))
	}
	viewer := preview(f.observer)
	if viewer["preview"] != "" || viewer["target"].(map[string]any)["cost"] != nil || viewer["current"].(map[string]any)["cost"] != nil {
		t.Fatal("platform viewer saw restricted preview fields")
	}
	prior := preview(f.ops)
	if prior["current"].(map[string]any)["channel_id"] != firstChannel || prior["target"].(map[string]any)["account_id"] != nil {
		t.Fatal("unexpected current/target snapshot")
	}
	publish(nextPath, "default", 80, 4, 201)
	confirm(0, prior["preview"]).expect(t, 409)
	f.exec(t, `ALTER TABLE sandbox_platform_audit ADD CONSTRAINT pricing_test_audit_failure CHECK (action <> 'route.switch')`)
	confirm(0, preview(f.ops)["preview"]).expect(t, 503)
	f.exec(t, `ALTER TABLE sandbox_platform_audit DROP CONSTRAINT pricing_test_audit_failure`)
	fresh := preview(f.ops)
	if fresh["target"].(map[string]any)["account_id"] != nil {
		t.Fatal("assignment persisted after switch audit rollback")
	}
	switched := workspaceRaceWinner(t, raceWorkspaceControl(func(i int) controlsResult { return confirm(i, fresh["preview"]) }), 201)
	if switched["approved_by"] != "ops-submit" || switched["submitted_by"] != "ops-submit" {
		t.Fatal("single-operator activation was not recorded")
	}
	newOrder := makePayment(1, f.legacy, "price-after", "price-after").object(t, 201)
	assertOrderFee(t, newOrder, "default", 85)
	if channelOf(newOrder) != nextChannel || channelOf(makePayment(1, f.secondary, "still-original", "still-original").object(t, 201)) != firstChannel {
		t.Fatal("route activation used the wrong merchant or channel")
	}

	restore := `{"direction":"collection","currency":"INR","kind":"override","restore_default":true}`
	f.call(0, "PUT", overridePath, f.ops, "", restore).expect(t, 201)
	f.call(1, "PUT", overridePath, f.ops, "", restore).expect(t, 200)
	if err := migrations.Apply(t.Context(), f.db); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	replayed := makePayment(1, f.legacy, "price-before", "price-before").object(t, 200)
	if channelOf(replayed) != firstChannel || !reflect.DeepEqual(quote, replayed["quote"]) || replayed["id"] != original["id"] {
		t.Fatal("order changed after route/pricing update or schema replay")
	}
	returnPath := merchant + "/sites/" + site + "/switch/" + firstChannel
	returnPreview := f.call(0, "GET", returnPath, f.ops, "", "").object(t, 200)
	f.call(0, "POST", returnPath, f.ops, "", fmt.Sprintf(`{"preview":%q}`, returnPreview["preview"])).expect(t, 201)
	assertOrderFee(t, makePayment(0, f.legacy, "restored-default", "restored-default").object(t, 201), "default", 32)

	checkout := "/sandbox" + original["checkout_path"].(string)
	checkoutResult := f.call(0, "POST", checkout+"/confirm", "", "", `{"confirm":true}`).object(t, 200)
	if checkoutResult["status"] != "succeeded" || checkoutResult["quote"] != nil {
		t.Fatal("checkout confirmation or merchant fee redaction failed")
	}
	account := original["route_snapshot"].(map[string]any)["account_id"].(string)
	var channelReference string
	if err := f.db.QueryRowContext(t.Context(), "SELECT channel_reference FROM payments WHERE id=$1", original["id"]).Scan(&channelReference); err != nil {
		t.Fatal(err)
	}
	statementPath := controlWorkspace + "/settlement/" + account + "/INR/batches"
	base := fmt.Sprintf(`{"id":"priced-collection-batch","statement_date":"2026-11-20","settlement_status":"settled","withdrawal_status":"not_reported","withdrawal_reference":"","entries":[{"id":"priced-collection-row","kind":"collection","channel_reference":%q,"gross_minor":10100,"fee_minor":19,"net_minor":10081}]}`, channelReference)
	f.call(0, "POST", statementPath, f.admin, "", base).expect(t, 201)
	feesPath := merchant + "/order-fees?reference=price-before"
	fees := pricingRows(t, f.call(0, "GET", feesPath, f.ops, "", ""))
	if len(fees) != 1 || fees[0]["statement_fee_minor"] != float64(19) || fees[0]["statement_net_minor"] != float64(10081) ||
		fees[0]["estimated_margin_minor"] != float64(45) || fees[0]["statement_status"] != "matched_settled" {
		t.Fatalf("collection pricing/report mismatch: %+v", fees)
	}
	observerFees := pricingRows(t, f.call(0, "GET", feesPath, f.observer, "", ""))
	if observerFees[0]["cost"] != nil || observerFees[0]["estimated_margin_minor"] != nil {
		t.Fatal("viewer role saw upstream costs")
	}
	f.call(0, "GET", feesPath, f.admin, "", "").expect(t, 401)
	reversal := fmt.Sprintf(`{"id":"priced-reversal-batch","statement_date":"2026-11-21","settlement_status":"settled","withdrawal_status":"not_reported","withdrawal_reference":"","entries":[{"id":"priced-reversal","kind":"reversal","reverses_entry":"priced-collection-row","channel_reference":%q,"gross_minor":-10100,"fee_minor":-19,"net_minor":-10081}]}`, channelReference)
	f.call(0, "POST", statementPath, f.admin, "", reversal).expect(t, 201)
	fees = pricingRows(t, f.call(0, "GET", feesPath, f.ops, "", ""))
	if fees[0]["statement_status"] != "reversal_reported" || fees[0]["statement_fee_minor"] != nil {
		t.Fatal("reversed statement still appeared as a reconciled fee")
	}
}

func TestChannelRatesPayoutSnapshotsAndStatement(t *testing.T) {
	f := newControlsFixture(t)
	const base = "/sandbox/ops/channels/channel_builtin_card"
	publish := func(kind string, bps, fixed int) {
		t.Helper()
		f.call(0, "PUT", base+"/prices", f.ops, "", fmt.Sprintf(
			`{"direction":"payout","currency":"USD","kind":%q,"bps":%d,"fixed_minor":%d}`, kind, bps, fixed)).expect(t, 201)
	}
	publish("cost", 125, 2)
	publish("default", 250, 30)
	recipient := f.call(0, "POST", "/v1/payout-recipients", f.operator, "recipient-price",
		`{"type":"bank_account","test_suffix":"1234"}`).object(t, 201)["id"]
	const account = "default_M10028_mock_card"
	draft := fmt.Sprintf(`{"merchant_reference":"costed-payout","account_id":%q,"recipient_id":%q,"currency":"USD","amount_minor":10001}`, account, recipient)
	attempt := func(i int) controlsResult { return f.call(i, "POST", "/v1/payouts", f.operator, "payout-price", draft) }
	results := raceWorkspaceControl(attempt)
	if results[1].status == 201 {
		results[0], results[1] = results[1], results[0]
	}
	first, replay := results[0].object(t, 201), results[1].object(t, 200)
	quote := assertOrderFee(t, first, "default", 280)
	if first["id"] != replay["id"] || !reflect.DeepEqual(quote, replay["quote"]) {
		t.Fatal("payout price changed during concurrently replayed draft")
	}
	publish("default", 0, 0)
	f.call(0, "PUT", base+"/status", f.ops, "", `{"enabled":false}`).expect(t, 200)
	replay = attempt(1).object(t, 200)
	if replay["platform_channel_id"] != "channel_builtin_card" || replay["account_id"] != account || !reflect.DeepEqual(quote, replay["quote"]) {
		t.Fatal("old payout failed to replay when the channel was disabled or price changed")
	}
	f.call(0, "PUT", base+"/status", f.ops, "", `{"enabled":true}`).expect(t, 200)
	id := first["id"].(string)
	f.call(0, "POST", "/v1/payouts/"+id+"/approve", f.reviewer, "", "").expect(t, 200)
	worker := &server{db: f.db}
	if err := worker.dispatchPayout(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE sandbox_payouts SET next_check_at=now() WHERE id=$1", id)
	if err := worker.reconcilePayout(t.Context()); err != nil {
		t.Fatal(err)
	}
	final := f.call(1, "GET", "/v1/payouts/"+id, f.operator, "", "").object(t, 200)
	if final["status"] != "succeeded" || !reflect.DeepEqual(quote, final["quote"]) {
		t.Fatal("payout settlement changed pricing")
	}
	statement := fmt.Sprintf(`{"id":"priced-payout-batch","statement_date":%q,"settlement_status":"settled","withdrawal_status":"not_reported","withdrawal_reference":"","entries":[{"id":"priced-payout-row","kind":"payout","channel_reference":%q,"gross_minor":-10001,"fee_minor":133,"net_minor":-10134}]}`, time.Now().UTC().Format("2006-01-02"), first["channel_reference"])
	f.call(0, "POST", controlWorkspace+"/settlement/"+account+"/USD/batches", f.admin, "", statement).expect(t, 201)
	fees := pricingRows(t, f.call(0, "GET", "/sandbox/ops/merchants/M10028/order-fees?reference="+id, f.ops, "", ""))
	if len(fees) != 1 || fees[0]["statement_fee_minor"] != float64(133) || fees[0]["statement_net_minor"] != float64(-10134) ||
		fees[0]["estimated_margin_minor"] != float64(153) || fees[0]["statement_status"] != "matched_settled" {
		t.Fatalf("payout pricing/report mismatch: %+v", fees)
	}
	const platformFinance = "/sandbox/ops/finance"
	f.call(0, "GET", platformFinance, f.admin, "", "").expect(t, 401)
	f.call(0, "GET", platformFinance+"?from=invalid", f.ops, "", "").expect(t, 400)
	f.call(0, "GET", platformFinance+"?merchant=M00000", f.ops, "", "").expect(t, 404)
	report := f.call(0, "GET", platformFinance, f.ops, "", "").object(t, 200)
	summary := report["summary"].([]any)
	payouts := report["payouts"].([]any)
	settlements := report["settlements"].([]any)
	if len(summary) != 1 || len(payouts) != 1 || len(settlements) != 1 {
		t.Fatalf("unexpected finance report: %+v", report)
	}
	amount := summary[0].(map[string]any)
	if amount["estimated_revenue_minor"] != "280" || amount["estimated_cost_minor"] != "127" ||
		amount["estimated_margin_minor"] != "153" || amount["successful_minor"] != "10001" {
		t.Fatalf("wrong payout totals: %+v", amount)
	}
	payoutRow, batch := payouts[0].(map[string]any), settlements[0].(map[string]any)
	if payoutRow["id"] != id || payoutRow["status"] != "succeeded" || payoutRow["recipient_id"] != nil ||
		batch["statement_fee_minor"] != "133" || batch["differences"] != float64(0) {
		t.Fatalf("wrong payout or settlement: %+v %+v", payoutRow, batch)
	}
	viewer := f.call(0, "GET", platformFinance, f.observer, "", "").object(t, 200)
	if viewer["summary"].([]any)[0].(map[string]any)["estimated_margin_minor"] != nil {
		t.Fatal("platform viewer received estimated platform margin")
	}
	other := f.call(0, "GET", platformFinance+"?merchant=M20029", f.ops, "", "").object(t, 200)
	if len(other["payouts"].([]any)) != 0 || len(other["settlements"].([]any)) != 0 {
		t.Fatal("merchant-scoped finance included another merchant")
	}
	newDraft := strings.Replace(draft, "costed-payout", "zero-priced-payout", 1)
	newOrder := f.call(0, "POST", "/v1/payouts", f.operator, "zero-priced-payout", newDraft).object(t, 201)
	assertOrderFee(t, newOrder, "default", 0)
}
