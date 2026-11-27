package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"flowxpay.local/platform/migrations"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSandboxMemberAccess(t *testing.T) {
	url := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("run scripts/local-sandbox.sh test to create an isolated PostgreSQL instance")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := t.Context()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}

	addMember := func(id, merchant, role string) string {
		t.Helper()
		if _, err := db.ExecContext(ctx, `
			INSERT INTO merchant_members (id, merchant_id, name, role) VALUES ($1, $2, $1, $3)`, id, merchant, role); err != nil {
			t.Fatal(err)
		}
		return id
	}
	addSession := func(memberID, ttl string, revoked bool) string {
		t.Helper()
		var secret [32]byte
		rand.Read(secret[:])
		token := "fxs_" + hex.EncodeToString(secret[:])
		digest := sha256.Sum256([]byte(token))
		if _, err := db.ExecContext(ctx, `
			INSERT INTO merchant_sessions (token_hash, member_id, expires_at, revoked_at)
			VALUES ($1, $2, clock_timestamp() + $3::interval, CASE WHEN $4 THEN clock_timestamp() END)`,
			digest[:], memberID, ttl, revoked); err != nil {
			t.Fatal(err)
		}
		return token
	}

	adminID := addMember("test-admin", "M10028", "admin")
	viewerID := addMember("test-viewer", "M10028", "viewer")
	operatorID := addMember("test-operator", "M10028", "operator")
	otherID := addMember("test-harbor-admin", "M20029", "admin")
	admin := addSession(adminID, "1 hour", false)
	viewer := addSession(viewerID, "1 hour", false)
	operator := addSession(operatorID, "1 hour", false)
	secondOperatorSession := addSession(operatorID, "1 hour", false)
	other := addSession(otherID, "1 hour", false)
	expired := addSession(adminID, "-1 second", false)
	revoked := addSession(adminID, "1 hour", true)

	legacyKey := strings.Repeat("a", 32)
	app := &server{db: db, merchants: [2]sandboxMerchant{{ID: "M10028", Key: legacyKey}, {ID: "M20029", Key: strings.Repeat("b", 32)}}}
	handler := app.routes()
	const workspace = "/sandbox/workspace"
	call := func(method, path, token, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Content-Type", "application/json")
		// Caller-supplied tenant and role headers cannot influence authorization.
		r.Header.Set("X-Merchant-ID", "M10028")
		r.Header.Set("X-Role", "admin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d, body %s", method, path, w.Code, want, w.Body)
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	call("GET", workspace+"/session", "", "", 401)
	call("GET", workspace+"/session", legacyKey, "", 401)
	call("GET", workspace+"/session", "fxs_"+strings.Repeat("0", 64), "", 401)
	call("GET", workspace+"/session", expired, "", 401)
	call("GET", workspace+"/session", revoked, "", 401)
	call("GET", "/v1/capabilities", admin, "", 401)
	call("GET", "/v1/capabilities", legacyKey, "", 200)
	if result := call("GET", workspace+"/session?merchant_id=M10028", other, "", 200); result["merchant_id"] != "M20029" || result["id"] != otherID {
		t.Fatalf("session returned incorrect identity: %v", result)
	}

	call("GET", workspace+"/members/"+viewerID, viewer, "", 403)
	call("GET", workspace+"/members/"+viewerID, other, "", 404)
	call("POST", workspace+"/members/"+operatorID+"/disable", operator, "", 403)
	call("POST", workspace+"/members/"+operatorID+"/disable", other, "", 404)
	call("POST", workspace+"/members/"+adminID+"/disable", admin, "", 400)
	call("GET", workspace+"/members/"+viewerID, admin, "", 200)

	const siteBody = `{"name":"Cedar Site"}`
	call("POST", workspace+"/sites", viewer, siteBody, 403)
	call("POST", workspace+"/sites", other, `{"name":"Injected","merchant_id":"M10028"}`, 400)
	site := call("POST", workspace+"/sites", operator, siteBody, 201)
	siteID := site["id"].(string)
	call("GET", workspace+"/sites/"+siteID, viewer, "", 200)
	call("GET", workspace+"/sites/"+siteID+"?merchant_id=M10028", other, "", 404)

	accountBody := fmt.Sprintf(`{"site_id":%q,"provider":"mock_pix"}`, siteID)
	for _, token := range []string{viewer, other, admin, operator} {
		call("POST", workspace+"/channel-accounts", token, accountBody, 403)
	}
	const accountID = "preexisting-test-account"
	if _, err := db.ExecContext(ctx, `INSERT INTO sandbox_channel_accounts (id,merchant_id,site_id,provider)
 VALUES ($1,'M10028',$2,'mock_pix')`, accountID, siteID); err != nil {
		t.Fatal(err)
	}

	call("GET", workspace+"/channel-accounts/"+accountID, viewer, "", 200)
	call("GET", workspace+"/channel-accounts/"+accountID, other, "", 404)

	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	call("GET", workspace+"/channel-accounts/"+accountID, operator, "", 200)
	if _, err := db.ExecContext(ctx, "UPDATE merchant_members SET role = 'viewer' WHERE id = $1", operatorID); err != nil {
		t.Fatal(err)
	}
	call("POST", workspace+"/sites", operator, siteBody, 403)
	if result := call("POST", workspace+"/members/"+operatorID+"/disable", admin, "", 200); result["active"] != false {
		t.Fatalf("member was not disabled: %v", result)
	}
	call("POST", workspace+"/members/"+operatorID+"/disable", admin, "", 200)
	call("GET", workspace+"/session", operator, "", 401)
	call("POST", workspace+"/sites", secondOperatorSession, siteBody, 401)
	call("GET", workspace+"/sites/"+siteID, viewer, "", 200)

	assertConstraint := func(code, constraint, query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code || pgErr.ConstraintName != constraint {
			t.Fatalf("expected PostgreSQL constraint %s (%s), got %v", constraint, code, err)
		}
	}
	const insertAccount = `INSERT INTO sandbox_channel_accounts (id, merchant_id, site_id, environment, provider)
		VALUES ('invalid-test-account', $1, $2, $3, $4)`
	assertConstraint("23503", "sandbox_account_site_owner", insertAccount, "M20029", siteID, "sandbox", "mock_pix")
	assertConstraint("23514", "sandbox_channel_accounts_environment_check", insertAccount, "M10028", siteID, "production", "mock_pix")
	assertConstraint("23514", "sandbox_channel_accounts_provider_check", insertAccount, "M10028", siteID, "sandbox", "live_pix")
	assertConstraint("23514", "merchant_members_role_check",
		`INSERT INTO merchant_members (id, merchant_id, name, role) VALUES ('bad-role', 'M10028', 'Bad Role', 'owner')`)

	var sites, accounts int
	err = db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM merchant_sites), (SELECT count(*) FROM sandbox_channel_accounts)").Scan(&sites, &accounts)
	if err != nil || sites != 3 || accounts != 7 {
		t.Fatalf("denied requests altered workspace inventory: sites=%d accounts=%d err=%v", sites, accounts, err)
	}

	list := func(path, token string, wantIDs ...string) []map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", workspace+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var items []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || items == nil || len(items) != len(wantIDs) {
			t.Fatalf("incorrect list for %s: %d %s", path, w.Code, w.Body)
		}
		for i, id := range wantIDs {
			if items[i]["id"] != id {
				t.Fatalf("incorrect list item for %s: %v", path, items[i])
			}
		}
		return items
	}
	defaultAccounts := func(merchant string) []string {
		prefix := "default_" + merchant + "_"
		return []string{prefix + "mock_card", prefix + "mock_in_redirect", prefix + "mock_pix"}
	}
	list("/sites?merchant_id=M10028", other, "default_M20029")
	list("/channel-accounts", other, defaultAccounts("M20029")...)
	list("/sites?merchant_id=M20029", viewer, siteID, "default_M10028")
	list("/channel-accounts?merchant_id=M20029", viewer, append(defaultAccounts("M10028"), accountID)...)
	list("/members", other, otherID)
	if members := list("/members", admin, adminID, operatorID, viewerID); members[1]["active"] != false {
		t.Fatalf("list lost disabled member status: %v", members)
	}
	call("GET", workspace+"/members", viewer, "", 403)
	call("GET", workspace+"/sites", legacyKey, "", 401)
	call("GET", workspace+"/channel-accounts", secondOperatorSession, "", 401)

	const retryKey = "workspace-retry-key"
	write := func(path, token, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", workspace+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	concurrentCreate := func(path, body string) string {
		t.Helper()
		var results [6]*httptest.ResponseRecorder
		var group sync.WaitGroup
		start := make(chan struct{})
		for i := range results {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				results[i] = write(path, admin, retryKey, body)
			}()
		}
		close(start)
		group.Wait()
		var id string
		var created int
		for _, result := range results {
			if result.Code == 201 {
				created++
			} else if result.Code != 200 {
				t.Fatalf("unexpected concurrent create response %d: %s", result.Code, result.Body)
			}
			var item struct{ ID string }
			if err := json.Unmarshal(result.Body.Bytes(), &item); err != nil || item.ID == "" {
				t.Fatalf("invalid concurrent create response: %s", result.Body)
			}
			if id != "" && id != item.ID {
				t.Fatalf("concurrent writes created multiple resources: %q and %q", id, item.ID)
			}
			id = item.ID
		}
		if created != 1 {
			t.Fatalf("want one insert for %s, got %d", path, created)
		}
		return id
	}
	newSite := concurrentCreate("/sites", `{"name":"Retry Site"}`)
	list("/sites", viewer, siteID, "default_M10028", newSite)
	list("/channel-accounts", viewer, append(defaultAccounts("M10028"), accountID)...)
	for _, check := range []struct {
		path, token, key, body string
		want                   int
	}{
		{"/sites", admin, retryKey, `{"name":"Different name"}`, 409},
		{"/sites", admin, "", siteBody, 400},
		{"/sites", viewer, retryKey, `{"name":"Retry Site"}`, 403},
		{"/sites", other, retryKey, `{"name":"Retry Site"}`, 201},
		{"/channel-accounts", admin, retryKey, accountBody, 403},
		{"/channel-accounts", other, retryKey, accountBody, 403},
	} {
		if result := write(check.path, check.token, check.key, check.body); result.Code != check.want {
			t.Fatalf("POST %s: got %d, want %d: %s", check.path, result.Code, check.want, result.Body)
		}
	}
}
