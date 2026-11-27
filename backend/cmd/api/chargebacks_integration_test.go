package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSandboxChargebackCallbacksAndWorkspace(t *testing.T) {
	root := []byte(strings.Repeat("c", 32))
	f := newControlsFixtureWithEvents(t, eventConfig{callbackRoot: root})
	create := func(key, reference string, confirm bool) map[string]any {
		t.Helper()
		order := f.call(0, "POST", "/v1/payments", key, reference,
			`{"merchant_reference":"`+reference+`","market":"US","currency":"USD","method":"card","amount_minor":1000}`).object(t, 201)
		if confirm {
			f.call(0, "POST", "/sandbox"+order["checkout_path"].(string)+"/confirm", "", "",
				`{"confirm":true}`).expect(t, 200)
		}
		return order
	}
	id := create(f.legacy, "CB-ORDER", true)["id"].(string)
	secondID := create(f.legacy, "CB-SECOND", true)["id"].(string)
	unpaid := create(f.legacy, "CB-UNPAID", false)["id"].(string)
	foreign := create(f.secondary, "CB-FOREIGN", true)
	account := f.call(0, "GET", "/v1/payments/"+id, f.legacy, "", "").object(t, 200)["route_snapshot"].(map[string]any)["account_id"].(string)
	foreignAccount := foreign["route_snapshot"].(map[string]any)["account_id"].(string)
	const records = controlWorkspace + "/chargebacks"
	path := records + "/" + account + "/case-1"
	stamp := strconv.FormatInt(time.Now().Unix(), 10)

	event := channelCallback{EventID: "cb-open", Kind: "chargeback", SubjectID: id, ChargebackID: "case-1",
		Reference: "sim_" + id, Status: "opened", Currency: "USD", Amount: 300}
	send := func(instance int, account, signer, timestamp string, input channelCallback, tamper bool) controlsResult {
		body, _ := json.Marshal(input)
		signature := sandboxTestSignature(root, "callback", signer, timestamp, body)
		if tamper {
			body = append(body, ' ')
		}
		request := httptest.NewRequest("POST", "/sandbox/channel-callbacks/"+account, strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("FlowXPay-Timestamp", timestamp)
		request.Header.Set("FlowXPay-Signature", signature)
		recorder := httptest.NewRecorder()
		f.instances[instance].ServeHTTP(recorder, request)
		return controlsResult{recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String()}
	}
	expectOutcome := func(result controlsResult, want string) {
		t.Helper()
		if result.object(t, 200)["outcome"] != want {
			t.Fatalf("wanted callback outcome %q, received %s", want, result.body)
		}
	}
	send(0, account, account, stamp, event, true).expect(t, 401)
	send(0, account, account, "0", event, false).expect(t, 401)
	send(0, foreignAccount, account, stamp, event, false).expect(t, 401)
	send(0, foreignAccount, foreignAccount, stamp, event, false).expect(t, 404)
	for _, invalid := range []struct {
		edit func(*channelCallback)
		code int
	}{
		{func(e *channelCallback) { e.SubjectID, e.Reference = unpaid, "sim_"+unpaid }, 404},
		{func(e *channelCallback) { e.Reference = "wrong-reference" }, 404},
		{func(e *channelCallback) { e.Currency = "BRL" }, 404},
		{func(e *channelCallback) { e.Amount = 1001 }, 404},
		{func(e *channelCallback) { e.ChargebackID = "" }, 400},
		{func(e *channelCallback) { e.Status = "succeeded" }, 400},
	} {
		input := event
		invalid.edit(&input)
		send(0, account, account, stamp, input, false).expect(t, invalid.code)
	}

	// An account disabled for new routing still owns its existing orders.
	f.exec(t, "UPDATE sandbox_channel_accounts SET enabled=false WHERE id=$1", account)
	snapshot := func() string {
		t.Helper()
		var state string
		err := f.db.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
			'payment',(SELECT to_jsonb(p) FROM payments p WHERE id=$1),
			'outbox',(SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM collection_outbox o),
			'payout_accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.account_id,a.currency) FROM sandbox_payout_accounts a),
			'refunds',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM sandbox_refunds r))::text`, id).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	pair := raceWorkspaceControl(func(i int) controlsResult { return send(i, account, account, stamp, event, false) })
	if pair[1].object(t, 200)["outcome"] == "applied" {
		pair[0], pair[1] = pair[1], pair[0]
	}
	expectOutcome(pair[0], "applied")
	expectOutcome(pair[1], "duplicate")

	rebound := event
	rebound.EventID, rebound.SubjectID, rebound.Reference = "cb-rebound", secondID, "sim_"+secondID
	send(1, account, account, stamp, rebound, false).expect(t, 404)
	rebound = event
	rebound.EventID, rebound.Amount = "cb-new-amount", 301
	send(1, account, account, stamp, rebound, false).expect(t, 404)
	rebound.EventID = event.EventID
	send(1, account, account, stamp, rebound, false).expect(t, 409)

	won, lost := event, event
	won.EventID, won.Status = "cb-won", "won"
	lost.EventID, lost.Status = "cb-lost", "lost"
	finals := [2]channelCallback{won, lost}
	pair = raceWorkspaceControl(func(i int) controlsResult { return send(i, account, account, stamp, finals[i], false) })
	winner, loser := 0, 1
	if pair[1].object(t, 200)["outcome"] == "applied" {
		winner, loser = 1, 0
	}
	expectOutcome(pair[winner], "applied")
	expectOutcome(pair[loser], "terminal_conflict")
	expectOutcome(send(1, account, account, stamp, finals[loser], false), "duplicate")
	late := event
	late.EventID = "cb-late-open"
	expectOutcome(send(1, account, account, stamp, late, false), "ignored")

	// A final event may be the first observation. The following open cannot regress it.
	firstFinal := lost
	firstFinal.ChargebackID, firstFinal.EventID = "case-terminal-first", "cb-terminal-first"
	expectOutcome(send(0, account, account, stamp, firstFinal, false), "applied")
	firstFinal.EventID, firstFinal.Status = "cb-open-after-final", "opened"
	expectOutcome(send(1, account, account, stamp, firstFinal, false), "ignored")

	for _, actor := range []string{f.viewer, f.operator, f.admin} {
		list := f.call(1, http.MethodGet, records, actor, "", "")
		list.expect(t, 200)
		var summaries []chargeback
		if err := json.Unmarshal([]byte(list.body), &summaries); err != nil || len(summaries) != 2 {
			t.Fatalf("wrong list length or payload: %v", err)
		}
		detail := f.call(1, http.MethodGet, path, actor, "", "").object(t, 200)
		order := detail["payment"].(map[string]any)
		if detail["status"] != finals[winner].Status || detail["payment_id"] != id || detail["merchant_reference"] != "CB-ORDER" ||
			detail["amount_minor"] != float64(300) || order["status"] != "succeeded" || order["amount_minor"] != float64(1000) {
			t.Fatal("wrong chargeback or original payment details")
		}
		var historyIDs, outcomes []string
		for _, raw := range detail["history"].([]any) {
			item := raw.(map[string]any)
			historyIDs = append(historyIDs, item["event_id"].(string))
			outcomes = append(outcomes, item["outcome"].(string))
			if (item["replay_conflict_at"] != nil) != (item["event_id"] == "cb-open") {
				t.Fatal("wrong event ID conflict attribution")
			}
		}
		if !reflect.DeepEqual(historyIDs, []string{event.EventID, finals[winner].EventID, finals[loser].EventID, late.EventID}) ||
			!reflect.DeepEqual(outcomes, []string{"applied", "applied", "terminal_conflict", "ignored"}) {
			t.Fatalf("unexpected persisted history: %v %v", historyIDs, outcomes)
		}
	}
	otherList := f.call(1, "GET", records, f.otherMerchant, "", "")
	otherList.expect(t, 200)
	if strings.TrimSpace(otherList.body) != "[]" {
		t.Fatal("cross-merchant record leak")
	}
	f.call(1, "GET", path, f.otherMerchant, "", "").expect(t, 404)
	foreignID := foreign["id"].(string)
	otherEvent := event
	otherEvent.SubjectID, otherEvent.Reference = foreignID, "sim_"+foreignID
	expectOutcome(send(1, foreignAccount, foreignAccount, stamp, otherEvent, false), "applied")
	otherPath := records + "/" + foreignAccount + "/case-1"
	if f.call(1, "GET", otherPath, f.otherMerchant, "", "").object(t, 200)["payment_id"] != foreignID {
		t.Fatal("same account-scoped IDs did not resolve to the foreign merchant's own case")
	}
	f.call(1, "GET", otherPath, f.viewer, "", "").expect(t, 404)
	f.call(1, "GET", path, f.legacy, "", "").expect(t, 401)
	f.call(1, "GET", path, "", "", "").expect(t, 401)
	f.call(1, "POST", path, f.admin, "", "{}").expect(t, 405)
	if after := snapshot(); after != before {
		t.Fatal("chargeback changed payment, refund, payout balances or outbox")
	}
	var cases, events, inbox int
	if err := f.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM sandbox_chargebacks),
		(SELECT count(*) FROM sandbox_chargeback_events), (SELECT count(*) FROM collection_inbox)`).
		Scan(&cases, &events, &inbox); err != nil || cases != 3 || events != 7 || inbox != 7 {
		t.Fatalf("wrong persistence counts: %d/%d/%d (%v)", cases, events, inbox, err)
	}
}
