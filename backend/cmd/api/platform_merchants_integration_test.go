package main

import (
	"crypto/sha256"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const platformMerchantPath = "/sandbox/ops/merchants"

func onboardFixtureBody(name string) string {
	return `{"name":"` + name + `","site_name":"New default","admin_name":"First Admin","admin_email":"first@example.test"}`
}

func initialInviteToken(t *testing.T, body map[string]any) string {
	t.Helper()
	path, ok := body["invite_path"].(string)
	token, found := strings.CutPrefix(path, "/invite#fxi_")
	if !ok || !found || len(token) != 64 {
		t.Fatal("missing valid one-time invitation")
	}
	return "fxi_" + token
}

func acceptOnboarding(f controlsFixture, instance int, invitation string) controlsResult {
	return f.call(instance, "POST", controlWorkspace+"/invitations/accept", "", "",
		`{"token":"`+invitation+`","password":"synthetic-onboarding-password"}`)
}

func TestPlatformMerchantOnboardingDefaults(t *testing.T) {
	f := newControlsFixture(t)
	created := f.call(0, "POST", platformMerchantPath, f.ops, "default-names",
		`{"name":"  Juniper Shop  ","admin_email":"FIRST.ADMIN@example.test"}`).object(t, http.StatusCreated)
	replay := f.call(1, "POST", platformMerchantPath, f.ops, "default-names",
		`{"name":"Juniper Shop","admin_email":"first.admin@example.test","site_name":"Juniper Shop","admin_name":"first.admin"}`).object(t, http.StatusOK)
	if replay["merchant_id"] != created["merchant_id"] || replay["invite_path"] != nil || replay["api_key"] != nil {
		t.Fatal("explicit defaults did not safely replay the original request")
	}
	var siteName, adminName string
	if err := f.db.QueryRowContext(t.Context(), `SELECT s.name,i.name FROM sandbox_merchant_onboardings o
		JOIN merchant_sites s ON s.id=o.site_id JOIN merchant_invitations i ON i.token_hash=o.invite_hash
		WHERE o.merchant_id=$1`, created["merchant_id"]).Scan(&siteName, &adminName); err != nil ||
		siteName != "Juniper Shop" || adminName != "first.admin" {
		t.Fatalf("onboarding did not store normalized defaults: %v", err)
	}
}

func TestPlatformMerchantOnboarding(t *testing.T) {
	f := newControlsFixture(t)
	const name = "Quartz Commerce"
	payload := onboardFixtureBody(name)
	lookup := func(path string, token string) map[string]any {
		t.Helper()
		return f.call(0, "GET", path, token, "", "").object(t, http.StatusOK)
	}

	f.call(0, "POST", platformMerchantPath, f.observer, "open-1", payload).expect(t, http.StatusForbidden)
	for _, token := range []string{f.admin, f.otherMerchant, f.legacy, ""} {
		f.call(0, "POST", platformMerchantPath, token, "open-1", payload).expect(t, http.StatusUnauthorized)
	}
	f.call(0, "POST", platformMerchantPath, f.ops, "", payload).expect(t, http.StatusBadRequest)
	f.call(0, "POST", platformMerchantPath, f.ops, "invalid", `{"name":"  ","admin_email":"wrong"}`).expect(t, http.StatusBadRequest)

	assertCount := func(want int, query string, args ...any) {
		t.Helper()
		var count int
		if err := f.db.QueryRowContext(t.Context(), query, args...).Scan(&count); err != nil || count != want {
			t.Fatalf("expected %d rows, got %d (query error: %v)", want, count, err)
		}
	}
	for _, failure := range []struct{ table, check string }{
		{"merchant_api_keys", "label <> 'Initial sandbox key'"},
		{"sandbox_platform_audit", "action <> 'key.issue'"},
		{"sandbox_platform_audit", "action <> 'merchant.onboard'"},
	} {
		f.exec(t, `ALTER TABLE `+failure.table+` ADD CONSTRAINT test_onboarding_failure CHECK (`+failure.check+`)`)
		f.call(0, "POST", platformMerchantPath, f.ops, "open-1", payload).expect(t, http.StatusServiceUnavailable)
		assertCount(0, `SELECT count(*) FROM merchants WHERE name=$1`, name)
		assertCount(0, `SELECT count(*) FROM merchant_sites WHERE name='New default'`)
		assertCount(0, `SELECT count(*) FROM merchant_invitations WHERE email='first@example.test'`)
		assertCount(0, `SELECT count(*) FROM sandbox_merchant_onboardings WHERE request_key='open-1'`)
		assertCount(0, `SELECT count(*) FROM merchant_api_keys WHERE label='Initial sandbox key'`)
		assertCount(0, `SELECT count(*) FROM sandbox_platform_audit WHERE action IN ('key.issue','merchant.onboard')`)
		f.exec(t, `ALTER TABLE `+failure.table+` DROP CONSTRAINT test_onboarding_failure`)
	}
	f.exec(t, `INSERT INTO merchants SELECT 'A' || lpad(i::text, 3, '0'), 'Directory fixture' FROM generate_series(1,120) t(i)`)

	results := raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", platformMerchantPath, f.ops, "open-1", payload)
	})
	if results[1].status == http.StatusCreated {
		results[0], results[1] = results[1], results[0]
	}
	created := results[0].object(t, http.StatusCreated)
	replay := results[1].object(t, http.StatusOK)
	invitation := initialInviteToken(t, created)
	merchant, siteID := created["merchant_id"].(string), created["site_id"].(string)
	if !regexp.MustCompile(`^M[2-9A-HJ-NP-Z]{12}$`).MatchString(merchant) ||
		!regexp.MustCompile(`^site_[2-9A-HJ-NP-Z]{24}$`).MatchString(siteID) {
		t.Fatalf("unexpected onboarding numbers: %q / %q", merchant, siteID)
	}
	key := created["api_key"].(map[string]any)
	keyID, secret := key["id"].(string), key["secret"].(string)
	digest, valid := authTokenDigest(secret, apiKeyPrefix)
	if !valid || key["platform"] != true || key["legacy"] != false || key["created_by"] != "ops-submit" {
		t.Fatal("initial key is not a valid platform-managed sandbox key")
	}
	if replay["merchant_id"] != merchant || replay["site_id"] != siteID || replay["invite_path"] != nil || replay["api_key"] != nil ||
		merchant == "M10028" || merchant == "M20029" {
		t.Fatal("creation or idempotency response has the wrong scope")
	}
	f.call(0, "POST", platformMerchantPath, f.ops, "open-1", onboardFixtureBody("Conflict")).expect(t, http.StatusConflict)
	assertCount(1, `SELECT count(*) FROM merchants WHERE name=$1`, name)
	assertCount(1, `SELECT count(*) FROM merchant_sites WHERE id=$1 AND merchant_id=$2 AND is_default`, siteID, merchant)
	assertCount(1, `SELECT count(*) FROM merchant_invitations WHERE merchant_id=$1 AND role='admin' AND consumed_at IS NULL`, merchant)
	assertCount(1, `SELECT count(*) FROM sandbox_platform_audit WHERE merchant_id=$1 AND action='merchant.onboard'`, merchant)
	assertCount(0, `SELECT count(*) FROM merchant_members WHERE merchant_id=$1`, merchant)
	assertCount(0, `SELECT count(*) FROM sandbox_channel_accounts WHERE merchant_id=$1`, merchant)
	assertCount(1, `SELECT count(*) FROM merchant_api_keys WHERE merchant_id=$1`, merchant)
	assertCount(1, `SELECT count(*) FROM merchant_api_keys WHERE merchant_id=$1 AND id=$2 AND key_hash=$3
		AND platform_created_by='ops-submit' AND created_by IS NULL AND revoked_at IS NULL`, merchant, keyID, digest[:])
	assertCount(1, `SELECT count(*) FROM sandbox_platform_audit WHERE merchant_id=$1 AND action='key.issue' AND resource_id=$2`, merchant, keyID)
	if lookup("/v1/capabilities", secret)["merchant"].(map[string]any)["id"] != merchant {
		t.Fatal("initial key changed merchant scope")
	}
	assertCount(0, `SELECT count(*) FROM sandbox_payout_accounts WHERE merchant_id=$1`, merchant)

	first := lookup(platformMerchantPath, f.observer)
	if len(first["merchants"].([]any)) != 100 {
		t.Fatal("expected a bounded first page")
	}
	page2 := lookup(platformMerchantPath+"?after="+first["next_after"].(string), f.observer)
	if len(page2["merchants"].([]any)) != 23 || page2["next_after"] != nil {
		t.Fatal("merchant directory cursor missed rows")
	}
	for _, query := range []string{"quartz commerce", strings.ToLower(merchant)} {
		found := lookup(platformMerchantPath+"?q="+url.QueryEscape(query), f.observer)["merchants"].([]any)
		if len(found) != 1 || found[0].(map[string]any)["id"] != merchant {
			t.Fatal("lookup failed outside the first page")
		}
	}
	dashboard := lookup("/sandbox/ops/dashboard?merchant="+merchant, f.observer)
	if dashboard["merchants"].([]any)[0].(map[string]any)["id"] != merchant || len(dashboard["payments"].([]any)) != 0 {
		t.Fatal("dashboard lost its selected merchant or leaked existing payments")
	}
	operations := "/sandbox/ops/operations?merchant=" + merchant
	op := lookup(operations, f.observer)
	keys := op["keys"].([]any)
	if op["onboarding"].(map[string]any)["invite_path"] != nil || len(op["sites"].([]any)) != 1 || len(keys) != 1 ||
		keys[0].(map[string]any)["id"] != keyID || keys[0].(map[string]any)["secret"] != nil {
		t.Fatal("operations exposed invitation or unrelated data")
	}

	reissue := platformMerchantPath + "/" + merchant + "/admin-invitation"
	const version1 = `{"invite_version":1}`
	f.call(0, "POST", reissue, f.observer, "replace-1", version1).expect(t, http.StatusForbidden)
	f.call(0, "POST", reissue, f.admin, "replace-1", version1).expect(t, http.StatusUnauthorized)
	f.call(0, "POST", platformMerchantPath+"/M10028/admin-invitation", f.ops, "replace-1", version1).expect(t, http.StatusNotFound)

	f.exec(t, `UPDATE merchant_invitations SET expires_at=clock_timestamp()-interval '1 hour' WHERE merchant_id=$1`, merchant)
	if lookup(operations, f.observer)["onboarding"].(map[string]any)["invite_status"] != "expired" {
		t.Fatal("expired invitation is not recoverable from Operations")
	}
	results = raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", reissue, f.ops, "replace-1", version1)
	})
	if results[1].status == http.StatusCreated {
		results[0], results[1] = results[1], results[0]
	}
	reissued := results[0].object(t, http.StatusCreated)
	replacement := initialInviteToken(t, reissued)
	if reissued["api_key"] != nil {
		t.Fatal("reissuing an invitation returned a key")
	}
	if results[1].object(t, http.StatusOK)["invite_path"] != nil {
		t.Fatal("replacement replay exposed a one-time link")
	}
	f.call(0, "POST", reissue, f.ops, "replace-1", `{"invite_version":2}`).expect(t, http.StatusConflict)
	f.call(0, "POST", reissue, f.ops, "stale", version1).expect(t, http.StatusConflict)

	results = raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", reissue, f.ops, "replace-2-"+strconv.Itoa(i), `{"invite_version":2}`)
	})
	finalInvite := initialInviteToken(t, workspaceRaceWinner(t, results, http.StatusCreated))
	assertCount(1, `SELECT count(*) FROM merchant_invitations WHERE merchant_id=$1 AND consumed_at IS NULL AND expires_at > clock_timestamp()`, merchant)
	for _, old := range []string{invitation, replacement} {
		acceptOnboarding(f, 0, old).expect(t, http.StatusBadRequest)
	}
	acceptOnboarding(f, 0, finalInvite).expect(t, http.StatusCreated)
	acceptOnboarding(f, 1, finalInvite).expect(t, http.StatusBadRequest)
	f.call(0, "POST", reissue, f.ops, "after-accept", `{"invite_version":3}`).expect(t, http.StatusConflict)
	assertCount(2, `SELECT count(*) FROM sandbox_platform_audit WHERE merchant_id=$1 AND action='merchant.invite_reissue'`, merchant)
	assertCount(1, `SELECT count(*) FROM merchant_api_keys WHERE merchant_id=$1`, merchant)
	assertCount(1, `SELECT count(*) FROM merchant_api_keys WHERE merchant_id=$1 AND id=$2 AND key_hash=$3 AND revoked_at IS NULL`, merchant, keyID, digest[:])
	assertCount(0, `SELECT count(*) FROM sandbox_platform_audit WHERE merchant_id=$1 AND
		(details ? 'invite_path' OR details ? 'secret' OR strpos(details::text,$2)>0 OR strpos(details::text,$3)>0
		OR details::text LIKE '%example.test%')`, merchant, invitation, secret)
	assertCount(1, `SELECT count(*) FROM merchant_credentials c JOIN merchant_members m ON c.member_id=m.id
		WHERE c.merchant_id=$1 AND c.email='first@example.test' AND m.role='admin'`, merchant)
	login := func(id string, expected int) map[string]any {
		return f.call(0, "POST", controlWorkspace+"/auth/login", "", "",
			`{"merchant_id":"`+id+`","email":"first@example.test","password":"synthetic-onboarding-password"}`).object(t, expected)
	}
	admin := login(merchant, http.StatusOK)["session_token"].(string)
	for _, id := range []string{"M10028", "M20029"} {
		login(id, http.StatusUnauthorized)
	}
	f.call(0, "POST", platformMerchantPath, admin, "forbidden", payload).expect(t, http.StatusUnauthorized)
	if lookup(controlWorkspace+"/session", admin)["merchant_id"] != merchant {
		t.Fatal("password authentication changed merchant scope")
	}
	keyPath := platformMerchantPath + "/" + merchant + "/api-keys"
	f.call(0, "POST", controlWorkspace+"/api-keys/"+keyID+"/revoke", admin, "", "").expect(t, http.StatusForbidden)
	f.call(0, "POST", platformMerchantPath+"/M20029/api-keys/"+keyID+"/revoke", f.ops, "", "").expect(t, http.StatusNotFound)
	f.call(0, "POST", keyPath+"/"+keyID+"/revoke", f.ops, "", "").expect(t, http.StatusOK)
	f.call(0, "GET", "/v1/capabilities", secret, "", "").expect(t, http.StatusUnauthorized)
	f.call(0, "POST", keyPath, f.ops, "", `{"label":"Replacement sandbox key"}`).expect(t, http.StatusCreated)
	replay = f.call(0, "POST", platformMerchantPath, f.ops, "open-1", payload).object(t, http.StatusOK)
	if replay["merchant_id"] != merchant || replay["invite_path"] != nil || replay["api_key"] != nil {
		t.Fatal("replay after replacement exposed a credential")
	}
	assertCount(2, `SELECT count(*) FROM merchant_api_keys WHERE merchant_id=$1`, merchant)
}

