package main

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPlatformPreviewSeparatesMerchantAndOperatorSessions(t *testing.T) {
	f := newControlsFixture(t)
	const dashboard = "/sandbox/ops/dashboard"
	const login = "/sandbox/ops/login"
	const logout = "/sandbox/ops/logout"
	f.instances[0] = (&server{db: f.db}).routes()
	f.call(0, "GET", dashboard, "", "", "").expect(t, http.StatusNotFound)

	const password = "synthetic-preview-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	f.instances[1] = (&server{db: f.db, platform: &platformPreview{
		accounts: map[string]platformAccount{"preview-operator": {Hash: string(hash), Role: "viewer"}}, sessions: make(map[[32]byte]platformSession),
	}}).routes()
	for _, token := range []string{"", f.legacy, f.admin} {
		f.call(1, "GET", dashboard, token, "", "").expect(t, http.StatusUnauthorized)
	}
	f.call(1, "POST", login, "", "", `{"user":"preview-operator","password":"incorrect-password"}`).expect(t, http.StatusUnauthorized)
	res := f.call(1, "POST", login, "", "", `{"user":"preview-operator","password":"`+password+`"}`).object(t, http.StatusOK)
	token := res["token"].(string)
	if res["role"] != "viewer" {
		t.Fatal("preview identity was granted an incorrect role")
	}
	f.call(1, "GET", controlWorkspace+"/session", token, "", "").expect(t, http.StatusUnauthorized)
	const ops = "/sandbox/ops/operations"
	f.call(1, "GET", ops, f.admin, "", "").expect(t, http.StatusUnauthorized)
	catalog := f.call(1, "GET", ops, token, "", "").object(t, http.StatusOK)
	integrations := catalog["upstream_integrations"].([]any)
	if len(integrations) != 1 {
		t.Fatalf("expected one pending upstream, got %d", len(integrations))
	}
	thailand := integrations[0].(map[string]any)
	if thailand["id"] != "thai-upstream" || thailand["currency"] != "THB" ||
		thailand["collections"] != true || thailand["payouts"] != true || thailand["validation_status"] != "signature_rejected" {
		t.Fatal("unexpected Thailand upstream onboarding record")
	}
	for _, value := range catalog["channels"].([]any) {
		if value.(map[string]any)["id"] == thailand["id"] {
			t.Fatal("pending upstream appeared in assignable channel catalog")
		}
	}
	if scoped := f.call(1, "GET", ops+"?merchant=M10028", token, "", "").object(t, http.StatusOK); scoped["upstream_integrations"] != nil {
		t.Fatal("merchant-scoped response must not return the platform onboarding list")
	}
	all := f.call(1, "GET", dashboard, token, "", "").object(t, http.StatusOK)
	if len(all["merchants"].([]any)) != 2 {
		t.Fatal("operator could not see both synthetic merchants")
	}
	filtered := f.call(1, "GET", dashboard+"?merchant=M10028", token, "", "").object(t, http.StatusOK)
	if len(filtered["channels"].([]any)) == 0 {
		t.Fatal("the synthetic merchant has no visible channel accounts")
	}
	for _, row := range filtered["channels"].([]any) {
		if row.(map[string]any)["merchant"] != "M10028" {
			t.Fatal("channel result did not honor merchant filter")
		}
	}
	const oldID = "pay_0123456789abcdef0123456789abcdef"
	newID := newResourceID(paymentPrefix)
	f.exec(t, `INSERT INTO payments
		(id,merchant_id,merchant_reference,idempotency_key,request_hash,market,currency,method,amount_minor,status,route)
		VALUES ($1,'M10028','SAME-REF','legacy-fixture','hash','US','USD','card',100,'requires_action','mock_card'),
		       ($2,'M20029','SAME-REF','new-fixture','hash','US','USD','card',100,'requires_action','mock_card')`, oldID, newID)
	for _, test := range []struct{ query, id string }{
		{"reference=" + oldID, oldID},
		{"reference=" + newID, newID},
		{"merchant=M10028&reference=SAME-REF", oldID},
		{"merchant=M10028&reference=" + newID, ""},
		{"reference=" + oldID[:8], ""},
	} {
		rows := f.call(1, "GET", dashboard+"?"+test.query, token, "", "").object(t, http.StatusOK)["payments"].([]any)
		if test.id == "" && len(rows) != 0 || test.id != "" && (len(rows) != 1 || rows[0].(map[string]any)["id"] != test.id) {
			t.Fatalf("wrong exact payment match for %q: %v", test.query, rows)
		}
	}
	payoutID := newResourceID(payoutPrefix)
	f.exec(t, `INSERT INTO sandbox_payout_recipients(id,merchant_id,request_key,recipient_type,test_suffix)
		VALUES('platform-payout-fixture','M10028','platform-payout-fixture','pix','1234')`)
	f.exec(t, `INSERT INTO sandbox_payouts(id,merchant_id,merchant_reference,request_key,account_id,recipient_id,
		recipient_type,amount_minor,currency,status,created_by,channel_reference)
		VALUES($1,'M10028','SAME-REF','platform-payout','default_M10028_mock_pix','platform-payout-fixture',
		 'pix',170,'BRL','draft','control-admin','platform-payout-channel-ref')`, payoutID)
	for _, reference := range []string{payoutID, "SAME-REF"} {
		matches := f.call(1, "GET", dashboard+"?direction=payout&reference="+reference, token, "", "").
			object(t, http.StatusOK)["payments"].([]any)
		if len(matches) != 1 || matches[0].(map[string]any)["id"] != payoutID {
			t.Fatalf("wrong exact payout match for %q: %v", reference, matches)
		}
	}
	matches := f.call(1, "GET", dashboard+"?direction=payout&merchant=M20029&reference=SAME-REF", token, "", "").
		object(t, http.StatusOK)["payments"].([]any)
	if len(matches) != 0 {
		t.Fatalf("payout lookup crossed the merchant filter: %v", matches)
	}
	f.call(1, "GET", dashboard+"?merchant=missing", token, "", "").expect(t, http.StatusNotFound)
	f.call(1, "POST", dashboard, token, "", "{}").expect(t, http.StatusMethodNotAllowed)
	f.call(1, "POST", logout, token, "", "").expect(t, http.StatusNoContent)
	f.call(1, "GET", dashboard, token, "", "").expect(t, http.StatusUnauthorized)
	if !strings.HasPrefix(token, "fxo_") {
		t.Fatal("platform session does not have its own namespace")
	}
}
