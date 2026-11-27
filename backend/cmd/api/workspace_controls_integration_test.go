package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type controlsResult struct {
	status int
	cache  string
	body   string
}

func (r controlsResult) object(t *testing.T, status int) map[string]any {
	t.Helper()
	r.expect(t, status)
	var data map[string]any
	if err := json.Unmarshal([]byte(r.body), &data); err != nil {
		t.Fatal("expected JSON object from workspace control")
	}
	return data
}

func (r controlsResult) expect(t *testing.T, status int) {
	t.Helper()
	if r.status != status || r.cache != "no-store" {
		// Creation and rotation may return a secret. Never print the raw response.
		t.Fatalf("expected status %d and no-store, got status %d and cache policy %q", status, r.status, r.cache)
	}
}

type controlsFixture struct {
	db                                           *sql.DB
	instances                                    [2]http.Handler
	legacy, secondary                            string
	admin, reviewer, secondAdmin                 string
	operator, viewer, otherMerchant              string
	otherReviewer                                string
	ops, opsAgain, approver, approver2, observer string
}

const controlWorkspace = "/sandbox/workspace"

func newControlsFixture(t *testing.T) controlsFixture {
	return newControlsFixtureWithEvents(t, eventConfig{})
}

func newControlsFixtureWithEvents(t *testing.T, events eventConfig) controlsFixture {
	t.Helper()
	url := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("run scripts/local-sandbox.sh test to create an isolated PostgreSQL instance")
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL configuration")
	}
	baseDB := stdlib.OpenDB(*config)
	defer baseDB.Close()
	schema := "controls_" + strings.ToLower(rand.Text())
	if _, err := baseDB.ExecContext(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	primaryDB, secondaryDB := stdlib.OpenDB(*config), stdlib.OpenDB(*config)
	t.Cleanup(func() { primaryDB.Close(); secondaryDB.Close() })
	if err := migrations.Apply(t.Context(), primaryDB); err != nil {
		t.Fatal(err)
	}
	platform := &platformPreview{sessions: make(map[[32]byte]platformSession)}
	f := controlsFixture{db: primaryDB, legacy: strings.Repeat("a", 32), secondary: strings.Repeat("b", 32)}
	for i, db := range []*sql.DB{primaryDB, secondaryDB} {
		s := &server{db: db, platform: platform, events: events, signing: []byte(strings.Repeat("s", 32)), collection: sandboxCollection{db: db},
			merchants: [2]sandboxMerchant{{ID: "M10028", Key: f.legacy}, {ID: "M20029", Key: f.secondary}}}
		f.instances[i] = s.routes()
	}
	for _, actor := range []struct {
		id, merchant, role string
		token              *string
	}{
		{"control-admin", "M10028", "admin", &f.admin},
		{"control-reviewer", "M10028", "admin", &f.reviewer},
		{"control-reviewer-2", "M10028", "admin", &f.otherReviewer},
		{"control-operator", "M10028", "operator", &f.operator},
		{"control-viewer", "M10028", "viewer", &f.viewer},
		{"control-other", "M20029", "admin", &f.otherMerchant},
	} {
		f.exec(t, "INSERT INTO merchant_members (id, merchant_id, name, role) VALUES ($1,$2,$1,$3)", actor.id, actor.merchant, actor.role)
		*actor.token = f.session(t, actor.id)
	}
	f.secondAdmin = f.session(t, "control-admin")
	for _, actor := range []struct {
		name, role string
		token      *string
	}{
		{"ops-submit", "operator", &f.ops}, {"ops-submit", "operator", &f.opsAgain},
		{"ops-approve", "operator", &f.approver}, {"ops-approve-2", "operator", &f.approver2},
		{"ops-observer", "viewer", &f.observer},
	} {
		token, digest := newAuthToken(platformSessionPrefix)
		platform.sessions[digest] = platformSession{User: actor.name, Role: actor.role, expires: time.Now().Add(time.Hour)}
		*actor.token = token
	}
	return f
}