func TestPlatformMerchantOnboardingReplayBeforeInitialKeys(t *testing.T) {
	f := newControlsFixture(t)
	const merchant, site = "MHistorical", "site_historical"
	payload := onboardFixtureBody("Historical")
	hash := sha256.Sum256([]byte(payload))
	_, inviteHash := newAuthToken("fxi_")
	f.exec(t, `INSERT INTO merchants (id,name) VALUES ($1,'Historical')`, merchant)
	f.exec(t, `INSERT INTO merchant_sites (id,merchant_id,name,is_default) VALUES ($1,$2,'New default',true)`, site, merchant)
	f.exec(t, `INSERT INTO merchant_invitations (token_hash,merchant_id,email,name,role,expires_at)
		VALUES ($1,$2,'first@example.test','First Admin','admin',clock_timestamp()+interval '1 hour')`, inviteHash[:], merchant)
	f.exec(t, `INSERT INTO sandbox_merchant_onboardings (merchant_id,request_key,request_hash,site_id,invite_hash)
		VALUES ($1,'historical-onboarding',$2,$3,$4)`, merchant, hash[:], site, inviteHash[:])

	replay := f.call(0, "POST", platformMerchantPath, f.ops, "historical-onboarding", payload).object(t, http.StatusOK)
	if replay["merchant_id"] != merchant || replay["site_id"] != site || replay["invite_path"] != nil || replay["api_key"] != nil {
		t.Fatal("historical onboarding replay changed shape or scope")
	}
	reissue := platformMerchantPath + "/" + merchant + "/admin-invitation"
	issued := f.call(0, "POST", reissue, f.ops, "historical-reissue", `{"invite_version":1}`).object(t, http.StatusCreated)
	if issued["api_key"] != nil {
		t.Fatal("reissuing a historical invitation returned a key")
	}
	op := f.call(0, "GET", "/sandbox/ops/operations?merchant="+merchant, f.observer, "", "").object(t, http.StatusOK)
	if len(op["keys"].([]any)) != 0 {
		t.Fatal("historical onboarding received an unrequested key")
	}
}