func (f controlsFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f controlsFixture) session(t *testing.T, actor string) string {
	t.Helper()
	token, digest := newAuthToken("fxs_")
	f.exec(t, `INSERT INTO merchant_sessions (token_hash, member_id, expires_at)
		VALUES ($1,$2,clock_timestamp()+interval '1 hour')`, digest[:], actor)
	return token
}

func (f controlsFixture) call(instance int, method, path, token, key, payload string) controlsResult {
	r := httptest.NewRequest(method, path, strings.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Merchant-ID", "M10028")
	r.Header.Set("X-Role", "admin")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.instances[instance].ServeHTTP(w, r)
	return controlsResult{w.Code, w.Header().Get("Cache-Control"), w.Body.String()}
}

func raceWorkspaceControl(operation func(int) controlsResult) [2]controlsResult {
	var results [2]controlsResult
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		group.Go(func() {
			<-start
			results[i] = operation(i)
		})
	}
	close(start)
	group.Wait()
	return results
}

func workspaceRaceWinner(t *testing.T, results [2]controlsResult, want int) map[string]any {
	t.Helper()
	if results[1].status == want {
		results[0], results[1] = results[1], results[0]
	}
	results[1].expect(t, 409)
	return results[0].object(t, want)
}

func TestSandboxPlatformAPIKeyLifecycle(t *testing.T) {
	f := newControlsFixture(t)
	const metadata = controlWorkspace + "/api-keys"
	const namespace = "/sandbox/ops/merchants/"
	const keys = namespace + "M10028/api-keys"
	capabilities := func(token string, status int) map[string]any {
		t.Helper()
		return f.call(1, "GET", "/v1/capabilities", token, "", "").object(t, status)
	}
	capabilities(f.legacy, 200)
	capabilities(f.secondary, 200)
	for _, token := range []string{f.admin, f.viewer, f.operator, f.otherMerchant} {
		f.call(0, "POST", metadata, token, "", `{"label":"Forbidden"}`).expect(t, 403)
		f.call(0, "POST", keys, token, "", `{"label":"Forbidden"}`).expect(t, 401)
	}
	f.call(0, "POST", keys, f.observer, "", `{"label":"Forbidden"}`).expect(t, 403)
	f.call(0, "POST", keys+"/legacy_M10028/revoke", f.ops, "", "").expect(t, 403)
	// Keys issued by the pre-existing merchant workspace continue to authenticate.
	previous, previousDigest := newAuthToken(apiKeyPrefix)
	f.exec(t, `INSERT INTO merchant_api_keys (id,merchant_id,label,key_hash,created_at,created_by)
 VALUES ('prior-key','M10028','Prior key',$1,now(),'control-admin')`, previousDigest[:])
	f.call(0, "POST", keys+"/prior-key/revoke", f.ops, "", "").expect(t, 403)
	capabilities(previous, 200)
	created := workspaceRaceWinner(t, raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", keys, f.ops, "", `{"label":" Shipments "}`)
	}), 201)
	id, secret := created["id"].(string), created["secret"].(string)
	if created["label"] != "Shipments" || created["created_by"] != "ops-submit" || created["platform"] != true {
		t.Fatal("key does not have the platform actor and label")
	}
	f.call(0, "POST", keys, f.approver, "", `{"label":"shipments"}`).expect(t, 409)
	digest := sha256.Sum256([]byte(secret))
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT key_hash FROM merchant_api_keys WHERE id=$1", id).Scan(&stored); err != nil || string(stored) != string(digest[:]) {
		t.Fatal("expected SHA-256 digest")
	}
	if capabilities(secret, 200)["merchant"].(map[string]any)["id"] != "M10028" {
		t.Fatal("issued key authenticated for the wrong merchant")
	}
	f.call(0, "GET", metadata, secret, "", "").expect(t, 401)
	f.call(0, "POST", metadata+"/"+id+"/rotate", f.admin, "", "").expect(t, 403)
	f.call(0, "POST", metadata+"/"+id+"/revoke", f.admin, "", "").expect(t, 403)
	f.call(0, "POST", keys+"/"+id+"/revoke", f.observer, "", "").expect(t, 403)
	foreign := f.call(1, "GET", metadata, f.otherMerchant, "", "")
	foreign.expect(t, 200)
	if strings.Contains(foreign.body, id) {
		t.Fatal("cross-merchant key metadata exposed")
	}
	f.call(0, "POST", namespace+"M20029/api-keys/"+id+"/revoke", f.ops, "", "").expect(t, 404)
	revoked := f.call(1, "POST", keys+"/"+id+"/revoke", f.approver, "", "").object(t, 200)
	replay := f.call(0, "POST", keys+"/"+id+"/revoke", f.ops, "", "").object(t, 200)
	if revoked["revoked_by"] != "ops-approve" || replay["revoked_by"] != revoked["revoked_by"] || replay["revoked_at"] != revoked["revoked_at"] {
		t.Fatal("repeat revocation changed original audit")
	}
	if err := migrations.Apply(t.Context(), f.db); err != nil {
		t.Fatal(err)
	}
	capabilities(secret, 401)
	capabilities(previous, 200)
	capabilities(f.legacy, 200)
	capabilities(f.secondary, 200)
	for _, result := range []controlsResult{f.call(1, "GET", metadata, f.admin, "", ""), f.call(0, "GET", "/sandbox/ops/operations?merchant=M10028", f.observer, "", "")} {
		result.expect(t, 200)
		if strings.Contains(result.body, secret) || strings.Contains(result.body, `"secret"`) || strings.Contains(result.body, "key_hash") {
			t.Fatal("read exposes credential material")
		}
	}
	var auditCount int
	if err := f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sandbox_platform_audit
 WHERE merchant_id='M10028' AND resource_id=$1 AND ((actor='ops-submit' AND action='key.issue') OR (actor='ops-approve' AND action='key.revoke'))`, id).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatal("expected one issuance and one revocation audit")
	}
	f.exec(t, `ALTER TABLE sandbox_platform_audit ADD CONSTRAINT synthetic_key_audit_failure CHECK
		(resource_id <> 'blocked-key' AND details->>'label' <> 'Cannot audit')`)
	f.call(0, "POST", keys, f.ops, "", `{"label":"Cannot audit"}`).expect(t, 503)
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM merchant_api_keys WHERE label='Cannot audit'").Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatal("issuance persisted without platform audit")
	}
	blocked, blockedDigest := newAuthToken(apiKeyPrefix)
	f.exec(t, `INSERT INTO merchant_api_keys (id,merchant_id,label,key_hash,created_at,platform_created_by)
		VALUES ('blocked-key','M10028','Blocked revocation',$1,now(),'ops-submit')`, blockedDigest[:])
	f.call(0, "POST", keys+"/blocked-key/revoke", f.ops, "", "").expect(t, 503)
	capabilities(blocked, 200)
}

func TestSandboxMerchantAPIKeyRotation(t *testing.T) {
	f := newControlsFixture(t)
	const keys = controlWorkspace + "/api-keys"
	const oldID = "prior-key"
	const prior = keys + "/" + oldID
	previous, digest := newAuthToken(apiKeyPrefix)
	f.exec(t, `INSERT INTO merchant_api_keys (id,merchant_id,label,key_hash,created_at,created_by)
		VALUES ($1,'M10028','Prior key',$2,now(),'control-admin')`, oldID, digest[:])
	for _, route := range []string{"/rotate", "/revoke"} {
		for _, token := range []string{f.viewer, f.operator} {
			f.call(0, "POST", prior+route, token, "", "{}").expect(t, 403)
		}
		f.call(0, "POST", prior+route, f.otherMerchant, "", "{}").expect(t, 404)
		f.call(0, "POST", prior+route, f.ops, "", "{}").expect(t, 401)
	}
	order := f.call(0, "POST", "/v1/payments", previous, "prior-order",
		`{"merchant_reference":"prior-order","market":"IN","currency":"INR","method":"gateway","amount_minor":100}`).object(t, 201)
	paymentPath := "/v1/payments/" + order["id"].(string)
	before := f.call(0, "GET", paymentPath, previous, "", "")
	before.expect(t, 200)

	// A platform key occupies the requested label. The failed rotation must preserve the old key.
	f.call(0, "POST", "/sandbox/ops/merchants/M10028/api-keys", f.ops, "", `{"label":"Duplicate label"}`).expect(t, 201)
	f.call(1, "POST", prior+"/rotate", f.reviewer, "", `{"label":"duplicate label"}`).expect(t, 409)
	f.call(1, "GET", paymentPath, previous, "", "").expect(t, 200)

	rotated := workspaceRaceWinner(t, raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", prior+"/rotate", f.reviewer, "", "{}")
	}), 201)
	id, secret := rotated["id"].(string), rotated["secret"].(string)
	if rotated["label"] != "Prior key" || rotated["replaces_id"] != oldID || rotated["created_by"] != "control-reviewer" || rotated["platform"] != false {
		t.Fatal("rotation lost label, predecessor or merchant actor")
	}
	f.call(0, "GET", paymentPath, previous, "", "").expect(t, 401)
	after := f.call(1, "GET", paymentPath, secret, "", "")
	after.expect(t, 200)
	if before.body != after.body {
		t.Fatal("rotation changed the existing payment")
	}
	f.call(1, "GET", paymentPath, f.secondary, "", "").expect(t, 404)
	predecessor := f.call(0, "POST", prior+"/revoke", f.admin, "", "").object(t, 200)
	if predecessor["revoked_by"] != "control-reviewer" {
		t.Fatal("rotation did not preserve the retiring merchant actor")
	}
	revoked := f.call(1, "POST", keys+"/"+id+"/revoke", f.admin, "", "{}").object(t, 200)
	replay := f.call(0, "POST", keys+"/"+id+"/revoke", f.reviewer, "", "").object(t, 200)
	if revoked["revoked_by"] != "control-admin" || replay["revoked_by"] != revoked["revoked_by"] || replay["revoked_at"] != revoked["revoked_at"] {
		t.Fatal("merchant revoke replay changed original attribution")
	}
	f.call(0, "GET", paymentPath, secret, "", "").expect(t, 401)
	f.call(0, "GET", paymentPath, f.legacy, "", "").expect(t, 200)

	// The original environment-backed key also supports a one-time replacement.
	legacyReplacement := f.call(0, "POST", keys+"/legacy_M10028/rotate", f.admin, "", "").object(t, 201)
	if legacyReplacement["replaces_id"] != "legacy_M10028" || legacyReplacement["platform"] != false {
		t.Fatal("legacy replacement lost merchant ownership")
	}
	f.call(1, "GET", paymentPath, f.legacy, "", "").expect(t, 401)
	f.call(1, "GET", paymentPath, legacyReplacement["secret"].(string), "", "").expect(t, 200)
	f.call(1, "GET", "/v1/capabilities", f.secondary, "", "").expect(t, 200)
	list := f.call(1, "GET", keys, f.admin, "", "")
	list.expect(t, 200)
	if strings.Contains(list.body, secret) || strings.Contains(list.body, legacyReplacement["secret"].(string)) || strings.Contains(list.body, "key_hash") {
		t.Fatal("merchant list exposes credential material")
	}
}

func TestSandboxPlatformRouteApproval(t *testing.T) {
	f := newControlsFixture(t)
	const merchant = "/sandbox/ops/merchants/M10028"
	const routes = merchant + "/routes"
	const merchantRoutes = controlWorkspace + "/route-changes"
	const site = "default_M10028"
	const original = "default_M10028_mock_in_redirect"
	paymentCall := func(instance int, ref, key string, status int) map[string]any {
		t.Helper()
		payload := fmt.Sprintf(`{"merchant_reference":%q,"market":"IN","currency":"INR","method":"gateway","amount_minor":100}`, ref)
		return f.call(instance, "POST", "/v1/payments", f.legacy, key, payload).object(t, status)
	}
	assertRoute := func(order map[string]any, account, change string) {
		t.Helper()
		snapshot := order["route_snapshot"].(map[string]any)
		if snapshot["account_id"] != account || (change == "" && snapshot["route_change_id"] != nil) ||
			(change != "" && snapshot["route_change_id"] != change) {
			t.Fatalf("expected account %s and route change %q, got snapshot %+v", account, change, snapshot)
		}
	}
	bodyFor := func(account string) string {
		return fmt.Sprintf(`{"site_id":%q,"account_id":%q}`, site, account)
	}
	// A higher-priority alternative under the old fallback ordering cannot replace
	// an existing route simply by being added to the site.
	f.exec(t, `INSERT INTO merchant_sites (id, merchant_id, name) VALUES ('legacy-in-qr', 'M10028', 'Legacy India QR')`)
	f.exec(t, `INSERT INTO sandbox_channel_accounts (id, merchant_id, site_id, provider)
		VALUES ('legacy-in-qr-account', 'M10028', 'legacy-in-qr', 'mock_in_qr')`)
	channel := func(id, provider string) {
		t.Helper()
		body := fmt.Sprintf(`{"id":%q,"name":%q,"provider":%q,"enabled":true}`, id, id, provider)
		f.call(0, "POST", "/sandbox/ops/channels", f.ops, "", body).expect(t, 201)
	}
	channel("redirect", "mock_in_redirect")
	channel("qr", "mock_in_qr")
	channel("unknown", "mock_in_unknown")
	const assignment = merchant + "/assignments"
	assign := func(site, channel string) string {
		t.Helper()
		body := fmt.Sprintf(`{"site_id":%q,"channel_id":%q}`, site, channel)
		setup := raceWorkspaceControl(func(i int) controlsResult { return f.call(i, "POST", assignment, f.ops, "", body) })
		if setup[1].status == 201 {
			setup[0], setup[1] = setup[1], setup[0]
		}
		first, second := setup[0].object(t, 201), setup[1].object(t, 200)
		if first["id"] != second["id"] {
			t.Fatal("concurrent assignment duplicated the account")
		}
		return first["id"].(string)
	}
	f.call(0, "POST", assignment, f.ops, "", `{"site_id":"default_M20029","channel_id":"qr"}`).expect(t, 404)
	f.call(0, "POST", "/sandbox/ops/channels", f.observer, "", `{}`).expect(t, 403)
	f.call(0, "POST", "/sandbox/ops/channels", f.ops, "", `{"id":"live","name":"Invalid","provider":"live_pix","enabled":true}`).expect(t, 400)
	standby := assign("legacy-in-qr", "redirect")
	var isStandby bool
	if err := f.db.QueryRowContext(t.Context(), "SELECT routing_standby FROM sandbox_channel_accounts WHERE id=$1", standby).Scan(&isStandby); err != nil || !isStandby {
		t.Fatal("higher-priority alternative was not provisioned as standby")
	}
	compat := f.call(1, "POST", "/v1/payments", f.legacy, "legacy-fallback-route",
		`{"merchant_reference":"legacy-fallback","site_id":"legacy-in-qr","market":"IN","currency":"INR","method":"gateway","amount_minor":100}`).object(t, 201)
	assertRoute(compat, "legacy-in-qr-account", "")

	qr := assign(site, "qr")
	first := paymentCall(0, "route-before", "route-before", 201)
	assertRoute(first, original, "")
	f.call(0, "POST", routes, f.legacy, "", bodyFor(qr)).expect(t, 401)
	f.call(0, "POST", routes, f.admin, "", bodyFor(qr)).expect(t, 401)
	f.call(0, "POST", routes, f.observer, "", bodyFor(qr)).expect(t, 403)
	f.call(0, "POST", merchantRoutes, f.admin, "", bodyFor(qr)).expect(t, 403)
	f.call(0, "GET", merchantRoutes, f.viewer, "", "").expect(t, 200)
	f.call(1, "POST", routes, f.ops, "", bodyFor("default_M20029_mock_in_redirect")).expect(t, 404)
	f.call(0, "GET", controlWorkspace+"/channel-accounts/"+qr, f.otherMerchant, "", "").expect(t, 404)
	// An older merchant proposal remains visible as history and cannot block new platform work.
	f.exec(t, `INSERT INTO collection_route_changes (id,merchant_id,site_id,account_id,market,currency,method,submitted_by)
 VALUES ('historical-route','M10028',$1,$2,'IN','INR','gateway','control-admin')`, site, original)
	f.call(0, "POST", routes+"/historical-route/approve", f.approver, "", "").expect(t, 404)

	pending := workspaceRaceWinner(t, raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", routes, f.ops, "", bodyFor(qr))
	}), 201)
	changeID := pending["id"].(string)
	if pending["submitted_by"] != "ops-submit" || pending["market"] != "IN" || pending["approved_at"] != nil {
		t.Fatal("invalid pending route audit")
	}
	approve := routes + "/" + changeID + "/approve"
	assertRoute(paymentCall(1, "route-pending", "route-pending", 201), original, "")
	f.call(0, "POST", merchantRoutes+"/"+changeID+"/approve", f.operator, "", "").expect(t, 403)
	f.call(0, "POST", approve, f.observer, "", "").expect(t, 403)
	f.call(0, "POST", "/sandbox/ops/merchants/M20029/routes/"+changeID+"/approve", f.approver, "", "").expect(t, 404)
	foreignRoutes := f.call(1, "GET", merchantRoutes, f.otherMerchant, "", "")
	foreignRoutes.expect(t, 200)
	if strings.Contains(foreignRoutes.body, changeID) {
		t.Fatal("cross-merchant routing metadata exposed")
	}
	// Recheck channel availability at approval time.
	f.call(0, "PUT", "/sandbox/ops/channels/qr/status", f.ops, "", `{"enabled":false}`).expect(t, 200)
	f.call(0, "POST", approve, f.approver, "", "").expect(t, 409)
	f.call(0, "PUT", "/sandbox/ops/channels/qr/status", f.ops, "", `{"enabled":true}`).expect(t, 200)
	admins := []string{f.opsAgain, f.approver2}
	winner := workspaceRaceWinner(t, raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", approve, admins[i], "", "")
	}), 200)
	if (winner["approved_by"] != "ops-submit" && winner["approved_by"] != "ops-approve-2") ||
		winner["approved_at"] == nil || winner["id"] != changeID {
		t.Fatal("approval audit was not persisted")
	}
	got := paymentCall(1, "route-after", "route-after", 201)
	assertRoute(got, qr, changeID)
	if got["next_action"].(map[string]any)["type"] != "display_qr" {
		t.Fatal("new order did not use the approved channel adapter")
	}
	replay := paymentCall(1, "route-before", "route-before", 200)
	assertRoute(replay, original, "")
	if replay["id"] != first["id"] || replay["next_action"].(map[string]any)["type"] != "redirect" {
		t.Fatal("existing order changed after route approval")
	}
	direct := strings.TrimSuffix(bodyFor(original), "}") + `,"activate":true}`
	f.call(0, "POST", routes, f.observer, "", direct).expect(t, 403)
	second := f.call(1, "POST", routes, f.ops, "", direct).object(t, 201)
	secondID := second["id"].(string)
	replayed := f.call(0, "POST", routes, f.ops, "", direct).object(t, 200)
	if second["approved_by"] != "ops-submit" || second["approved_at"] == nil || replayed["id"] != secondID {
		t.Fatal("direct activation did not preserve the operator or replay the active route")
	}
	if err := migrations.Apply(t.Context(), f.db); err != nil {
		t.Fatalf("schema replay after single-operator activation: %v", err)
	}
	assertRoute(paymentCall(0, "route-final", "route-final", 201), original, secondID)
	assertRoute(paymentCall(1, "route-after", "route-after", 200), qr, changeID)
	// Cancellation frees the pending slot without activating the proposed account.
	unknownAccount := assign(site, "unknown")
	cancelled := f.call(0, "POST", routes, f.ops, "", bodyFor(unknownAccount)).object(t, 201)["id"].(string)
	f.call(0, "POST", routes+"/"+cancelled+"/cancel", f.ops, "", "").expect(t, 200)
	f.call(1, "POST", routes+"/"+cancelled+"/approve", f.approver, "", "").expect(t, 409)
	unknownRoute := f.call(0, "POST", routes, f.ops, "", bodyFor(unknownAccount)).object(t, 201)["id"].(string)
	f.call(1, "POST", routes+"/"+unknownRoute+"/approve", f.opsAgain, "", "").expect(t, 200)
	unknown := paymentCall(0, "route-unknown", "route-unknown", 201)
	assertRoute(unknown, unknownAccount, unknownRoute)
	if unknown["status"] != "pending_confirmation" {
		t.Fatal("unsupported action must remain pending")
	}

	// Disable the selected channel. The unsupported result remains on the same account.
	f.call(0, "PUT", "/sandbox/ops/channels/unknown/status", f.ops, "", `{"enabled":false}`).expect(t, 200)
	rejected := paymentCall(1, "route-disabled", "route-disabled", 400)
	if rejected["error"].(map[string]any)["code"] != "payment_method_unavailable" {
		t.Fatal("unavailable active route did not fail closed")
	}
	matrix := f.call(0, "GET", "/v1/capabilities", f.legacy, "", "").object(t, 200)
	for _, option := range matrix["payments"].([]any) {
		if option.(map[string]any)["market"] == "IN" {
			t.Fatal("capabilities advertised an unavailable approved route")
		}
	}
	backToQR := f.call(0, "POST", routes, f.ops, "", bodyFor(qr)).object(t, 201)["id"].(string)
	f.call(1, "POST", routes+"/"+backToQR+"/approve", f.approver, "", "").expect(t, 200)
	assertRoute(paymentCall(1, "route-after-unknown", "route-after-unknown", 201), qr, backToQR)
	assertRoute(paymentCall(0, "route-final", "route-final", 200), original, secondID)
	assertRoute(paymentCall(1, "route-unknown", "route-unknown", 200), unknownAccount, unknownRoute)

	// The worker still queries the original, now disabled account after the switch.
	unknownID := unknown["id"].(string)
	f.exec(t, `UPDATE payments SET next_check_at=now() WHERE id=$1`, unknownID)
	f.exec(t, `UPDATE sandbox_channel_rate SET window_start=now()-interval '2 seconds' WHERE account_id=$1`, unknownAccount)
	worker := &server{db: f.db}
	if err := worker.reconcilePayment(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := f.call(1, "GET", "/v1/payments/"+unknownID, f.legacy, "", "").object(t, 200)
	assertRoute(found, unknownAccount, unknownRoute)
	f.call(0, "GET", "/v1/payments/"+unknownID, f.secondary, "", "").expect(t, 404)
	var checks, receipts int
	var originalReceipt bool
	if err := f.db.QueryRowContext(t.Context(), `SELECT check_attempts,
  (SELECT count(*) FROM sandbox_channel_receipts WHERE subject_id=$1),
  EXISTS (SELECT 1 FROM sandbox_channel_rate WHERE account_id=$2 AND window_start >= date_trunc('second', now()) - interval '1 second')
  FROM payments WHERE id=$1`, unknownID, unknownAccount).Scan(&checks, &receipts, &originalReceipt); err != nil ||
		checks != 1 || receipts != 1 || !originalReceipt || found["status"] != "pending_confirmation" {
		t.Fatal("original-account lookup changed the unknown result or dispatched again")
	}

	var active, superseded int
	if err := f.db.QueryRowContext(t.Context(), `
		SELECT count(*) FILTER (WHERE superseded_at IS NULL), count(*) FILTER (WHERE superseded_at IS NOT NULL)
		FROM collection_route_changes WHERE merchant_id='M10028' AND site_id=$1 AND approved_at IS NOT NULL`,
		site).Scan(&active, &superseded); err != nil || active != 1 || superseded != 3 {
		t.Fatal("expected one superseded and one active change")
	}
}

func TestSandboxPlatformAuditFailureRollsBackWrites(t *testing.T) {
	f := newControlsFixture(t)
	f.exec(t, `ALTER TABLE sandbox_platform_audit ADD CONSTRAINT synthetic_audit_failure CHECK (action <> 'channel.create')`)
	f.call(0, "POST", "/sandbox/ops/channels", f.ops, "", `{"id":"rollback","name":"Rollback","provider":"mock_pix","enabled":true}`).expect(t, 503)
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sandbox_platform_channels WHERE id='rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("platform write persisted without its audit")
	}
}
func TestPlatformGeneratedChannelCreation(t *testing.T) {
	f := newControlsFixture(t)
	const path = "/sandbox/ops/channels"
	const body = `{"provider":"mock_pix","enabled":true}`
	f.call(0, "POST", path, f.ops, "", body).expect(t, 400)
	f.call(0, "POST", path, f.observer, "generated", body).expect(t, 403)
	results := raceWorkspaceControl(func(i int) controlsResult {
		return f.call(i, "POST", path, f.ops, "generated", body)
	})
	if results[1].status == 201 {
		results[0], results[1] = results[1], results[0]
	}
	added, replay := results[0].object(t, 201), results[1].object(t, 200)
	id, _ := added["id"].(string)
	if !strings.HasPrefix(id, "channel_") || added["name"] != "Mock Pix" || replay["id"] != id {
		t.Fatal("concurrent retry did not return the generated channel")
	}
	f.call(0, "POST", path, f.ops, "generated", `{"name":" Mock Pix ","provider":"mock_pix","enabled":true}`).expect(t, 200)
	f.call(0, "POST", path, f.ops, "generated", `{"name":"Different","provider":"mock_pix","enabled":true}`).expect(t, 409)
	const legacy = `{"id":"old-pix","name":"Legacy Pix","provider":"mock_pix","enabled":false}`
	f.call(0, "POST", path, f.ops, "", legacy).expect(t, 201)
	f.call(0, "POST", path, f.ops, "", legacy).expect(t, 409)
	f.call(0, "POST", "/sandbox/ops/merchants/M10028/assignments", f.ops, "",
		`{"site_id":"default_M10028","channel_id":"old-pix"}`).expect(t, 409)
	f.call(0, "PUT", path+"/"+id+"/status", f.ops, "", `{"enabled":false}`).expect(t, 200)
	if f.call(1, "POST", path, f.ops, "generated", body).object(t, 200)["enabled"] != false {
		t.Fatal("retry restored the original channel status")
	}
	var audits int
	if err := f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sandbox_platform_audit
		WHERE action='channel.create' AND resource_id=$1`, id).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("expected one creation audit: %d, %v", audits, err)
	}
}