func TestPlatformMerchantInvitationAcceptAndReplaceRace(t *testing.T) {
	f := newControlsFixture(t)
	created := f.call(0, "POST", platformMerchantPath, f.ops, "accept-race", onboardFixtureBody("Race")).object(t, http.StatusCreated)
	merchant := created["merchant_id"].(string)
	token := initialInviteToken(t, created)
	path := platformMerchantPath + "/" + merchant + "/admin-invitation"
	results := raceWorkspaceControl(func(i int) controlsResult {
		if i == 0 {
			return acceptOnboarding(f, i, token)
		}
		return f.call(i, "POST", path, f.ops, "replace-race", `{"invite_version":1}`)
	})
	if results[0].status == http.StatusCreated {
		results[1].expect(t, http.StatusConflict)
	} else {
		results[0].expect(t, http.StatusBadRequest)
		next := initialInviteToken(t, results[1].object(t, http.StatusCreated))
		acceptOnboarding(f, 1, next).expect(t, http.StatusCreated)
	}
	f.call(0, "POST", path, f.ops, "replace-after-race", `{"invite_version":1}`).expect(t, http.StatusConflict)
	var admins int
	if err := f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM merchant_members WHERE merchant_id=$1 AND role='admin'`, merchant).Scan(&admins); err != nil || admins != 1 {
		t.Fatal("accept and replacement race did not preserve exactly one administrator")
	}
}
